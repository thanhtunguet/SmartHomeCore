package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/thanhtunguet/homecore/internal/activity"
	"github.com/thanhtunguet/homecore/internal/agentctl"
	"github.com/thanhtunguet/homecore/internal/api/gen"
	"github.com/thanhtunguet/homecore/internal/audit"
	"github.com/thanhtunguet/homecore/internal/auth"
	"github.com/thanhtunguet/homecore/internal/commands"
	"github.com/thanhtunguet/homecore/internal/config"
	"github.com/thanhtunguet/homecore/internal/controllerkey"
	"github.com/thanhtunguet/homecore/internal/ddns"
	"github.com/thanhtunguet/homecore/internal/ddns/ddnstest"
	"github.com/thanhtunguet/homecore/internal/devices"
	"github.com/thanhtunguet/homecore/internal/monitoring"
	"github.com/thanhtunguet/homecore/internal/platform"
	"github.com/thanhtunguet/homecore/internal/secrets"
	"github.com/thanhtunguet/homecore/internal/storage"
	"github.com/thanhtunguet/homecore/internal/system"
)

// devEnv is the real Controller router (real JWT authentication, real SQLite, the real device
// service, monitor and command engine) with fakes only at the network edge: a scripted TCP prober
// and a recording Wake-on-LAN sender. Every response it returns has already been validated against
// api/openapi.yaml.
type devEnv struct {
	t      *testing.T
	h      http.Handler
	db     *storage.DB
	spec   *specChecker
	mon    *monitoring.Monitor
	engine *commands.Engine
	key    *controllerkey.Key
	repo   *devices.SQLiteRepository
	token  string
	userID string

	// Phase 4: DDNS against a fake Cloudflare and a scripted public-IP discoverer, and metrics.
	cf      *ddnstest.Cloudflare
	ip      *scriptedIP
	ddnsSvc *ddns.Service
	metrics *swapMetrics
	logs    *logBuffer

	mu      sync.Mutex
	up      map[int]bool // monitor_port → reachable
	sent    []string     // magic packets: "mac broadcast:port"
	sendErr error        // when set, sending a magic packet fails
	calls   int
}

type wakeRecorder struct{ e *devEnv }

func (w wakeRecorder) SendWake(_ context.Context, mac net.HardwareAddr, broadcast string, port int) error {
	w.e.mu.Lock()
	defer w.e.mu.Unlock()
	if w.e.sendErr != nil {
		return w.e.sendErr
	}
	w.e.sent = append(w.e.sent, mac.String()+" "+broadcast+":"+strconv.Itoa(port))
	return nil
}

type engineProber struct{ m *monitoring.Monitor }

func (p engineProber) Probe(ctx context.Context, id string) (bool, error) {
	out, err := p.m.ProbeNow(ctx, id)
	return out.Result.Reachable, err
}

func newDevEnv(t *testing.T) *devEnv { return newDevEnvCfg(t, nil) }

// newDevEnvCfg lets a test adjust the Controller configuration (for example the command deadlines).
func newDevEnvCfg(t *testing.T, mutate func(*config.Controller)) *devEnv {
	t.Helper()
	return newDevEnvOpts(t, mutate, false)
}

// newDevEnvOpts is newDevEnvCfg with a choice of probe: the scripted one (realProbe false, the
// default: tests flip ports with setUp) or the monitor's real TCP probe (the integration tests with
// a real agent use a real listener they close).
func newDevEnvOpts(t *testing.T, mutate func(*config.Controller), realProbe bool) *devEnv {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	sealer, err := secrets.NewSealer(bytes.Repeat([]byte{4}, secrets.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	logs := &logBuffer{}
	log := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	cfg := config.Default().Controller
	cfg.Auth.APIRatePerMinute = 0 // the per-IP limiter is covered elsewhere
	if mutate != nil {
		mutate(&cfg)
	}
	writer := audit.NewWriter(db, nil)
	authSvc, err := auth.NewService(ctx, auth.Config{
		DB: db, Sealer: sealer, Audit: writer, Log: log,
		AccessTTL: cfg.Auth.AccessTokenTTL, RefreshTTL: cfg.Auth.RefreshTokenTTL,
		Params: auth.Params{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32},
	})
	if err != nil {
		t.Fatal(err)
	}
	user, err := authSvc.Accounts().Create(ctx, "alice", pw, "test")
	if err != nil {
		t.Fatal(err)
	}
	pair, err := authSvc.Login(ctx, "alice", pw, "192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}

	e := &devEnv{t: t, db: db, spec: newSpecChecker(t), token: pair.AccessToken, userID: user.ID, up: map[int]bool{}, logs: logs}
	e.cf = ddnstest.New(cfTestToken)
	t.Cleanup(e.cf.Close)
	e.cf.Zones = []ddnstest.Zone{{ID: "zone1", Name: "example.com"}, {ID: "zone2", Name: "Another.org"}}
	e.cf.Records["zone1"] = []ddnstest.Record{
		{ID: "rec1", Name: "home.example.com", Type: "A", Content: "8.8.4.4", TTL: 300},
		{ID: "rec2", Name: "home6.example.com", Type: "AAAA", Content: "2606:4700:4700::1001", TTL: 1, Proxied: true},
		{ID: "rec3", Name: "alias.example.com", Type: "CNAME", Content: "home.example.com", TTL: 1},
		{ID: "rec4", Name: "cam.example.com", Type: "A", Content: "8.8.4.4", TTL: 120},
	}
	e.cf.Records["zone2"] = []ddnstest.Record{{ID: "rec9", Name: "x.another.org", Type: "A", Content: "8.8.4.4", TTL: 1}}
	cfClient, err := ddns.NewCloudflare(e.cf.URL(), true, &http.Client{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	e.ip = &scriptedIP{v4: "8.8.8.8", v6: "2606:4700:4700::1111"}
	e.ddnsSvc = ddns.NewService(ddns.ServiceConfig{Repo: ddns.NewSQLiteRepository(db), Sealer: sealer, DNS: cfClient, IP: e.ip, Log: log})
	e.metrics = &swapMetrics{p: &system.Cached{Provider: system.NewGopsutil(system.GopsutilConfig{DataDir: t.TempDir(), Started: time.Now()}), TTL: time.Second}}
	devRepo := devices.NewSQLiteRepository(db)
	devSvc := devices.NewService(devRepo, nil)
	monCfg := monitoring.Config{Store: devRepo, Log: log, Tick: 5 * time.Second, Concurrency: 4, OfflineThreshold: 2}
	if !realProbe {
		monCfg.Probe = func(_ context.Context, _ string, port int, _ time.Duration) monitoring.ProbeResult {
			e.mu.Lock()
			defer e.mu.Unlock()
			if e.up[port] {
				return monitoring.ProbeResult{Reachable: true, Latency: 3 * time.Millisecond}
			}
			return monitoring.ProbeResult{Err: context.DeadlineExceeded}
		}
	}
	e.mon = monitoring.New(monCfg)
	e.engine = commands.NewEngine(commands.Config{
		Repo: commands.NewSQLiteRepository(db), Devices: devSvc, Prober: engineProber{e.mon}, Log: log,
		WakeDeadline: cfg.Commands.WakeDeadline, ShutdownDeadline: cfg.Commands.ShutdownDeadline,
	})
	e.engine.Register(commands.TypeWake, commands.WakeDispatcher{Sender: wakeRecorder{e}})
	key, _, err := controllerkey.LoadOrCreate(ctx, db, sealer, nil)
	if err != nil {
		t.Fatal(err)
	}
	e.key, e.repo = key, devRepo
	e.engine.Register(commands.TypeShutdown, &agentctl.ShutdownDispatcher{Signer: key, Status: devRepo, Log: log})
	e.mon.SetObserver(e.engine)

	srv, err := New(Deps{
		Config: cfg, Log: log, DB: db, Auth: authSvc,
		Devices: devSvc, Commands: e.engine, Monitor: e.mon, Audit: writer,
		ControllerKey: key, Activity: activity.NewService(db), DDNS: e.ddnsSvc, Metrics: e.metrics,
	})
	if err != nil {
		t.Fatal(err)
	}
	e.h = srv.Handler()
	return e
}

// swapMetrics lets a test replace the metrics provider behind the router.
type swapMetrics struct {
	mu sync.Mutex
	p  platform.MetricsProvider
}

func (s *swapMetrics) set(p platform.MetricsProvider) { s.mu.Lock(); s.p = p; s.mu.Unlock() }

func (s *swapMetrics) Collect(ctx context.Context) (platform.Metrics, error) {
	s.mu.Lock()
	p := s.p
	s.mu.Unlock()
	return p.Collect(ctx)
}

// logBuffer collects the Controller's log output so a test can check no secret reached it.
type logBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *logBuffer) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

func (e *devEnv) setUp(port int, up bool) {
	e.mu.Lock()
	e.up[port] = up
	e.mu.Unlock()
}

func (e *devEnv) packets() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.sent...)
}

// call performs a request as alice (token "" = anonymous) and checks the response against the spec.
func (e *devEnv) call(method, path string, body any, token string) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.do(method, path, body, token, true)
}

// callRaw skips the spec check, for requests that never reach an operation (a malformed path
// parameter is rejected by the generated router, which the spec does not describe).
func (e *devEnv) callRaw(method, path string, body any, token string) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.do(method, path, body, token, false)
}

func (e *devEnv) do(method, path string, body any, token string, check bool) *httptest.ResponseRecorder {
	e.t.Helper()
	var rdr io.Reader = http.NoBody
	switch b := body.(type) {
	case nil:
	case string:
		rdr = strings.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			e.t.Fatal(err)
		}
		rdr = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rdr)
	e.calls++
	req.RemoteAddr = "192.0.2." + strconv.Itoa(e.calls%250+1) + ":4000"
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if check {
		e.spec.check(e.t, req, rec)
	}
	return rec
}

func (e *devEnv) as(method, path string, body any) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.call(method, path, body, e.token)
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
	return v
}

func want(t *testing.T, rec *httptest.ResponseRecorder, status int) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status %d, want %d; body=%s", rec.Code, status, rec.Body)
	}
}

// mkDevice creates a device through the API and returns it.
func (e *devEnv) mkDevice(body map[string]any) gen.Device {
	e.t.Helper()
	if _, ok := body["ip_address"]; !ok {
		body["ip_address"] = "192.168.1.20"
	}
	if _, ok := body["monitor_port"]; !ok {
		body["monitor_port"] = 22
	}
	rec := e.as("POST", "/api/v1/devices", body)
	want(e.t, rec, 201)
	return decode[gen.Device](e.t, rec)
}

func TestDeviceCRUDHappyPath(t *testing.T) {
	e := newDevEnv(t)

	rec := e.as("POST", "/api/v1/devices", map[string]any{"name": "Living Room PC", "ip_address": "192.168.1.20", "monitor_port": 22})
	want(t, rec, 201)
	d := decode[gen.Device](t, rec)
	if d.Status != gen.DeviceStatusUnknown || d.PowerTransition != gen.PowerTransition("none") || !d.Enabled ||
		*d.DeviceType != "desktop" || *d.Os != "linux" || *d.WolPort != 9 ||
		d.MonitorIntervalSeconds != 30 || d.MonitorTimeoutMs != 2000 || d.Agent != nil ||
		d.LastSeenAt != nil || d.LastCheckedAt != nil || d.LastLatencyMs != nil {
		t.Fatalf("created device: %s", rec.Body)
	}
	if uuid.UUID(d.Id).Version() != 7 {
		t.Fatalf("id %s is not a UUIDv7", d.Id)
	}

	got := decode[gen.Device](t, e.as("GET", "/api/v1/devices/"+d.Id.String(), nil))
	if got.Id != d.Id || got.Name != "Living Room PC" {
		t.Fatalf("get: %+v", got)
	}

	rec = e.as("PATCH", "/api/v1/devices/"+d.Id.String(), map[string]any{
		"name": "Den PC", "description": "under the desk", "mac_address": "AA-BB-CC-DD-EE-FF",
		"device_type": "laptop", "os": "macos", "monitor_interval_seconds": 60, "enabled": false,
	})
	want(t, rec, 200)
	p := decode[gen.Device](t, rec)
	if p.Name != "Den PC" || *p.Description != "under the desk" || *p.MacAddress != "aa:bb:cc:dd:ee:ff" ||
		*p.DeviceType != "laptop" || *p.Os != "macos" || p.MonitorIntervalSeconds != 60 || p.Enabled ||
		p.MonitorPort != 22 || p.IpAddress != "192.168.1.20" {
		t.Fatalf("patched device: %s", rec.Body)
	}
	if !p.UpdatedAt.After(d.UpdatedAt) && !p.UpdatedAt.Equal(d.UpdatedAt) {
		t.Fatal("updated_at went backwards")
	}

	list := decode[gen.DeviceList](t, e.as("GET", "/api/v1/devices", nil))
	if len(list.Items) != 1 || list.NextCursor != nil || list.Items[0].Name != "Den PC" {
		t.Fatalf("list: %+v", list)
	}

	want(t, e.as("DELETE", "/api/v1/devices/"+d.Id.String(), nil), 204)
	rec = e.as("GET", "/api/v1/devices/"+d.Id.String(), nil)
	want(t, rec, 404)
	if errCode(t, rec) != "not_found" {
		t.Fatalf("body: %s", rec.Body)
	}
	want(t, e.as("DELETE", "/api/v1/devices/"+d.Id.String(), nil), 404)
	if list = decode[gen.DeviceList](t, e.as("GET", "/api/v1/devices", nil)); list.Items == nil || len(list.Items) != 0 {
		t.Fatalf("an empty list must be [] not null: %s", e.as("GET", "/api/v1/devices", nil).Body)
	}
}

func TestCreateValidation(t *testing.T) {
	e := newDevEnv(t)
	ok := func(m map[string]any) map[string]any {
		base := map[string]any{"name": "pc", "ip_address": "10.0.0.1", "monitor_port": 22}
		for k, v := range m {
			if v == nil {
				delete(base, k)
			} else {
				base[k] = v
			}
		}
		return base
	}
	tests := []struct {
		name string
		body any
		msg  string // substring of error.message
	}{
		{"unknown field", ok(map[string]any{"colour": "red"}), "unknown field"},
		{"agent unknown field", ok(map[string]any{"agent": map[string]any{"agent_id": uuid.NewString(), "port": 9750, "token": "x"}}), "unknown field"},
		{"name missing", ok(map[string]any{"name": nil}), "name: required"},
		{"name too long", ok(map[string]any{"name": strings.Repeat("x", 101)}), "name: at most 100"},
		{"address missing", ok(map[string]any{"ip_address": nil}), "ip_address: required"},
		{"address garbage", ok(map[string]any{"ip_address": "999.1.1.1 !"}), "ip_address"},
		{"port missing", ok(map[string]any{"monitor_port": nil}), "monitor_port: required"},
		{"port 0", ok(map[string]any{"monitor_port": 0}), "monitor_port"},
		{"port too big", ok(map[string]any{"monitor_port": 70000}), "monitor_port"},
		{"interval too small", ok(map[string]any{"monitor_interval_seconds": 1}), "monitor_interval_seconds"},
		{"timeout too big", ok(map[string]any{"monitor_timeout_ms": 20000}), "monitor_timeout_ms"},
		{"bad mac", ok(map[string]any{"mac_address": "not-a-mac"}), "mac_address"},
		{"bad broadcast", ok(map[string]any{"wol_broadcast_address": "::1"}), "wol_broadcast_address"},
		{"bad device_type", ok(map[string]any{"device_type": "fridge"}), "device_type"},
		{"bad os", ok(map[string]any{"os": "dos"}), "os:"},
		{"agent not a uuid", ok(map[string]any{"agent": map[string]any{"agent_id": "abc", "port": 9750}}), "UUID"},
		{"agent bad scheme", ok(map[string]any{"agent": map[string]any{"agent_id": uuid.NewString(), "port": 9750, "scheme": "gopher"}}), "agent.scheme"},
		{"wrong type", `{"name": 5, "ip_address": "10.0.0.1", "monitor_port": 22}`, "JSON"},
		{"not json", `nope`, "JSON"},
		{"empty body", ``, "JSON"},
		{"trailing data", `{"name":"a","ip_address":"10.0.0.1","monitor_port":22} {}`, "single JSON object"},
		{"array", `[]`, "JSON"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := e.as("POST", "/api/v1/devices", tt.body)
			want(t, rec, 400)
			if env := decode[gen.Error](t, rec); env.Error.Code != "bad_request" || !strings.Contains(env.Error.Message, tt.msg) {
				t.Fatalf("error %+v, want a message containing %q", env.Error, tt.msg)
			}
		})
	}
	if list := decode[gen.DeviceList](t, e.as("GET", "/api/v1/devices", nil)); len(list.Items) != 0 {
		t.Fatalf("rejected requests created %d devices", len(list.Items))
	}
}

func TestUpdateValidationAndUnknownFields(t *testing.T) {
	e := newDevEnv(t)
	d := e.mkDevice(map[string]any{"name": "pc"})
	path := "/api/v1/devices/" + d.Id.String()
	for name, body := range map[string]any{
		"unknown field":      map[string]any{"status": "online"}, // observed state is not writable
		"read-only id":       map[string]any{"id": uuid.NewString()},
		"empty name":         map[string]any{"name": ""},
		"empty address":      map[string]any{"ip_address": ""},
		"interval too small": map[string]any{"monitor_interval_seconds": 4},
		"bad mac":            map[string]any{"mac_address": "xx"},
		"not an object":      `"name"`,
	} {
		t.Run(name, func(t *testing.T) {
			rec := e.as("PATCH", path, body)
			want(t, rec, 400)
			if errCode(t, rec) != "bad_request" {
				t.Fatalf("body: %s", rec.Body)
			}
		})
	}
	// Nothing was applied by the rejected requests.
	if got := decode[gen.Device](t, e.as("GET", path, nil)); got.Name != "pc" {
		t.Fatalf("device changed: %+v", got)
	}
}

func TestDuplicateNameIsA409(t *testing.T) {
	e := newDevEnv(t)
	a := e.mkDevice(map[string]any{"name": "Desktop"})
	b := e.mkDevice(map[string]any{"name": "Laptop"})

	rec := e.as("POST", "/api/v1/devices", map[string]any{"name": "desktop", "ip_address": "10.0.0.9", "monitor_port": 22})
	want(t, rec, 409)
	if errCode(t, rec) != "conflict" {
		t.Fatalf("body: %s", rec.Body)
	}
	want(t, e.as("PATCH", "/api/v1/devices/"+b.Id.String(), map[string]any{"name": "DESKTOP"}), 409)
	// Keeping or re-casing its own name is fine.
	want(t, e.as("PATCH", "/api/v1/devices/"+a.Id.String(), map[string]any{"name": "DESKTOP"}), 200)
}

func TestPatchClearsOptionalTextWithEmptyString(t *testing.T) {
	e := newDevEnv(t)
	d := e.mkDevice(map[string]any{
		"name": "nas", "description": "basement", "hostname": "nas.lan", "mac_address": "00:11:22:33:44:55",
		"wol_broadcast_address": "192.168.1.255", "wol_port": 7,
	})
	path := "/api/v1/devices/" + d.Id.String()
	if d.Description == nil || d.Hostname == nil || d.MacAddress == nil || d.WolBroadcastAddress == nil || *d.WolPort != 7 {
		t.Fatalf("created: %+v", d)
	}
	// Omitted = unchanged.
	p := decode[gen.Device](t, e.as("PATCH", path, map[string]any{"enabled": false}))
	if p.Description == nil || p.Hostname == nil || p.MacAddress == nil || p.WolBroadcastAddress == nil {
		t.Fatalf("an unrelated PATCH cleared fields: %+v", p)
	}
	// "" = clear (the four documented fields).
	rec := e.as("PATCH", path, map[string]any{"description": "", "hostname": "", "mac_address": "", "wol_broadcast_address": ""})
	want(t, rec, 200)
	p = decode[gen.Device](t, rec)
	if p.Description != nil || p.Hostname != nil || p.MacAddress != nil || p.WolBroadcastAddress != nil {
		t.Fatalf("fields not cleared: %s", rec.Body)
	}
	// Cleared MAC: wake becomes a 409 again.
	want(t, e.as("POST", path+"/wake", nil), 409)
}

func TestAgentBindingThroughTheAPI(t *testing.T) {
	e := newDevEnv(t)
	agentA, agentB := uuid.NewString(), uuid.NewString()

	d := e.mkDevice(map[string]any{"name": "srv", "agent": map[string]any{"agent_id": agentA}})
	if d.Agent == nil || d.Agent.AgentId.String() != agentA || d.Agent.Port != 9750 || d.Agent.Scheme == nil || *d.Agent.Scheme != "http" ||
		d.Agent.Status == nil || *d.Agent.Status != gen.AgentBindingStatusUnknown || d.Agent.Version != nil || d.Agent.LastOkAt != nil {
		t.Fatalf("binding with defaults (status unknown until the first contact; version and last_ok_at absent): %+v", d.Agent)
	}
	path := "/api/v1/devices/" + d.Id.String()

	// Persisted: a fresh GET and the list both show it.
	if g := decode[gen.Device](t, e.as("GET", path, nil)); g.Agent == nil || g.Agent.AgentId.String() != agentA {
		t.Fatalf("binding not persisted: %+v", g.Agent)
	}
	if l := decode[gen.DeviceList](t, e.as("GET", "/api/v1/devices", nil)); l.Items[0].Agent == nil {
		t.Fatal("list lost the binding")
	}

	// PATCH replaces it; an unrelated PATCH keeps it.
	p := decode[gen.Device](t, e.as("PATCH", path, map[string]any{"agent": map[string]any{"agent_id": agentB, "port": 9751, "scheme": "https"}}))
	if p.Agent.AgentId.String() != agentB || p.Agent.Port != 9751 || *p.Agent.Scheme != "https" {
		t.Fatalf("patched binding: %+v", p.Agent)
	}
	if p = decode[gen.Device](t, e.as("PATCH", path, map[string]any{"description": "x"})); p.Agent == nil {
		t.Fatal("unrelated PATCH removed the binding")
	}

	// An agent serves one device.
	rec := e.as("POST", "/api/v1/devices", map[string]any{"name": "dup", "ip_address": "10.0.0.2", "monitor_port": 22, "agent": map[string]any{"agent_id": agentB}})
	want(t, rec, 409)

	// DELETE /agent is idempotent: 204 with a binding, 204 without, 404 only for an unknown device.
	for i := 0; i < 2; i++ {
		want(t, e.as("DELETE", path+"/agent", nil), 204)
	}
	if g := decode[gen.Device](t, e.as("GET", path, nil)); g.Agent != nil {
		t.Fatalf("binding survived DELETE: %+v", g.Agent)
	}
	plain := e.mkDevice(map[string]any{"name": "plain"})
	want(t, e.as("DELETE", "/api/v1/devices/"+plain.Id.String()+"/agent", nil), 204)
	rec = e.as("DELETE", "/api/v1/devices/"+uuid.NewString()+"/agent", nil)
	want(t, rec, 404)
	// The released agent id can be bound again.
	e.mkDevice(map[string]any{"name": "again", "agent": map[string]any{"agent_id": agentB}})
}

func TestListPaginationOverHTTP(t *testing.T) {
	e := newDevEnv(t)
	names := []string{"alpha", "Bravo", "charlie", "Delta", "echo"}
	for _, i := range []int{3, 0, 4, 1, 2} {
		e.mkDevice(map[string]any{"name": names[i]})
	}
	var got []string
	path := "/api/v1/devices?limit=2"
	pages := 0
	for {
		l := decode[gen.DeviceList](t, e.as("GET", path, nil))
		pages++
		for _, d := range l.Items {
			got = append(got, d.Name)
		}
		if l.NextCursor == nil {
			break
		}
		if len(l.Items) != 2 {
			t.Fatalf("short page with a cursor: %d", len(l.Items))
		}
		path = "/api/v1/devices?limit=2&cursor=" + *l.NextCursor
	}
	if pages != 3 || strings.Join(got, ",") != strings.Join(names, ",") {
		t.Fatalf("pages=%d order=%v, want case-insensitive name order %v", pages, got, names)
	}
	for name, path := range map[string]string{
		"limit 0":        "/api/v1/devices?limit=0",
		"limit 201":      "/api/v1/devices?limit=201",
		"limit negative": "/api/v1/devices?limit=-5",
		"cursor garbage": "/api/v1/devices?cursor=%21%21",
		// A well-formed cursor of a different list (status history) is not accepted either.
		"cursor of another list": "/api/v1/devices?cursor=" + base64.RawURLEncoding.EncodeToString([]byte(`{"t":1,"i":2}`)),
	} {
		t.Run(name, func(t *testing.T) {
			rec := e.as("GET", path, nil)
			want(t, rec, 400)
			if errCode(t, rec) != "bad_request" {
				t.Fatalf("body: %s", rec.Body)
			}
		})
	}
	// limit=abc never reaches the handler: the generated router rejects it (not described by the spec).
	want(t, e.callRaw("GET", "/api/v1/devices?limit=abc", nil, e.token), 400)
	// limit=200 is the maximum.
	want(t, e.as("GET", "/api/v1/devices?limit=200", nil), 200)
}

func TestProbeAndHistory(t *testing.T) {
	e := newDevEnv(t)
	d := e.mkDevice(map[string]any{"name": "pc", "monitor_port": 2222})
	path := "/api/v1/devices/" + d.Id.String()

	// Unreachable: hysteresis keeps the status "unknown" after one failure.
	r := decode[gen.ProbeResult](t, e.as("POST", path+"/probe", nil))
	if r.Reachable || r.Status != gen.DeviceStatusUnknown || r.Error == nil || r.LatencyMs != nil || r.DeviceId != d.Id {
		t.Fatalf("first probe: %+v", r)
	}
	// The second consecutive failure makes it offline (history row 1).
	r = decode[gen.ProbeResult](t, e.as("POST", path+"/probe", nil))
	if r.Status != gen.DeviceStatusOffline {
		t.Fatalf("second probe: %+v", r)
	}
	// Reachable: online at once (history row 2); latency and last_seen are stored.
	e.setUp(2222, true)
	r = decode[gen.ProbeResult](t, e.as("POST", path+"/probe", nil))
	if !r.Reachable || r.Status != gen.DeviceStatusOnline || r.LatencyMs == nil || *r.LatencyMs != 3 || r.Error != nil {
		t.Fatalf("third probe: %+v", r)
	}
	got := decode[gen.Device](t, e.as("GET", path, nil))
	if got.Status != gen.DeviceStatusOnline || got.LastSeenAt == nil || got.LastCheckedAt == nil || got.LastLatencyMs == nil || *got.LastLatencyMs != 3 {
		t.Fatalf("stored state after probes: %+v", got)
	}

	h := decode[gen.StatusHistory](t, e.as("GET", path+"/history", nil))
	if len(h.Items) != 2 || h.Items[0].Status != gen.DeviceStatusOnline || *h.Items[0].PreviousStatus != gen.DeviceStatusOffline ||
		*h.Items[0].Reason != "manual probe" || h.Items[1].Status != gen.DeviceStatusOffline || *h.Items[1].PreviousStatus != gen.DeviceStatusUnknown {
		t.Fatalf("history (newest first): %+v", h.Items)
	}
	// Cursor pagination, one row at a time.
	first := decode[gen.StatusHistory](t, e.as("GET", path+"/history?limit=1", nil))
	if len(first.Items) != 1 || first.NextCursor == nil {
		t.Fatalf("first page: %+v", first)
	}
	second := decode[gen.StatusHistory](t, e.as("GET", path+"/history?limit=1&cursor="+*first.NextCursor, nil))
	if len(second.Items) != 1 || second.NextCursor != nil || second.Items[0].Status != gen.DeviceStatusOffline {
		t.Fatalf("second page: %+v", second)
	}
	want(t, e.as("GET", path+"/history?cursor=bogus", nil), 400)
	want(t, e.as("GET", path+"/history?limit=0", nil), 400)

	// A disabled device can still be probed by hand.
	e.as("PATCH", path, map[string]any{"enabled": false})
	want(t, e.as("POST", path+"/probe", nil), 200)
}

func TestUnknownDeviceIs404EverywhereAndBadIDIs400(t *testing.T) {
	e := newDevEnv(t)
	id := uuid.NewString()
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v1/devices/" + id},
		{"PATCH", "/api/v1/devices/" + id},
		{"DELETE", "/api/v1/devices/" + id},
		{"DELETE", "/api/v1/devices/" + id + "/agent"},
		{"POST", "/api/v1/devices/" + id + "/probe"},
		{"GET", "/api/v1/devices/" + id + "/history"},
		{"POST", "/api/v1/devices/" + id + "/wake"},
		{"GET", "/api/v1/commands/" + id},
	} {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			var body any
			if c.method == "PATCH" {
				body = map[string]any{"name": "x"}
			}
			rec := e.as(c.method, c.path, body)
			want(t, rec, 404)
			if errCode(t, rec) != "not_found" {
				t.Fatalf("body: %s", rec.Body)
			}
		})
	}
	rec := e.callRaw("GET", "/api/v1/devices/not-a-uuid", nil, e.token)
	want(t, rec, 400)
	if errCode(t, rec) != "bad_request" {
		t.Fatalf("body: %s", rec.Body)
	}
}

func TestEveryImplementedOperationRequiresAuthentication(t *testing.T) {
	e := newDevEnv(t)
	id := uuid.NewString()
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v1/devices"},
		{"POST", "/api/v1/devices"},
		{"GET", "/api/v1/devices/" + id},
		{"PATCH", "/api/v1/devices/" + id},
		{"DELETE", "/api/v1/devices/" + id},
		{"DELETE", "/api/v1/devices/" + id + "/agent"},
		{"POST", "/api/v1/devices/" + id + "/probe"},
		{"GET", "/api/v1/devices/" + id + "/history"},
		{"POST", "/api/v1/devices/" + id + "/wake"},
		{"POST", "/api/v1/devices/" + id + "/shutdown"},
		{"GET", "/api/v1/commands"},
		{"GET", "/api/v1/commands/" + id},
		{"GET", "/api/v1/system/controller-key"},
		{"GET", "/api/v1/activity"},
	} {
		for _, token := range []string{"", "garbage"} {
			rec := e.call(c.method, c.path, map[string]any{}, token)
			if rec.Code != 401 || errCode(t, rec) != "unauthorized" || !strings.HasPrefix(rec.Header().Get("WWW-Authenticate"), "Bearer") {
				t.Errorf("%s %s token=%q → %d %s", c.method, c.path, token, rec.Code, rec.Body)
			}
		}
	}
	// The anonymous attempts changed nothing.
	if l := decode[gen.DeviceList](t, e.as("GET", "/api/v1/devices", nil)); len(l.Items) != 0 {
		t.Fatal("an unauthenticated request created a device")
	}
}

func TestDeviceChangesAreAudited(t *testing.T) {
	e := newDevEnv(t)
	d := e.mkDevice(map[string]any{"name": "pc", "mac_address": "aa:bb:cc:dd:ee:ff"})
	path := "/api/v1/devices/" + d.Id.String()
	e.as("PATCH", path, map[string]any{"description": "x", "enabled": false})
	e.as("PATCH", path, map[string]any{}) // no change: no entry
	e.as("DELETE", path+"/agent", nil)
	want(t, e.as("POST", path+"/wake", nil), 202)
	e.as("DELETE", path, nil)

	rows, err := e.db.Query(`SELECT action, actor, target_id, result, details FROM audit_log WHERE action LIKE 'device.%' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var actions []string
	for rows.Next() {
		var action, actor, target, result, details string
		if err := rows.Scan(&action, &actor, &target, &result, &details); err != nil {
			t.Fatal(err)
		}
		if actor != "alice" || target != d.Id.String() || result != "ok" {
			t.Fatalf("%s: actor=%q target=%q result=%q", action, actor, target, result)
		}
		if action == "device.update" && details != `{"fields":["description","enabled"],"name":"pc"}` {
			t.Fatalf("update details: %s", details)
		}
		actions = append(actions, action)
	}
	if strings.Join(actions, ",") != "device.create,device.update,device.agent_remove,device.wake,device.delete" {
		t.Fatalf("audit actions: %v", actions)
	}
}
