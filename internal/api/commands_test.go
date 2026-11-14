package api

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/thanhtunguet/homecore/internal/api/gen"
	"github.com/thanhtunguet/homecore/internal/config"
)

// ok asserts the status and returns the recorder, so a call can be decoded in one expression.
func ok(t *testing.T, rec *httptest.ResponseRecorder, status int) *httptest.ResponseRecorder {
	t.Helper()
	want(t, rec, status)
	return rec
}

// REQ-API-019 and ADR-012, end to end: wake is asynchronous, idempotent, fail-fast and throttled.
func TestWakeLifecycleOverHTTP(t *testing.T) {
	e := newDevEnv(t)
	d := e.mkDevice(map[string]any{
		"name": "gaming pc", "monitor_port": 3333, "mac_address": "AA:BB:CC:DD:EE:FF",
		"wol_broadcast_address": "192.168.1.255", "wol_port": 9,
	})
	path := "/api/v1/devices/" + d.Id.String()

	// 202 + a command that is already `sent`; one magic packet to the configured broadcast address.
	c := decode[gen.Command](t, ok(t, e.as("POST", path+"/wake", nil), 202))
	if c.Status != gen.CommandStatusSent || c.Type != gen.CommandType("wake") || c.DeviceId != d.Id ||
		c.DeadlineAt == nil || c.CompletedAt != nil || c.Error != nil || c.ErrorCode != nil ||
		c.RequestedBy == nil || c.RequestedBy.String() != e.userID {
		t.Fatalf("wake response: %+v", c)
	}
	if got := e.packets(); len(got) != 1 || got[0] != "aa:bb:cc:dd:ee:ff 192.168.1.255:9" {
		t.Fatalf("packets: %v", got)
	}
	if left := time.Until(*c.DeadlineAt); left < 170*time.Second || left > 181*time.Second {
		t.Fatalf("deadline_at is %v away, want about the 180 s default", left)
	}

	// power_transition is derived from the active command, in get and list.
	if g := decode[gen.Device](t, e.as("GET", path, nil)); g.PowerTransition != "waking" {
		t.Fatalf("get: power_transition %q", g.PowerTransition)
	}
	if l := decode[gen.DeviceList](t, e.as("GET", "/api/v1/devices", nil)); l.Items[0].PowerTransition != "waking" {
		t.Fatalf("list: power_transition %q", l.Items[0].PowerTransition)
	}

	// A second request inside the same second: 429 with Retry-After (REQ-API-018), nothing sent.
	rec := e.as("POST", path+"/wake", nil)
	want(t, rec, 429)
	if errCode(t, rec) != "rate_limited" || rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("throttle: %s retry-after=%q", rec.Body, rec.Header().Get("Retry-After"))
	}
	if len(e.packets()) != 1 {
		t.Fatal("a throttled request sent a packet")
	}

	// After the window, repeating the wake is not an error: the same command comes back, no new packet.
	time.Sleep(1100 * time.Millisecond)
	again := decode[gen.Command](t, ok(t, e.as("POST", path+"/wake", nil), 202))
	if again.Id != c.Id || again.Status != gen.CommandStatusSent || len(e.packets()) != 1 {
		t.Fatalf("idempotent wake: %+v packets=%d", again, len(e.packets()))
	}
	if got := decode[gen.Command](t, ok(t, e.as("GET", "/api/v1/commands/"+c.Id.String(), nil), 200)); got.Status != gen.CommandStatusSent {
		t.Fatalf("poll: %+v", got)
	}

	// The machine boots: the next monitor pass sees it online and completes the command.
	e.setUp(3333, true)
	e.mon.CheckDue(context.Background())
	done := decode[gen.Command](t, ok(t, e.as("GET", "/api/v1/commands/"+c.Id.String(), nil), 200))
	if done.Status != gen.CommandStatusCompleted || done.CompletedAt == nil {
		t.Fatalf("after the monitor saw the device online: %+v", done)
	}
	g := decode[gen.Device](t, ok(t, e.as("GET", path, nil), 200))
	if g.Status != gen.DeviceStatusOnline || g.PowerTransition != "none" || g.LastSeenAt == nil || g.LastLatencyMs == nil {
		t.Fatalf("device after completion: %+v", g)
	}
	h := decode[gen.StatusHistory](t, ok(t, e.as("GET", path+"/history", nil), 200))
	if len(h.Items) != 1 || h.Items[0].Status != gen.DeviceStatusOnline || h.Items[0].Reason == nil || *h.Items[0].Reason != "probe" {
		t.Fatalf("history: %+v", h.Items)
	}

	// Waking a device that is already online completes at once, without a packet.
	time.Sleep(1100 * time.Millisecond)
	noop := decode[gen.Command](t, ok(t, e.as("POST", path+"/wake", nil), 202))
	if noop.Id == c.Id || noop.Status != gen.CommandStatusCompleted || noop.CompletedAt == nil || len(e.packets()) != 1 {
		t.Fatalf("wake of an online device: %+v packets=%d", noop, len(e.packets()))
	}
}

func TestWakeWithoutMACIs409(t *testing.T) {
	e := newDevEnv(t)
	d := e.mkDevice(map[string]any{"name": "no mac"})
	rec := e.as("POST", "/api/v1/devices/"+d.Id.String()+"/wake", nil)
	want(t, rec, 409)
	if errCode(t, rec) != "conflict" {
		t.Fatalf("code: %s", rec.Body)
	}
	if l := decode[gen.CommandList](t, e.as("GET", "/api/v1/commands", nil)); len(l.Items) != 0 {
		t.Fatalf("a rejected wake left a command behind: %+v", l.Items)
	}
}

func TestWakeSendFailureYieldsAFailedCommand(t *testing.T) {
	e := newDevEnv(t)
	d := e.mkDevice(map[string]any{"name": "pc", "mac_address": "aa:bb:cc:dd:ee:ff"})
	e.mu.Lock()
	e.sendErr = errors.New("network is unreachable")
	e.mu.Unlock()

	// Fail fast: the request still succeeds with 202, the command says what went wrong.
	c := decode[gen.Command](t, ok(t, e.as("POST", "/api/v1/devices/"+d.Id.String()+"/wake", nil), 202))
	if c.Status != gen.CommandStatusFailed || c.ErrorCode == nil || *c.ErrorCode != "wol_send_failed" ||
		c.Error == nil || *c.Error == "" || c.CompletedAt == nil {
		t.Fatalf("failed wake: %+v", c)
	}
	if g := decode[gen.Device](t, e.as("GET", "/api/v1/devices/"+d.Id.String(), nil)); g.PowerTransition != "none" {
		t.Fatalf("power_transition %q after a failed wake", g.PowerTransition)
	}
}

func TestWakeTimesOutAtItsDeadline(t *testing.T) {
	e := newDevEnvCfg(t, func(c *config.Controller) { c.Commands.WakeDeadline = 80 * time.Millisecond })
	d := e.mkDevice(map[string]any{"name": "pc", "mac_address": "aa:bb:cc:dd:ee:ff", "monitor_port": 4444})
	c := decode[gen.Command](t, ok(t, e.as("POST", "/api/v1/devices/"+d.Id.String()+"/wake", nil), 202))
	if c.Status != gen.CommandStatusSent {
		t.Fatalf("wake: %+v", c)
	}
	time.Sleep(150 * time.Millisecond)
	if err := e.engine.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := decode[gen.Command](t, ok(t, e.as("GET", "/api/v1/commands/"+c.Id.String(), nil), 200))
	if got.Status != gen.CommandStatusTimedOut || got.CompletedAt == nil {
		t.Fatalf("after the deadline: %+v", got)
	}
	if g := decode[gen.Device](t, e.as("GET", "/api/v1/devices/"+d.Id.String(), nil)); g.PowerTransition != "none" {
		t.Fatalf("power_transition %q after a timeout", g.PowerTransition)
	}
}

func TestCommandListFiltersPaginationAndErrors(t *testing.T) {
	e := newDevEnv(t)
	a := e.mkDevice(map[string]any{"name": "a", "mac_address": "aa:bb:cc:dd:ee:01", "monitor_port": 5001})
	b := e.mkDevice(map[string]any{"name": "b", "mac_address": "aa:bb:cc:dd:ee:02", "monitor_port": 5002})
	e.setUp(5002, true) // b is online, so its wake completes at once

	wake := func(id string) gen.Command {
		return decode[gen.Command](t, ok(t, e.as("POST", "/api/v1/devices/"+id+"/wake", nil), 202))
	}
	c1 := wake(a.Id.String())
	time.Sleep(5 * time.Millisecond)
	e.mon.CheckDue(context.Background()) // b becomes online
	c2 := wake(b.Id.String())
	if c2.Status != gen.CommandStatusCompleted {
		t.Fatalf("online wake: %+v", c2)
	}

	// Newest first.
	all := decode[gen.CommandList](t, ok(t, e.as("GET", "/api/v1/commands", nil), 200))
	if len(all.Items) != 2 || all.Items[0].Id != c2.Id || all.Items[1].Id != c1.Id || all.NextCursor != nil {
		t.Fatalf("list: %+v", all)
	}
	// Filters.
	byDev := decode[gen.CommandList](t, e.as("GET", "/api/v1/commands?device_id="+a.Id.String(), nil))
	if len(byDev.Items) != 1 || byDev.Items[0].Id != c1.Id {
		t.Fatalf("device_id filter: %+v", byDev.Items)
	}
	byStatus := decode[gen.CommandList](t, e.as("GET", "/api/v1/commands?status=completed", nil))
	if len(byStatus.Items) != 1 || byStatus.Items[0].Id != c2.Id {
		t.Fatalf("status filter: %+v", byStatus.Items)
	}
	both := decode[gen.CommandList](t, e.as("GET", "/api/v1/commands?status=sent&device_id="+b.Id.String(), nil))
	if len(both.Items) != 0 || both.NextCursor != nil {
		t.Fatalf("combined filter: %+v", both)
	}
	// Cursor pagination, one at a time.
	p1 := decode[gen.CommandList](t, ok(t, e.as("GET", "/api/v1/commands?limit=1", nil), 200))
	if len(p1.Items) != 1 || p1.Items[0].Id != c2.Id || p1.NextCursor == nil {
		t.Fatalf("page 1: %+v", p1)
	}
	p2 := decode[gen.CommandList](t, ok(t, e.as("GET", "/api/v1/commands?limit=1&cursor="+*p1.NextCursor, nil), 200))
	if len(p2.Items) != 1 || p2.Items[0].Id != c1.Id || p2.NextCursor != nil {
		t.Fatalf("page 2: %+v", p2)
	}
	// While c1 is active, the device shows waking in the list.
	l := decode[gen.DeviceList](t, e.as("GET", "/api/v1/devices", nil))
	if l.Items[0].PowerTransition != "waking" || l.Items[1].PowerTransition != "none" {
		t.Fatalf("power_transition in list: %q %q", l.Items[0].PowerTransition, l.Items[1].PowerTransition)
	}

	// Errors.
	for _, q := range []string{"?status=bogus", "?limit=0", "?limit=201", "?cursor=bogus", "?device_id=zzz"} {
		rec := e.callRaw("GET", "/api/v1/commands"+q, nil, e.token)
		if rec.Code != 400 {
			t.Errorf("GET /commands%s: status %d, want 400 (%s)", q, rec.Code, rec.Body)
		}
	}
	want(t, e.as("GET", "/api/v1/commands?status=bogus", nil), 400)
	want(t, e.as("GET", "/api/v1/commands?cursor=bogus", nil), 400)
	rec := e.as("GET", "/api/v1/commands/0190a0a0-0000-7000-8000-000000000000", nil)
	want(t, rec, 404)
	if errCode(t, rec) != "not_found" {
		t.Fatalf("code: %s", rec.Body)
	}
	if rec := e.callRaw("GET", "/api/v1/commands/not-a-uuid", nil, e.token); rec.Code != 400 {
		t.Fatalf("bad id: %d", rec.Code)
	}
}
