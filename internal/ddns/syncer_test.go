package ddns

import (
	"context"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thanhtunguet/homecore/internal/audit"
)

type syncerEnv struct {
	*env
	s *Syncer
}

func newSyncerEnv(t *testing.T, mod ...func(*SyncerConfig)) *syncerEnv {
	t.Helper()
	e := newEnv(t)
	cfg := SyncerConfig{Service: e.svc, Now: e.clock.Now, Jitter: func() float64 { return 1 }, Log: e.svc.log,
		Audit: audit.NewWriter(e.db, e.clock.Now)}
	for _, m := range mod {
		m(&cfg)
	}
	return &syncerEnv{env: e, s: NewSyncer(cfg)}
}

// tick runs one scheduling pass and waits for the syncs it started.
func (se *syncerEnv) tick() {
	se.t.Helper()
	se.s.dispatch(context.Background())
	se.s.wg.Wait()
}

func (se *syncerEnv) entry(id string) entry {
	se.s.mu.Lock()
	defer se.s.mu.Unlock()
	if e := se.s.entries[id]; e != nil {
		return *e
	}
	return entry{}
}

func (se *syncerEnv) content(id string) string {
	rec, _ := se.cf.Record(zone, id)
	return rec.Content
}

// REQ-DDNS-007: each enabled configuration is synced on its own interval, disabled ones are skipped.
func TestSyncerRunsEachConfigOnItsInterval(t *testing.T) {
	se := newSyncerEnv(t)
	a := se.mk("A", "r1", func(in *CreateInput) { in.IntervalSeconds = ptrOf(60) })
	b := se.mk("B", "r3", func(in *CreateInput) { in.IntervalSeconds = ptrOf(600) })
	off := se.mk("Off", "r5", func(in *CreateInput) { in.Enabled = ptrOf(false) })

	se.tick() // first pass: everything enabled is due at once
	if se.content("r1") != "8.8.8.8" || se.content("r3") != "8.8.8.8" || se.content("r5") != "8.8.4.4" {
		t.Fatalf("records after the first pass: %s %s %s", se.content("r1"), se.content("r3"), se.content("r5"))
	}
	if len(se.history(off.ID)) != 0 || se.get(off.ID).LastCheckedAt != nil {
		t.Fatal("a disabled configuration was synced")
	}

	gets := se.cf.Count("GET", "/dns_records/r1")
	se.tick() // nothing is due yet
	if se.cf.Count("GET", "/dns_records/r1") != gets {
		t.Fatal("a configuration ran before its interval")
	}

	se.clock.Advance(61 * time.Second) // A is due, B is not
	se.tick()
	if len(se.history(a.ID)) != 2 || len(se.history(b.ID)) != 1 {
		t.Fatalf("history rows: A=%d B=%d", len(se.history(a.ID)), len(se.history(b.ID)))
	}

	se.ip.set("8.8.4.5", nil)
	se.clock.Advance(61 * time.Second) // A sees the new address; B (600 s) still waits
	se.tick()
	if se.content("r1") != "8.8.4.5" || se.content("r3") != "8.8.8.8" {
		t.Fatalf("A=%s B=%s", se.content("r1"), se.content("r3"))
	}
	se.clock.Advance(600 * time.Second)
	se.tick()
	if se.content("r3") != "8.8.4.5" {
		t.Fatalf("B=%s", se.content("r3"))
	}

	// Enabling a configuration makes it due at once (the edit changes its version).
	if _, err := se.svc.Update(context.Background(), off.ID, UpdateInput{Enabled: ptrOf(true)}); err != nil {
		t.Fatal(err)
	}
	se.tick()
	if se.content("r5") != "8.8.4.5" {
		t.Fatalf("the re-enabled record: %s", se.content("r5"))
	}
}

// REQ-DDNS-007: exponential backoff with jitter, capped, and a recovery resets it.
func TestSyncerBacksOffOnTransientFailures(t *testing.T) {
	se := newSyncerEnv(t)
	c := se.mk("A", "r1", func(in *CreateInput) { in.IntervalSeconds = ptrOf(3600) })
	se.tick()
	se.ip.set("", quorumFailure())

	want := []time.Duration{1, 2, 4, 8, 16, 32, 64, 128, 256, 300, 300}
	se.clock.Advance(time.Hour)
	for i, w := range want {
		se.tick()
		en := se.entry(c.ID)
		if en.fails != i+1 || !en.next.Equal(se.clock.Now().Add(w*time.Second)) {
			t.Fatalf("failure %d: fails=%d next in %v, want %v", i+1, en.fails, en.next.Sub(se.clock.Now()), w*time.Second)
		}
		// Not due before the delay passed.
		calls := se.ip.calls
		se.clock.Advance(w*time.Second - time.Millisecond)
		se.tick()
		if se.ip.calls != calls {
			t.Fatalf("failure %d: retried before the backoff elapsed", i+1)
		}
		se.clock.Advance(time.Millisecond)
	}
	// Recovery: back to the normal interval, counter reset.
	se.ip.set("8.8.8.8", nil)
	se.tick()
	if en := se.entry(c.ID); en.fails != 0 || !en.next.Equal(se.clock.Now().Add(time.Hour)) {
		t.Fatalf("after recovery: %+v", en)
	}
}

func TestSyncerBackoffIsJitteredAndNeverLongerThanTheInterval(t *testing.T) {
	var j atomic.Value
	j.Store(0.0)
	se := newSyncerEnv(t, func(c *SyncerConfig) { c.Jitter = func() float64 { return j.Load().(float64) } })
	c := se.mk("A", "r1", func(in *CreateInput) { in.IntervalSeconds = ptrOf(60) })
	se.ip.set("", quorumFailure())
	se.tick()
	if en := se.entry(c.ID); !en.next.Equal(se.clock.Now().Add(500 * time.Millisecond)) {
		t.Fatalf("minimum jitter: %v", en.next.Sub(se.clock.Now()))
	}
	j.Store(1.0)
	se.clock.Advance(time.Second)
	for range 12 { // deep into the backoff: 5 min would exceed the 60 s interval
		se.tick()
		se.clock.Advance(time.Duration(65) * time.Second)
	}
	if en := se.entry(c.ID); en.next.Sub(se.clock.Now().Add(-65*time.Second)) > 60*time.Second {
		t.Fatalf("backoff exceeded the interval: %v", en.next.Sub(se.clock.Now().Add(-65*time.Second)))
	}
}

// REQ-DDNS-007: one configuration's trouble never delays another.
func TestSyncerConfigsAreIndependent(t *testing.T) {
	se := newSyncerEnv(t)
	bad := se.mk("Bad", "r1", func(in *CreateInput) { in.IntervalSeconds = ptrOf(600) })
	good := se.mk("Good", "r3", func(in *CreateInput) { in.IntervalSeconds = ptrOf(60) })
	// Cloudflare answers 503 for Bad's record only.
	se.cf.SetFail(func(r *http.Request) (int, string, http.Header) {
		if strings.HasSuffix(r.URL.Path, "/dns_records/r1") {
			return 503, `{}`, nil
		}
		return 0, "", nil
	})
	se.tick()
	if se.entry(bad.ID).fails != 1 || se.get(bad.ID).LastError == "" {
		t.Fatalf("bad: %+v / %+v", se.entry(bad.ID), se.get(bad.ID))
	}
	if se.content("r3") != "8.8.8.8" || se.entry(good.ID).fails != 0 || se.get(good.ID).LastError != "" {
		t.Fatalf("good: content=%s %+v", se.content("r3"), se.entry(good.ID))
	}
	// Bad retries on its own short backoff while Good keeps its interval.
	se.clock.Advance(time.Second)
	se.tick()
	if se.entry(bad.ID).fails != 2 || len(se.history(good.ID)) != 1 {
		t.Fatalf("bad fails=%d, good history=%d", se.entry(bad.ID).fails, len(se.history(good.ID)))
	}
}

func TestSyncerSlowConfigDoesNotBlockOthers(t *testing.T) {
	se := newSyncerEnv(t)
	slow := se.mk("Slow", "r1")
	fast := se.mk("Fast", "r3")
	release := make(chan struct{})
	se.cf.SetFail(func(r *http.Request) (int, string, http.Header) {
		if strings.HasSuffix(r.URL.Path, "/dns_records/r1") {
			<-release
		}
		return 0, "", nil
	})
	se.s.dispatch(context.Background())
	deadline := time.Now().Add(5 * time.Second)
	for len(se.history(fast.ID)) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the fast configuration waited for the slow one")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !se.entry(slow.ID).running || len(se.history(slow.ID)) != 0 {
		t.Fatalf("slow should still be running: %+v", se.entry(slow.ID))
	}
	// While it runs it is not started a second time.
	se.clock.Advance(time.Hour)
	se.s.dispatch(context.Background())
	close(release)
	se.s.wg.Wait()
	if len(se.history(slow.ID)) != 1 {
		t.Fatalf("slow history: %d (a second concurrent run was started)", len(se.history(slow.ID)))
	}
}

func TestSyncerConcurrencyIsBounded(t *testing.T) {
	se := newSyncerEnv(t, func(c *SyncerConfig) { c.Concurrency = 2 })
	for i, id := range []string{"r1", "r3", "r5", "r2"} {
		se.mk("C"+string(rune('a'+i)), id)
	}
	var inflight, peak atomic.Int32
	se.cf.SetFail(func(r *http.Request) (int, string, http.Header) {
		if r.Method == "GET" && strings.Contains(r.URL.Path, "/dns_records/") {
			n := inflight.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(40 * time.Millisecond)
			inflight.Add(-1)
		}
		return 0, "", nil
	})
	se.tick() // 2 start, 2 wait for the next tick
	se.tick()
	if p := peak.Load(); p != 2 {
		t.Fatalf("peak concurrency %d, want 2", p)
	}
	if se.count("ddns_sync_history") != 4 {
		t.Fatalf("history rows: %d", se.count("ddns_sync_history"))
	}
}

// REQ-DDNS-007: Cloudflare's Retry-After is honoured (this cycle fails, the next waits).
func TestSyncerHonoursRateLimits(t *testing.T) {
	se := newSyncerEnv(t)
	c := se.mk("A", "r1", func(in *CreateInput) { in.IntervalSeconds = ptrOf(60) })
	se.cf.SetFail(func(*http.Request) (int, string, http.Header) {
		return 429, `{"success":false,"errors":[{"code":971,"message":"Rate limited"}]}`, http.Header{"Retry-After": {"900"}}
	})
	se.tick()
	en := se.entry(c.ID)
	if en.next.Sub(se.clock.Now()) != 900*time.Second {
		t.Fatalf("next run in %v, want the 900 s Cloudflare asked for (even though the interval is 60 s)", en.next.Sub(se.clock.Now()))
	}
	if got := se.get(c.ID).LastError; !strings.Contains(got, "Rate limited") {
		t.Fatalf("last_error: %q", got)
	}
	requests := len(se.cf.Requests())
	se.clock.Advance(899 * time.Second)
	se.tick()
	if len(se.cf.Requests()) != requests {
		t.Fatal("Cloudflare was contacted before Retry-After elapsed")
	}
}

func TestSyncerPermanentFailuresWaitForTheNextInterval(t *testing.T) {
	se := newSyncerEnv(t)
	c := se.mk("A", "r1", func(in *CreateInput) { in.IntervalSeconds = ptrOf(600) })
	rec, _ := se.cf.Record(zone, "r1")
	rec.Name = "renamed.example.com"
	se.cf.Set(zone, rec)
	se.tick()
	en := se.entry(c.ID)
	if en.fails != 0 || en.next.Sub(se.clock.Now()) != 600*time.Second {
		t.Fatalf("a record mismatch must not be retried with backoff: %+v", en)
	}
	if se.cf.Count("PATCH", "") != 0 {
		t.Fatal("the mismatching record was written to")
	}
	if got := se.get(c.ID); !strings.Contains(got.LastError, "does not match") {
		t.Fatalf("last_error: %q", got.LastError)
	}
}

func TestSyncerEditsApplyImmediatelyAndDeletesAreForgotten(t *testing.T) {
	se := newSyncerEnv(t)
	c := se.mk("A", "r1", func(in *CreateInput) { in.IntervalSeconds = ptrOf(3600) })
	se.ip.set("", quorumFailure())
	se.clock.Advance(time.Second)
	for range 4 {
		se.tick()
		se.clock.Advance(time.Hour)
	}
	if se.entry(c.ID).fails < 3 {
		t.Fatalf("setup: %+v", se.entry(c.ID))
	}
	se.ip.set("8.8.8.8", nil)
	if _, err := se.svc.Update(context.Background(), c.ID, UpdateInput{Name: ptrOf("Renamed")}); err != nil {
		t.Fatal(err)
	}
	se.tick() // no clock movement: the edit alone makes it due and clears the failure count
	if se.entry(c.ID).fails != 0 || se.get(c.ID).LastError != "" {
		t.Fatalf("after the edit: %+v / %q", se.entry(c.ID), se.get(c.ID).LastError)
	}
	if err := se.svc.Delete(context.Background(), c.ID); err != nil {
		t.Fatal(err)
	}
	se.tick()
	se.s.mu.Lock()
	n := len(se.s.entries)
	se.s.mu.Unlock()
	if n != 0 {
		t.Fatalf("entries of deleted configurations kept: %d", n)
	}
}

// The scheduler audits a record update (actor system) but not unchanged or failed runs.
func TestSyncerAuditsUpdatesOnly(t *testing.T) {
	se := newSyncerEnv(t)
	c := se.mk("Home", "r1", func(in *CreateInput) { in.IntervalSeconds = ptrOf(60) })
	se.tick() // updated
	se.clock.Advance(time.Minute)
	se.tick() // unchanged
	se.ip.set("", quorumFailure())
	se.clock.Advance(time.Hour)
	se.tick() // failed
	rows, err := se.db.Query(`SELECT actor, action, target_type, target_id, result, details FROM audit_log`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var actor, action, tt, tid, result, details string
		if err := rows.Scan(&actor, &action, &tt, &tid, &result, &details); err != nil {
			t.Fatal(err)
		}
		n++
		if actor != "system" || action != "ddns.sync" || tt != "ddns_config" || tid != c.ID || result != "ok" ||
			!strings.Contains(details, `"ip":"8.8.8.8"`) || strings.Contains(details, testToken) {
			t.Fatalf("audit row: %s %s %s %s %s %s", actor, action, tt, tid, result, details)
		}
	}
	if n != 1 {
		t.Fatalf("%d audit rows, want exactly the one update", n)
	}
}

// REQ-DDNS-007: Run schedules until cancelled, stops gracefully and leaks no goroutine.
func TestSyncerRunStopsAndLeaksNothing(t *testing.T) {
	se := newSyncerEnv(t, func(c *SyncerConfig) { c.Tick = 10 * time.Millisecond })
	c := se.mk("A", "r1")
	runtime.GC()
	before := runtime.NumGoroutine()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- se.s.Run(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for se.content("r1") != "8.8.8.8" {
		if time.Now().After(deadline) {
			t.Fatal("Run never synced")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
	_ = c
	// Idle HTTP connections of the fake server may take a moment to go away.
	for i := 0; i < 100 && runtime.NumGoroutine() > before+2; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > before+2 {
		buf := make([]byte, 1<<16)
		t.Fatalf("goroutines: %d before, %d after\n%s", before, after, buf[:runtime.Stack(buf, true)])
	}
}

func TestSyncerShutdownWhileSyncing(t *testing.T) {
	se := newSyncerEnv(t, func(c *SyncerConfig) { c.Tick = 10 * time.Millisecond })
	c := se.mk("A", "r1")
	se.ip.gate = make(chan struct{}) // discovery never finishes
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = se.s.Run(ctx); close(done) }()
	for i := 0; ; i++ {
		se.ip.mu.Lock()
		n := se.ip.calls
		se.ip.mu.Unlock()
		if n > 0 {
			break
		}
		if i > 500 {
			t.Fatal("no sync started")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop with a sync in flight")
	}
	if len(se.history(c.ID)) != 0 {
		t.Fatal("an interrupted sync was recorded")
	}
	if en := se.entry(c.ID); en.fails != 0 {
		t.Fatalf("an interrupted sync counted as a failure: %+v", en)
	}
}

func TestSyncerSurvivesAPanic(t *testing.T) {
	se := newSyncerEnv(t)
	c := se.mk("A", "r1")
	se.s.svc = NewService(ServiceConfig{Repo: se.repo, Sealer: se.svc.sealer, DNS: panicDNS{}, IP: se.ip, Now: se.clock.Now})
	se.tick()
	if en := se.entry(c.ID); en.running || en.fails != 1 {
		t.Fatalf("after a panic: %+v", en)
	}
	var wg sync.WaitGroup
	wg.Wait()
}

type panicDNS struct{ DNSProvider }

func (panicDNS) GetRecord(context.Context, string, string, string) (Record, error) { panic("boom") }
