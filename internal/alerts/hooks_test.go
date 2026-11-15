package alerts_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/thanhtunguet/homecore/internal/alerts"
	"github.com/thanhtunguet/homecore/internal/auth"
	"github.com/thanhtunguet/homecore/internal/commands"
	"github.com/thanhtunguet/homecore/internal/ddns"
	"github.com/thanhtunguet/homecore/internal/devices"
	"github.com/thanhtunguet/homecore/internal/monitoring"
)

// fakeActive stands in for the commands repository: which (device, type) pairs have a command in flight.
type fakeActive struct {
	active map[string]bool // "deviceID/type"
	err    error
}

func (f fakeActive) Active(_ context.Context, id string, t commands.Type) (commands.Command, error) {
	switch {
	case f.err != nil:
		return commands.Command{}, f.err
	case f.active[id+"/"+string(t)]:
		return commands.Command{DeviceID: id, Type: t, Status: commands.StatusSent}, nil
	}
	return commands.Command{}, commands.ErrNotFound
}

func device(enabled bool) devices.Device {
	return devices.Device{ID: "dev1", Name: "Gaming PC", IPAddress: "192.168.1.20", MonitorPort: 22, Enabled: enabled}
}

func transition(from, to devices.Status, d devices.Device) monitoring.Transition {
	return monitoring.Transition{Device: d, From: from, To: to, At: time.Now(), Reason: "probe"}
}

// REQ-ALERT-004: a device that changes status with no command to explain it is reported; one that
// HomeCore itself woke or shut down is not.
func TestStatusHook(t *testing.T) {
	on, off, unk := devices.StatusOnline, devices.StatusOffline, devices.StatusUnknown
	cases := []struct {
		name    string
		tr      monitoring.Transition
		active  map[string]bool
		lookup  error
		wantMsg string // "" = no alert; else a substring of the title
	}{
		{"online to offline, nobody asked", transition(on, off, device(true)), nil, nil, "Gaming PC went offline"},
		{"offline to online, nobody asked", transition(off, on, device(true)), nil, nil, "Gaming PC is online"},
		{"offline after a shutdown command", transition(on, off, device(true)), map[string]bool{"dev1/shutdown": true}, nil, ""},
		{"online after a wake command", transition(off, on, device(true)), map[string]bool{"dev1/wake": true}, nil, ""},
		{"a wake does not explain going offline", transition(on, off, device(true)), map[string]bool{"dev1/wake": true}, nil, "went offline"},
		{"a shutdown does not explain coming online", transition(off, on, device(true)), map[string]bool{"dev1/shutdown": true}, nil, "is online"},
		{"another device's command is no excuse", transition(on, off, device(true)), map[string]bool{"dev2/shutdown": true}, nil, "went offline"},
		{"first sighting online", transition(unk, on, device(true)), nil, nil, ""},
		{"first sighting offline", transition(unk, off, device(true)), nil, nil, ""},
		{"monitoring switched off", transition(on, off, device(false)), nil, nil, ""},
		{"cannot tell: report it", transition(on, off, device(true)), nil, errors.New("db is locked"), "went offline"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, nil)
			e.fakeChannel("a", alerts.KindStatusChanged)
			e.run()
			hook := alerts.StatusHook{Svc: e.svc, Commands: fakeActive{active: c.active, err: c.lookup}}
			hook.DeviceTransitioned(context.Background(), c.tr)
			if c.wantMsg == "" {
				time.Sleep(100 * time.Millisecond)
				if got := e.fake.Sent(); len(got) != 0 {
					t.Fatalf("unexpected alert: %+v", got)
				}
				return
			}
			eventually(t, "the alert", func() bool { return len(e.fake.Sent()) == 1 })
			m := e.fake.Sent()[0]
			if m.Kind != alerts.KindStatusChanged || !strings.Contains(m.Title, c.wantMsg) || !strings.Contains(m.Body, "192.168.1.20:22") {
				t.Fatalf("alert: %+v", m)
			}
		})
	}
}

// REQ-ALERT-003: who asked, and how it went.
func TestCommandHook(t *testing.T) {
	user := auth.WithPrincipal(context.Background(), auth.Principal{UserID: "u1", Username: "alice"})
	key := auth.WithPrincipal(context.Background(), auth.Principal{UserID: "u1", Username: "alice", KeyID: "k1", Scope: "mcp"})
	d := device(true)
	cmd := func(typ commands.Type, st commands.Status, code string) commands.Command {
		return commands.Command{ID: "c1", DeviceID: d.ID, Type: typ, Status: st, ErrorCode: code}
	}
	cases := []struct {
		name  string
		ctx   context.Context
		c     commands.Command
		title string
		body  []string
	}{
		{"wake from the UI", user, cmd(commands.TypeWake, commands.StatusSent, ""), "⚡ Wake requested: Gaming PC", []string{"packet was sent", "By alice"}},
		{"shutdown through a key", key, cmd(commands.TypeShutdown, commands.StatusAcknowledged, ""), "⏻ Shutdown requested: Gaming PC", []string{"agent accepted", "By alice (service key)"}},
		{"wake that failed", user, cmd(commands.TypeWake, commands.StatusFailed, "wol_send_failed"), "❌ Wake failed: Gaming PC", []string{"wol_send_failed"}},
		{"shutdown that failed", user, cmd(commands.TypeShutdown, commands.StatusFailed, "agent_unreachable"), "❌ Shutdown failed: Gaming PC", []string{"agent_unreachable"}},
		{"nobody signed in", context.Background(), cmd(commands.TypeWake, commands.StatusSent, ""), "⚡ Wake requested: Gaming PC", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, nil)
			e.fakeChannel("a", alerts.KindPowerCommand)
			e.run()
			alerts.CommandHook{Svc: e.svc}.CommandIssued(c.ctx, c.c, d)
			eventually(t, "the alert", func() bool { return len(e.fake.Sent()) == 1 })
			m := e.fake.Sent()[0]
			if m.Kind != alerts.KindPowerCommand || m.Title != c.title {
				t.Fatalf("alert: %+v", m)
			}
			for _, w := range c.body {
				if !strings.Contains(m.Body, w) {
					t.Errorf("body %q lacks %q", m.Body, w)
				}
			}
			if c.body == nil && strings.Contains(m.Body, "By") {
				t.Errorf("body names a requester although nobody was signed in: %q", m.Body)
			}
		})
	}
}

// REQ-ALERT-002.
func TestIPHook(t *testing.T) {
	e := newEnv(t, nil)
	e.fakeChannel("a", alerts.KindIPChanged)
	e.run()
	hook := alerts.IPHook{Svc: e.svc}
	hook.IPChanged(context.Background(), ddns.IPChange{RecordName: "home.example.com", RecordType: ddns.RecordA, OldIP: "1.1.1.1", NewIP: "2.2.2.2"})
	hook.IPChanged(context.Background(), ddns.IPChange{RecordName: "home.example.com", RecordType: ddns.RecordA, NewIP: "3.3.3.3"})
	eventually(t, "both alerts", func() bool { return len(e.fake.Sent()) == 2 })
	got := e.fake.Sent()
	if got[0].Kind != alerts.KindIPChanged || got[0].Title != "🌐 Public IP changed" || got[0].Body != "home.example.com (A)\n1.1.1.1 → 2.2.2.2" {
		t.Fatalf("first: %+v", got[0])
	}
	if !strings.Contains(got[1].Body, "(empty) → 3.3.3.3") {
		t.Fatalf("second: %+v", got[1])
	}
}
