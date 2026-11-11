package monitoring

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/thanhtunguet/homecore/internal/devices"
)

func TestTrackerHysteresis(t *testing.T) {
	tr := NewTracker(2)
	steps := []struct {
		reachable bool
		want      devices.Status
		changed   bool
	}{
		{false, devices.StatusUnknown, false}, // 1 failure: not yet conclusive
		{false, devices.StatusOffline, true},  // 2 failures → offline
		{true, devices.StatusOnline, true},    // 1 success → online
		{false, devices.StatusOnline, false},  // single blip ignored
		{true, devices.StatusOnline, false},
		{false, devices.StatusOnline, false},
		{false, devices.StatusOffline, true},
	}
	for i, s := range steps {
		got, changed := tr.Observe(s.reachable)
		if got != s.want || changed != s.changed {
			t.Fatalf("step %d: got (%s,%v), want (%s,%v)", i, got, changed, s.want, s.changed)
		}
	}
}

func TestProbe(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	if r := Probe(context.Background(), "127.0.0.1", port, time.Second); !r.Reachable {
		t.Fatalf("expected reachable: %v", r.Err)
	}
	_ = ln.Close()
	if r := Probe(context.Background(), "127.0.0.1", port, 200*time.Millisecond); r.Reachable {
		t.Fatal("expected unreachable after close")
	}
}
