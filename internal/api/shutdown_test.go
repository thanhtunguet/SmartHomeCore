package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thanhtunguet/homecore/internal/agent"
	"github.com/thanhtunguet/homecore/internal/agent/protocol"
	"github.com/thanhtunguet/homecore/internal/agentctl"
	"github.com/thanhtunguet/homecore/internal/api/gen"
	"github.com/thanhtunguet/homecore/internal/commands"
	"github.com/thanhtunguet/homecore/internal/config"
)

// The Phase 3 integration tests: the real Controller router (JWT, SQLite, monitor with its real TCP
// probe, command engine, shutdown dispatcher, controller key) talking over real HTTP to the real
// Agent server (internal/agent) with a fake PowerController. The "device" is a TCP listener the
// test closes to play the machine going down. Every Controller response is also checked against
// api/openapi.yaml by devEnv.

type fakePower struct{ off, reboot atomic.Int32 }

func (p *fakePower) PowerOff(context.Context) error { p.off.Add(1); return nil }
func (p *fakePower) Reboot(context.Context) error   { p.reboot.Add(1); return nil }

// realAgent is an agent.Server behind an httptest server that counts the requests reaching it.
type realAgent struct {
	srv   *httptest.Server
	ag    *agent.Server
	power *fakePower
	posts atomic.Int32 // POST /agent/v1/commands received
	port  int
}

// startAgent starts a real agent that trusts the given public keys. cooldown is the agent's own
// post-acceptance cooldown (a long one makes a second, different command `already_scheduled`).
func startAgent(t *testing.T, trusted ...ed25519.PublicKey) *realAgent {
	t.Helper()
	cfg := config.Default().Agent
	cfg.DataDir = t.TempDir()
	cfg.ActionDelay = 20 * time.Millisecond
	for i, k := range trusted {
		cfg.TrustedControllers = append(cfg.TrustedControllers, config.TrustedController{Name: "ctl" + strconv.Itoa(i), PublicKey: protocol.EncodePublicKey(k)})
	}
	power := &fakePower{}
	ag, err := agent.New(cfg, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)), power)
	if err != nil {
		t.Fatal(err)
	}
	ra := &realAgent{ag: ag, power: power}
	inner := ag.Handler()
	ra.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == protocol.CommandsPath {
			ra.posts.Add(1)
		}
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(ra.srv.Close)
	_, p, _ := net.SplitHostPort(strings.TrimPrefix(ra.srv.URL, "http://"))
	ra.port, _ = strconv.Atoi(p)
	return ra
}

// machine is a TCP listener standing in for the device's monitored port.
type machine struct {
	ln   net.Listener
	port int
}

func startMachine(t *testing.T) *machine {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	m := &machine{ln: ln, port: ln.Addr().(*net.TCPAddr).Port}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return m
}

func (m *machine) powerOff() { _ = m.ln.Close() }

// shutdownEnv wires a devEnv with the real TCP probe, a machine, and a real agent that trusts the
// Controller's key; the device is bound to it and observed online once.
type shutdownEnv struct {
	*devEnv
	agent  *realAgent
	mach   *machine
	device gen.Device
	path   string
}

func newShutdownEnv(t *testing.T, mutate func(*config.Controller)) *shutdownEnv {
	t.Helper()
	e := newDevEnvOpts(t, mutate, true)
	se := &shutdownEnv{devEnv: e, mach: startMachine(t)}
	pub, err := protocol.ParsePublicKey(e.key.Public().PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	se.agent = startAgent(t, pub)
	se.device = e.mkDevice(map[string]any{
		"name": "gaming pc", "ip_address": "127.0.0.1", "monitor_port": se.mach.port,
		"agent": map[string]any{"agent_id": se.agent.ag.ID(), "port": se.agent.port},
	})
	se.path = "/api/v1/devices/" + se.device.Id.String()
	e.mon.CheckDue(context.Background()) // the machine is up: online
	if g := decode[gen.Device](t, e.as("GET", se.path, nil)); g.Status != gen.DeviceStatusOnline {
		t.Fatalf("setup: device is %s", g.Status)
	}
	return se
}

// settle lets the throttle window (1 s per device) pass.
func settle() { time.Sleep(1100 * time.Millisecond) }

func (se *shutdownEnv) command(id gen.Command) gen.Command {
	se.t.Helper()
	return decode[gen.Command](se.t, ok(se.t, se.as("GET", "/api/v1/commands/"+id.Id.String(), nil), 200))
}

func (se *shutdownEnv) shutdown() gen.Command {
	se.t.Helper()
	return decode[gen.Command](se.t, ok(se.t, se.as("POST", se.path+"/shutdown", nil), 202))
}

func (se *shutdownEnv) waitPower(n int32) {
	se.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for se.agent.power.off.Load() < n && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := se.agent.power.off.Load(); got != n {
		se.t.Fatalf("power-off calls: %d, want %d", got, n)
	}
}

type auditRow struct{ action, actor, target, result, details string }

func (e *devEnv) auditRows(action string) []auditRow {
	e.t.Helper()
	rows, err := e.db.Query(`SELECT action, actor, target_id, result, details FROM audit_log WHERE action = ? ORDER BY id`, action)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []auditRow
	for rows.Next() {
		var r auditRow
		if err := rows.Scan(&r.action, &r.actor, &r.target, &r.result, &r.details); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

// REQ-SHD-003..012, REQ-CMD-011: the full happy path with idempotency and the audit trail.
func TestShutdownEndToEndWithARealAgent(t *testing.T) {
	se := newShutdownEnv(t, nil)

	// 202 + a command that is already acknowledged by the agent.
	c := se.shutdown()
	if c.Type != "shutdown" || c.Status != gen.CommandStatusAcknowledged || c.DeadlineAt == nil || c.Error != nil || c.ErrorCode != nil ||
		c.RequestedBy == nil || c.RequestedBy.String() != se.userID {
		t.Fatalf("shutdown response: %+v", c)
	}
	if left := time.Until(*c.DeadlineAt); left < 110*time.Second || left > 121*time.Second {
		t.Fatalf("deadline_at is %v away, want about the 120 s default", left)
	}
	// The lifecycle went pending -> sent -> acknowledged (both timestamps are set).
	var sentAt, ackAt *int64
	if err := se.db.QueryRow(`SELECT sent_at, acknowledged_at FROM commands WHERE id = ?`, c.Id.String()).Scan(&sentAt, &ackAt); err != nil || sentAt == nil || ackAt == nil {
		t.Fatalf("sent_at/acknowledged_at: %v %v %v", sentAt, ackAt, err)
	}
	if got := se.agent.posts.Load(); got != 1 {
		t.Fatalf("%d requests reached the agent", got)
	}
	// The agent executes the (fake) power action exactly once, after acknowledging.
	se.waitPower(1)

	// The device is shutting down; its agent is ready and was contacted just now.
	g := decode[gen.Device](t, ok(t, se.as("GET", se.path, nil), 200))
	if g.PowerTransition != "shutting_down" || g.Agent == nil || g.Agent.Status == nil || *g.Agent.Status != gen.AgentBindingStatusReady ||
		g.Agent.LastOkAt == nil || time.Since(*g.Agent.LastOkAt) > 10*time.Second {
		t.Fatalf("device while shutting down: %+v agent=%+v", g, g.Agent)
	}

	// Repeating the request is not an error: the same command, and the agent is not contacted again.
	settle()
	again := se.shutdown()
	if again.Id != c.Id || again.Status != gen.CommandStatusAcknowledged {
		t.Fatalf("idempotent shutdown: %+v", again)
	}
	if got := se.agent.posts.Load(); got != 1 || se.agent.power.off.Load() != 1 {
		t.Fatalf("a repeated request reached the agent: posts=%d power=%d", got, se.agent.power.off.Load())
	}

	// Still online: the command stays acknowledged. The machine goes down: the monitor needs the
	// hysteresis (2 failures) and then completes the command.
	se.mon.CheckDue(context.Background())
	if got := se.command(c); got.Status != gen.CommandStatusAcknowledged {
		t.Fatalf("command before the machine went down: %+v", got)
	}
	se.mach.powerOff()
	se.mon.CheckDue(context.Background())
	if got := se.command(c); got.Status != gen.CommandStatusAcknowledged {
		t.Fatalf("one failed probe must not complete it (hysteresis): %+v", got)
	}
	se.mon.CheckDue(context.Background())
	done := se.command(c)
	if done.Status != gen.CommandStatusCompleted || done.CompletedAt == nil {
		t.Fatalf("after the machine went offline: %+v", done)
	}
	if g := decode[gen.Device](t, se.as("GET", se.path, nil)); g.Status != gen.DeviceStatusOffline || g.PowerTransition != "none" {
		t.Fatalf("device after completion: %+v", g)
	}

	// Audit: one row per request, with the acting user, the device and the command.
	rows := se.auditRows("device.shutdown")
	if len(rows) != 2 {
		t.Fatalf("audit rows: %+v", rows)
	}
	for _, r := range rows {
		if r.actor != "alice" || r.target != se.device.Id.String() || r.result != "ok" ||
			!strings.Contains(r.details, `"command_id":"`+c.Id.String()+`"`) || !strings.Contains(r.details, `"status":"acknowledged"`) {
			t.Fatalf("audit row: %+v", r)
		}
	}
}

// REQ-SHD-008, REQ-CMD-004 for shutdown (ADR-012): an offline device completes at once and the agent hears nothing.
func TestShutdownOfAnOfflineDeviceCompletesWithoutContactingTheAgent(t *testing.T) {
	se := newShutdownEnv(t, nil)
	se.mach.powerOff()
	// Two manual probes make it offline (a device without an active command is not due for 30 s).
	want(t, se.as("POST", se.path+"/probe", nil), 200)
	want(t, se.as("POST", se.path+"/probe", nil), 200)
	if g := decode[gen.Device](t, se.as("GET", se.path, nil)); g.Status != gen.DeviceStatusOffline {
		t.Fatalf("setup: %s", g.Status)
	}

	c := se.shutdown()
	if c.Status != gen.CommandStatusCompleted || c.CompletedAt == nil || c.ErrorCode != nil {
		t.Fatalf("shutdown of an offline device: %+v", c)
	}
	if got := se.agent.posts.Load(); got != 0 {
		t.Fatalf("%d requests reached the agent", got)
	}
	if se.agent.power.off.Load() != 0 {
		t.Fatal("the agent powered off an already offline machine")
	}
	rows := se.auditRows("device.shutdown")
	if len(rows) != 1 || rows[0].result != "ok" || !strings.Contains(rows[0].details, `"status":"completed"`) {
		t.Fatalf("audit: %+v", rows)
	}
}

// A stale `offline` status is confirmed by a fresh probe first (as for wake), so a running machine
// is still shut down.
func TestStaleOfflineStatusIsConfirmedByAFreshProbe(t *testing.T) {
	se := newShutdownEnv(t, nil)
	if _, err := se.db.Exec(`UPDATE devices SET status = 'offline' WHERE id = ?`, se.device.Id.String()); err != nil {
		t.Fatal(err)
	}
	c := se.shutdown()
	if c.Status != gen.CommandStatusAcknowledged || se.agent.posts.Load() != 1 {
		t.Fatalf("a running machine was not shut down: %+v posts=%d", c, se.agent.posts.Load())
	}
}

// REQ-SHD-005: an agent that does not trust the Controller's key says 401; the code is passed through.
func TestShutdownWithAnUntrustedKeyFailsWithTheAgentsCode(t *testing.T) {
	e := newDevEnvOpts(t, nil, true)
	mach := startMachine(t)
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	ra := startAgent(t, other) // trusts somebody else
	d := e.mkDevice(map[string]any{"name": "pc", "ip_address": "127.0.0.1", "monitor_port": mach.port,
		"agent": map[string]any{"agent_id": ra.ag.ID(), "port": ra.port}})

	c := decode[gen.Command](t, ok(t, e.as("POST", "/api/v1/devices/"+d.Id.String()+"/shutdown", nil), 202))
	if c.Status != gen.CommandStatusFailed || c.ErrorCode == nil || *c.ErrorCode != "unauthorized" ||
		c.Error == nil || !strings.Contains(*c.Error, e.key.ID) {
		t.Fatalf("untrusted key: %+v (%v)", c, c.Error)
	}
	if ra.power.off.Load() != 0 {
		t.Fatal("an unauthenticated command powered the machine off")
	}
	// The agent answered, so it is reachable even though it refused us.
	g := decode[gen.Device](t, e.as("GET", "/api/v1/devices/"+d.Id.String(), nil))
	if g.PowerTransition != "none" || g.Agent.Status == nil || *g.Agent.Status != gen.AgentBindingStatusReady {
		t.Fatalf("device after a refused shutdown: %+v %+v", g, g.Agent)
	}
	rows := e.auditRows("device.shutdown")
	if len(rows) != 1 || rows[0].result != "failed" || !strings.Contains(rows[0].details, `"error_code":"unauthorized"`) {
		t.Fatalf("audit: %+v", rows)
	}
}

// REQ-SHD-007/010: a dead agent fails the command within seconds, status becomes unreachable.
func TestShutdownWithTheAgentDownFailsFast(t *testing.T) {
	se := newShutdownEnv(t, nil)
	se.agent.srv.Close() // the agent process is gone

	start := time.Now()
	c := se.shutdown()
	if el := time.Since(start); el > 5*time.Second {
		t.Fatalf("fail-fast took %v", el)
	}
	if c.Status != gen.CommandStatusFailed || c.ErrorCode == nil || *c.ErrorCode != "agent_unreachable" || c.Error == nil || !strings.Contains(*c.Error, "127.0.0.1") {
		t.Fatalf("agent down: %+v", c)
	}
	g := decode[gen.Device](t, se.as("GET", se.path, nil))
	if g.PowerTransition != "none" || g.Status != gen.DeviceStatusOnline || *g.Agent.Status != gen.AgentBindingStatusUnreachable {
		t.Fatalf("device: %+v agent=%+v", g, g.Agent)
	}
	if rows := se.auditRows("device.shutdown"); len(rows) != 1 || rows[0].result != "failed" || !strings.Contains(rows[0].details, "agent_unreachable") {
		t.Fatalf("audit: %+v", rows)
	}
	// The user can simply press the button again: a failed command does not block a new one.
	settle()
	if c2 := se.shutdown(); c2.Id == c.Id || c2.Status != gen.CommandStatusFailed {
		t.Fatalf("second attempt: %+v", c2)
	}
}

// REQ-SHD-005: a Controller clock far from the agent's is reported as clock_skew with the drift.
func TestShutdownWithClockSkewReportsTheDrift(t *testing.T) {
	se := newShutdownEnv(t, nil)
	se.engine.Register(commands.TypeShutdown, &agentctl.ShutdownDispatcher{
		Signer: se.key, Status: se.repo, Now: func() time.Time { return time.Now().Add(10 * time.Minute) },
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	c := se.shutdown()
	if c.Status != gen.CommandStatusFailed || c.ErrorCode == nil || *c.ErrorCode != "clock_skew" ||
		c.Error == nil || !strings.Contains(*c.Error, "agent_time") || !strings.Contains(*c.Error, "drift about -10m") {
		t.Fatalf("clock skew: %+v (%v)", c, c.Error)
	}
	if se.agent.power.off.Load() != 0 {
		t.Fatal("a skewed command was executed")
	}
}

// REQ-SHD-008 / ADR-012: no agent binding is the one unavoidable 409; nothing is stored or audited.
func TestShutdownWithoutAnAgentBindingIs409(t *testing.T) {
	e := newDevEnvOpts(t, nil, true)
	d := e.mkDevice(map[string]any{"name": "no agent"})
	rec := e.as("POST", "/api/v1/devices/"+d.Id.String()+"/shutdown", nil)
	want(t, rec, 409)
	if errCode(t, rec) != "conflict" || !strings.Contains(rec.Body.String(), "agent binding") {
		t.Fatalf("body: %s", rec.Body)
	}
	if l := decode[gen.CommandList](t, e.as("GET", "/api/v1/commands", nil)); len(l.Items) != 0 {
		t.Fatalf("a rejected shutdown left a command behind: %+v", l.Items)
	}
	if rows := e.auditRows("device.shutdown"); len(rows) != 0 {
		t.Fatalf("a rejected request was audited: %+v", rows)
	}
	// Unknown device: 404.
	want(t, e.as("POST", "/api/v1/devices/0193a0b2-7c4e-7f00-8000-000000000001/shutdown", nil), 404)
}

func TestShutdownIsThrottledPerDevice(t *testing.T) {
	se := newShutdownEnv(t, nil)
	se.shutdown()
	rec := se.as("POST", se.path+"/shutdown", nil)
	want(t, rec, 429)
	if errCode(t, rec) != "rate_limited" || rec.Header().Get("Retry-After") != "1" || se.agent.posts.Load() != 1 {
		t.Fatalf("throttle: %s posts=%d", rec.Body, se.agent.posts.Load())
	}
}

// Acknowledged, but the machine never goes offline: timed_out at the deadline.
func TestShutdownTimesOutWhenTheMachineStaysUp(t *testing.T) {
	se := newShutdownEnv(t, func(c *config.Controller) { c.Commands.ShutdownDeadline = 150 * time.Millisecond })
	c := se.shutdown()
	if c.Status != gen.CommandStatusAcknowledged {
		t.Fatalf("shutdown: %+v", c)
	}
	time.Sleep(250 * time.Millisecond)
	if err := se.engine.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := se.command(c); got.Status != gen.CommandStatusTimedOut {
		t.Fatalf("after the deadline: %+v", got)
	}
	if g := decode[gen.Device](t, se.as("GET", se.path, nil)); g.PowerTransition != "none" {
		t.Fatalf("power_transition %q after a timeout", g.PowerTransition)
	}
}

// A wake and a shutdown of one device are independent (ADR-012).
func TestWakeAndShutdownAreIndependent(t *testing.T) {
	se := newShutdownEnv(t, nil)
	c := se.shutdown()
	settle()
	se.as("PATCH", se.path, map[string]any{"mac_address": "aa:bb:cc:dd:ee:ff"})
	// The device is online, so a wake completes at once; the shutdown stays active.
	w := decode[gen.Command](t, ok(t, se.as("POST", se.path+"/wake", nil), 202))
	if w.Type != "wake" || w.Id == c.Id || se.command(c).Status != gen.CommandStatusAcknowledged {
		t.Fatalf("wake %+v, shutdown %+v", w, se.command(c))
	}
}

// REQ-SHD-002, REQ-API-024: GET /system/controller-key: the public key an operator pastes into the agent, never the private one.
func TestControllerKeyEndpoint(t *testing.T) {
	e := newDevEnv(t)
	rec := ok(t, e.as("GET", "/api/v1/system/controller-key", nil), 200)
	k := decode[gen.ControllerKey](t, rec)
	pub := e.key.Public()
	if k.KeyId != pub.KeyID || k.PublicKey != pub.PublicKey || k.Algorithm != "ed25519" || len(k.KeyId) != 16 {
		t.Fatalf("key: %+v want %+v", k, pub)
	}
	// Exactly key_id, algorithm and public_key: nothing else can carry private key material.
	var fields map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &fields); err != nil || len(fields) != 3 {
		t.Fatalf("unexpected fields: %v %v", fields, err)
	}
	if pk, err := protocol.ParsePublicKey(k.PublicKey); err != nil || len(pk) != 32 {
		t.Fatalf("public key is not 32 bytes of standard base64: %v", err)
	}
	rec = e.call("GET", "/api/v1/system/controller-key", nil, "")
	want(t, rec, 401)
}

// The key the agent is told to trust is the one that signs: a verifier configured with the API's
// answer accepts what the dispatcher sends.
func TestAgentTrustingTheAPIKeyAcceptsTheDispatcher(t *testing.T) {
	e := newDevEnvOpts(t, nil, true)
	k := decode[gen.ControllerKey](t, e.as("GET", "/api/v1/system/controller-key", nil))
	pub, err := protocol.ParsePublicKey(k.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	mach := startMachine(t)
	ra := startAgent(t, pub)
	d := e.mkDevice(map[string]any{"name": "pc", "ip_address": "127.0.0.1", "monitor_port": mach.port,
		"agent": map[string]any{"agent_id": ra.ag.ID(), "port": ra.port}})
	c := decode[gen.Command](t, ok(t, e.as("POST", "/api/v1/devices/"+d.Id.String()+"/shutdown", nil), 202))
	if c.Status != gen.CommandStatusAcknowledged {
		t.Fatalf("%+v", c)
	}
}
