package ddns

import (
	"context"
	"errors"
	"testing"
	"time"
)

// REQ-DDNS-004: the repository round-trips every column and keeps the token only as a sealed blob.
func TestRepositoryRoundTripAndConstraints(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c := e.mk("Home", "r1", func(in *CreateInput) {
		in.Proxied, in.IntervalSeconds, in.Enabled = ptrOf(true), ptrOf(900), ptrOf(false)
	})
	st, err := e.repo.Get(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	g := st.Config
	if g.Name != "Home" || g.ZoneID != zone || g.RecordID != "r1" || g.RecordName != "home.example.com" || g.RecordType != RecordA ||
		!g.Proxied || g.TTL != 1 || g.Interval != 900*time.Second || g.Enabled || !g.HasToken || len(st.TokenSealed) == 0 ||
		!g.CreatedAt.Equal(e.clock.Now()) || !g.UpdatedAt.Equal(e.clock.Now()) || g.LastCheckedAt != nil || g.LastSyncedAt != nil || g.LastErrorAt != nil {
		t.Fatalf("round trip: %+v", g)
	}
	if _, err := e.repo.Get(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v", err)
	}
	// The unique (zone, name, type) key and the (zone, record id) index both map to ErrDuplicate.
	dup := st
	dup.Config.ID = "other-id"
	if err := e.repo.Create(ctx, dup); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("same target: %v", err)
	}
	dup.Config.RecordName = "elsewhere.example.com"
	if err := e.repo.Create(ctx, dup); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("same record id: %v", err)
	}
	// Legacy rows without a record id do not collide with each other.
	for i, n := range []string{"a.example.com", "b.example.com"} {
		l := st
		l.Config.ID, l.Config.RecordName, l.Config.RecordID = "legacy-"+string(rune('0'+i)), n, ""
		if err := e.repo.Create(ctx, l); err != nil {
			t.Fatalf("legacy row %d: %v", i, err)
		}
	}
}

func TestSaveOutcomeSemantics(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c := e.mk("Home", "r1")
	at := e.clock.Now().Add(time.Minute)

	applied, err := e.repo.SaveOutcome(ctx, c.ID, c.UpdatedAt, Outcome{At: at, Result: ResultUpdated, IP: "8.8.8.8", DNSValue: "8.8.8.8"})
	if err != nil || !applied {
		t.Fatalf("%v %v", applied, err)
	}
	// A failed run keeps what was known when it learned nothing new, and records when it failed.
	later := at.Add(time.Minute)
	if applied, err = e.repo.SaveOutcome(ctx, c.ID, c.UpdatedAt, Outcome{At: later, Result: ResultFailed, Error: "providers disagree"}); err != nil || !applied {
		t.Fatalf("%v %v", applied, err)
	}
	got := e.get(c.ID)
	if got.LastIP != "8.8.8.8" || got.DetectedIP != "8.8.8.8" || got.DNSValue != "8.8.8.8" || got.LastError != "providers disagree" ||
		!got.LastErrorAt.Equal(later) || !got.LastCheckedAt.Equal(later) || !got.LastSyncedAt.Equal(at) {
		t.Fatalf("after a failure: %+v", got)
	}
	// A failure that did read the record updates what it saw but never last_ip (nothing was pushed).
	if _, err = e.repo.SaveOutcome(ctx, c.ID, c.UpdatedAt, Outcome{At: later, Result: ResultFailed, IP: "8.8.4.4", DNSValue: "1.1.1.1", Error: "x"}); err != nil {
		t.Fatal(err)
	}
	if got = e.get(c.ID); got.LastIP != "8.8.8.8" || got.DetectedIP != "8.8.4.4" || got.DNSValue != "1.1.1.1" {
		t.Fatalf("%+v", got)
	}
	// Success clears the error.
	if _, err = e.repo.SaveOutcome(ctx, c.ID, c.UpdatedAt, Outcome{At: later.Add(time.Minute), Result: ResultUnchanged, IP: "8.8.4.4", DNSValue: "8.8.4.4"}); err != nil {
		t.Fatal(err)
	}
	if got = e.get(c.ID); got.LastError != "" || got.LastErrorAt != nil || got.LastIP != "8.8.4.4" {
		t.Fatalf("%+v", got)
	}
	// A stale version (the config was edited) or an unknown id is not applied and writes no history.
	rows := e.count("ddns_sync_history")
	for label, id := range map[string]string{"stale version": c.ID, "unknown config": "nope"} {
		version := c.UpdatedAt.Add(time.Second)
		if applied, err = e.repo.SaveOutcome(ctx, id, version, Outcome{At: at, Result: ResultUpdated, IP: "9.9.9.9"}); err != nil || applied {
			t.Fatalf("%s: applied=%v err=%v", label, applied, err)
		}
	}
	if e.count("ddns_sync_history") != rows || e.get(c.ID).LastIP != "8.8.4.4" {
		t.Fatal("a dropped outcome changed the database")
	}
}
