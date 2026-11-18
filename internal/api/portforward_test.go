package api

import (
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thanhtunguet/homecore/internal/api/gen"
)

const pfBase = "/api/v1/port-forwards"

// pfPort returns a TCP+UDP port that is free on loopback right now.
func pfPort(t *testing.T) int {
	t.Helper()
	for range 50 {
		ln, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		pc, uerr := net.ListenPacket("udp4", "127.0.0.1:"+strconv.Itoa(port))
		_ = ln.Close()
		if uerr == nil {
			_ = pc.Close()
			return port
		}
	}
	t.Fatal("no free port")
	return 0
}

// pfEcho serves a TCP echo server on loopback and returns its port.
func pfEcho(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); _, _ = io.Copy(c, c) }()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func pfBody(listen, target int, over map[string]any) map[string]any {
	b := map[string]any{"name": "ssh", "protocol": "tcp", "listen_address": "127.0.0.1", "listen_port": listen,
		"target_host": "127.0.0.1", "target_port": target}
	for k, v := range over {
		if v == nil {
			delete(b, k)
		} else {
			b[k] = v
		}
	}
	return b
}

func pfAccepts(port int) bool {
	c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), time.Second)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func pfWaitStatus(e *devEnv, id string, want gen.PortForwardStatus) gen.PortForward {
	e.t.Helper()
	var got gen.PortForward
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		got = decode[gen.PortForward](e.t, ok(e.t, e.as("GET", pfBase+"/"+id, nil), 200))
		if got.Status == want {
			return got
		}
	}
	e.t.Fatalf("status of %s = %s, want %s (%v)", id, got.Status, want, got.LastError)
	return got
}

// REQ-PF-001, REQ-PF-002, REQ-PF-003, REQ-API-033: create, read, switch and delete over HTTP; the
// listener really follows the switch.
func TestPortForwardCRUDAndSwitchOverHTTP(t *testing.T) {
	e := newDevEnv(t)
	listen, target := pfPort(t), pfEcho(t)

	rec := ok(t, e.as("POST", pfBase, pfBody(listen, target, nil)), 201)
	r := decode[gen.PortForward](t, rec)
	if r.Name != "ssh" || r.Protocol != gen.Tcp || r.ListenAddress != "127.0.0.1" || r.ListenPort != listen ||
		r.TargetHost != "127.0.0.1" || r.TargetPort != target || !r.Enabled || r.Status != gen.PortForwardStatusRunning || r.LastError != nil {
		t.Fatalf("created: %s", rec.Body)
	}
	if !pfAccepts(listen) {
		t.Fatal("an enabled rule does not listen")
	}
	path := pfBase + "/" + r.Id.String()

	list := decode[struct{ Items []gen.PortForward }](t, ok(t, e.as("GET", pfBase, nil), 200))
	if len(list.Items) != 1 || list.Items[0].Id != r.Id || list.Items[0].Status != gen.PortForwardStatusRunning {
		t.Fatalf("list: %+v", list)
	}

	off := decode[gen.PortForward](t, ok(t, e.as("PATCH", path, map[string]any{"enabled": false}), 200))
	if off.Enabled || off.Status != gen.PortForwardStatusStopped || pfAccepts(listen) {
		t.Fatalf("switched off: %+v, accepting=%v", off, pfAccepts(listen))
	}
	on := decode[gen.PortForward](t, ok(t, e.as("PATCH", path, map[string]any{"enabled": true}), 200))
	if !on.Enabled || on.Status != gen.PortForwardStatusRunning || !pfAccepts(listen) {
		t.Fatalf("switched on: %+v", on)
	}

	// A disabled rule can be created, and it never listens.
	idle := decode[gen.PortForward](t, ok(t, e.as("POST", pfBase, pfBody(pfPort(t), target, map[string]any{"name": "idle", "enabled": false})), 201))
	if idle.Enabled || idle.Status != gen.PortForwardStatusStopped {
		t.Fatalf("disabled create: %+v", idle)
	}

	// Defaults: address 0.0.0.0, enabled, universal accepted.
	def := decode[gen.PortForward](t, ok(t, e.as("POST", pfBase, pfBody(pfPort(t), target, map[string]any{
		"name": "everywhere", "protocol": "universal", "listen_address": nil, "enabled": nil})), 201))
	if def.ListenAddress != "0.0.0.0" || !def.Enabled || def.Protocol != gen.Universal || def.Status != gen.PortForwardStatusRunning {
		t.Fatalf("defaults: %+v", def)
	}

	ok(t, e.as("DELETE", path, nil), 204)
	if pfAccepts(listen) {
		t.Fatal("a deleted rule still listens")
	}
	if rec := e.as("GET", path, nil); rec.Code != 404 {
		t.Fatalf("after delete: %d", rec.Code)
	}
}

// REQ-PF-004: a rule that cannot bind is created and shown in error, then comes up by itself.
func TestPortForwardThatCannotBindShowsItsErrorAndRecovers(t *testing.T) {
	e := newDevEnv(t)
	listen := pfPort(t)
	blocker, err := net.Listen("tcp4", "127.0.0.1:"+strconv.Itoa(listen))
	if err != nil {
		t.Fatal(err)
	}
	r := decode[gen.PortForward](t, ok(t, e.as("POST", pfBase, pfBody(listen, pfEcho(t), nil)), 201))
	if r.Status != gen.PortForwardStatusError || r.LastError == nil || !r.Enabled {
		t.Fatalf("view: %+v", r)
	}
	_ = blocker.Close()
	got := pfWaitStatus(e, r.Id.String(), gen.PortForwardStatusRunning)
	if got.LastError != nil {
		t.Fatalf("the error stays after recovery: %v", *got.LastError)
	}
}

func TestPortForwardErrors(t *testing.T) {
	e := newDevEnv(t)
	listen, target := pfPort(t), pfEcho(t)
	r := decode[gen.PortForward](t, ok(t, e.as("POST", pfBase, pfBody(listen, target, nil)), 201))

	if rec := e.as("POST", pfBase, pfBody(pfPort(t), target, map[string]any{"name": "SSH"})); rec.Code != 409 {
		t.Errorf("duplicate name: %d %s", rec.Code, rec.Body)
	}
	if rec := e.as("POST", pfBase, pfBody(listen, target, map[string]any{"name": "other"})); rec.Code != 409 || !strings.Contains(rec.Body.String(), "ssh") {
		t.Errorf("overlapping enabled rule: %d %s", rec.Code, rec.Body)
	}
	for name, over := range map[string]map[string]any{
		"port 0":          {"listen_port": 0},
		"port too big":    {"target_port": 65536},
		"bad protocol":    {"protocol": "icmp"},
		"bad address":     {"listen_address": "not-an-ip"},
		"bad host":        {"target_host": "bad host"},
		"empty name":      {"name": ""},
		"unknown field":   {"shell": "rm -rf /"},
		"missing target":  {"target_host": nil},
		"loop to itself":  {"listen_address": "0.0.0.0", "target_host": "127.0.0.1", "listen_port": 7777, "target_port": 7777},
		"zone in address": {"listen_address": "fe80::1%eth0"},
	} {
		if rec := e.as("POST", pfBase, pfBody(pfPort(t), target, over)); rec.Code != 400 {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	path := pfBase + "/" + r.Id.String()
	if rec := e.as("PATCH", path, map[string]any{"listen_port": 0}); rec.Code != 400 {
		t.Errorf("bad port on update: %d", rec.Code)
	}
	if got := decode[gen.PortForward](t, ok(t, e.as("GET", path, nil), 200)); got.ListenPort != listen || got.Status != gen.PortForwardStatusRunning {
		t.Errorf("a refused update changed the rule: %+v", got)
	}
	missing := pfBase + "/0193a0b2-7c4e-7f00-8000-000000000001"
	for _, m := range []string{"GET", "PATCH", "DELETE"} {
		if rec := e.as(m, missing, map[string]any{"enabled": false}); rec.Code != 404 {
			t.Errorf("%s unknown rule: %d", m, rec.Code)
		}
	}
}

// REQ-API-033: every operation needs a session token; a service account key is refused.
func TestPortForwardEndpointsRequireASession(t *testing.T) {
	e := newDevEnv(t)
	id := "0193a0b2-7c4e-7f00-8000-000000000001"
	ops := [][2]string{{"GET", pfBase}, {"POST", pfBase}, {"GET", pfBase + "/" + id}, {"PATCH", pfBase + "/" + id}, {"DELETE", pfBase + "/" + id}}
	for _, rq := range ops {
		if rec := e.call(rq[0], rq[1], map[string]any{}, ""); rec.Code != 401 {
			t.Errorf("%s %s without a token: %d", rq[0], rq[1], rec.Code)
		}
		if rec := e.call(rq[0], rq[1], map[string]any{}, "garbage"); rec.Code != 401 {
			t.Errorf("%s %s with a bad token: %d", rq[0], rq[1], rec.Code)
		}
	}
	key := e.mkKey("mcp", 0).Key
	for _, rq := range ops[:2] {
		if rec := e.callRaw(rq[0], rq[1], pfBody(1, 2, nil), key); rec.Code != 403 {
			t.Errorf("%s %s with a scoped key: %d", rq[0], rq[1], rec.Code)
		}
	}
}

// REQ-PF-008: changes are audited with the acting user; a switch is its own action.
func TestPortForwardAudit(t *testing.T) {
	e := newDevEnv(t)
	listen, target := pfPort(t), pfEcho(t)
	r := decode[gen.PortForward](t, ok(t, e.as("POST", pfBase, pfBody(listen, target, nil)), 201))
	path := pfBase + "/" + r.Id.String()
	ok(t, e.as("PATCH", path, map[string]any{"enabled": false}), 200)
	ok(t, e.as("PATCH", path, map[string]any{"enabled": true}), 200)
	ok(t, e.as("PATCH", path, map[string]any{"name": "ssh2", "target_port": target}), 200)
	ok(t, e.as("DELETE", path, nil), 204)

	rows, err := e.db.Query(`SELECT action, actor, target_id, result, details FROM audit_log WHERE action LIKE 'port_forward.%' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var action, actor, tid, result, details string
		if err := rows.Scan(&action, &actor, &tid, &result, &details); err != nil {
			t.Fatal(err)
		}
		if actor != "alice" || tid != r.Id.String() || result != "ok" {
			t.Errorf("%s: actor=%q target=%q result=%q", action, actor, tid, result)
		}
		if !strings.Contains(details, strconv.Itoa(listen)) {
			t.Errorf("%s: details do not say where the rule listens: %s", action, details)
		}
		got = append(got, action)
	}
	if want := "port_forward.create port_forward.disable port_forward.enable port_forward.update port_forward.delete"; strings.Join(got, " ") != want {
		t.Fatalf("audit actions: %v", got)
	}
	if n := e.countLike("audit_log", "details", `"fields":["name","target_port"]`); n != 1 {
		t.Fatalf("the update names the fields it changed (%d rows)", n)
	}
	// The activity feed words them.
	feed := e.as("GET", "/api/v1/activity?limit=50", nil)
	for _, w := range []string{"Port forwarding rule ssh created", "switched off", "switched on", "Port forwarding rule ssh2 updated", "deleted"} {
		if !strings.Contains(feed.Body.String(), w) {
			t.Errorf("activity feed lacks %q: %s", w, feed.Body)
		}
	}
}
