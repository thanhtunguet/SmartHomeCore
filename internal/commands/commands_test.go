package commands

import "testing"

func TestTransitions(t *testing.T) {
	allowed := []struct {
		t        Type
		from, to Status
	}{
		{TypeWake, StatusPending, StatusSent},
		{TypeWake, StatusSent, StatusCompleted},
		{TypeShutdown, StatusSent, StatusAcknowledged},
		{TypeShutdown, StatusAcknowledged, StatusCompleted},
		{TypeShutdown, StatusAcknowledged, StatusTimedOut},
	}
	for _, c := range allowed {
		if err := Transition(c.t, c.from, c.to); err != nil {
			t.Errorf("expected allowed: %v", err)
		}
	}
	forbidden := []struct {
		t        Type
		from, to Status
	}{
		{TypeWake, StatusSent, StatusAcknowledged}, // wake has no agent ack
		{TypeShutdown, StatusPending, StatusCompleted},
		{TypeShutdown, StatusCompleted, StatusPending}, // terminal
		{TypeShutdown, StatusFailed, StatusSent},
	}
	for _, c := range forbidden {
		if CanTransition(c.t, c.from, c.to) {
			t.Errorf("%s %s → %s must be forbidden", c.t, c.from, c.to)
		}
	}
	for _, s := range []Status{StatusCompleted, StatusFailed, StatusTimedOut} {
		if !s.Terminal() {
			t.Errorf("%s must be terminal", s)
		}
	}
}
