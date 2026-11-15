package monitoring

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/thanhtunguet/homecore/internal/devices"
	"github.com/thanhtunguet/homecore/internal/storage"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// fakeProber answers from a table keyed by "host:port" and counts calls.
type fakeProber struct {
	mu    sync.Mutex
	up    map[string]bool
	calls map[string]int
	block chan struct{} // when set, every probe waits for it to be closed (or ctx)
	cur   atomic.Int32
	peak  atomic.Int32
}

func newFakeProber() *fakeProber { return &fakeProber{up: map[string]bool{}, calls: map[string]int{}} }

func (p *fakeProber) set(host string, port int, up bool) {
	p.mu.Lock()
	p.up[net.JoinHostPort(host, strconv.Itoa(port))] = up
	p.mu.Unlock()
}

func (p *fakeProber) count(host string, port int) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls[net.JoinHostPort(host, strconv.Itoa(port))]
}

func (p *fakeProber) probe(ctx context.Context, host string, port int, _ time.Duration) ProbeResult {
	n := p.cur.Add(1)
	defer p.cur.Add(-1)
	for {
		old := p.peak.Load()
		if n <= old || p.peak.CompareAndSwap(old, n) {
			break
		}
	}
	if p.block != nil {
		select {
		case <-p.block:
		case <-ctx.Done():
			return ProbeResult{Err: ctx.Err()}
		}
	}
	key := net.JoinHostPort(host, strconv.Itoa(port))
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls[key]++
	if p.up[key] {
		return ProbeResult{Reachable: true, Latency: 4 * time.Millisecond}
	}
	return ProbeResult{Err: errors.New("refused")}
}

type observed struct {
	id     string
	status devices.Status
}

type recorder struct {
	mu   sync.Mutex
	seen []observed
}

func (r *recorder) DeviceObserved(_ context.Context, id string, s devices.Status, _ time.Time) {
	r.mu.Lock()
	r.seen = append(r.seen, observed{id, s})
	r.mu.Unlock()
}

func (r *recorder) all() []observed {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]observed(nil), r.seen...)
}

type env struct {
	db     *storage.DB
	svc    *devices.Service
	repo   *devices.SQLiteRepository
	mon    *Monitor
	clock  *fakeClock
	prober *fakeProber
	obs    *recorder
}

func newEnv(t *testing.T, mutate func(*Config)) *env {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "mon.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	e := &env{
		db:     db,
		repo:   devices.NewSQLiteRepository(db),
		clock:  &fakeClock{t: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)},
		prober: newFakeProber(),
		obs:    &recorder{},
	}
	e.svc = devices.NewService(e.repo, e.clock.Now)
	cfg := Config{
		Store: e.repo, Observer: e.obs, Probe: e.prober.probe, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Tick: 5 * time.Second, Concurrency: 4, OfflineThreshold: 2, Now: e.clock.Now,
		Jitter: func(max time.Duration) time.Duration { return max / 2 }, // exactly the interval
	}
	if mutate != nil {
		mutate(&cfg)
	}
	e.mon = New(cfg)
	return e
}

func (e *env) add(t *testing.T, name, ip string, enabled bool) devices.Device {
	t.Helper()
	d, err := e.svc.Create(context.Background(), devices.CreateInput{
		Name: name, IPAddress: ip, MonitorPort: 22, Enabled: &enabled,
		MonitorIntervalSeconds: ptr(30),
	})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func ptr[T any](v T) *T { return &v }

func (e *env) device(t *testing.T, id string) devices.Device {
	t.Helper()
	d, err := e.svc.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func (e *env) history(t *testing.T, id string) []devices.StatusChange {
	t.Helper()
	p, err := e.svc.History(context.Background(), id, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	return p.Items
}

func TestHysteresisAndPersistence(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	d := e.add(t, "pc", "10.0.0.1", true)

	step := func(up bool, wantStatus devices.Status, wantHistory int) {
		t.Helper()
		e.prober.set("10.0.0.1", 22, up)
		e.mon.CheckDue(ctx)
		e.clock.Advance(31 * time.Second) // past the 30 s interval
		got := e.device(t, d.ID)
		if got.Status != wantStatus {
			t.Fatalf("status %s, want %s", got.Status, wantStatus)
		}
		if h := len(e.history(t, d.ID)); h != wantHistory {
			t.Fatalf("history rows %d, want %d", h, wantHistory)
		}
	}
	step(false, devices.StatusUnknown, 0) // 1 failure: not conclusive yet
	step(false, devices.StatusOffline, 1) // threshold 2 reached
	step(false, devices.StatusOffline, 1) // still offline: no new row
	step(true, devices.StatusOnline, 2)   // one success is enough
	step(false, devices.StatusOnline, 2)  // a single blip is ignored
	step(true, devices.StatusOnline, 2)

	got := e.device(t, d.ID)
	if got.LastSeenAt == nil || got.LastCheckedAt == nil || got.LastLatencyMs == nil || *got.LastLatencyMs != 4 {
		t.Fatalf("observed state not persisted: %+v", got)
	}
	h := e.history(t, d.ID)
	if h[0].Status != devices.StatusOnline || h[0].PreviousStatus != devices.StatusOffline || h[0].Reason != "probe" ||
		h[1].Status != devices.StatusOffline || h[1].PreviousStatus != devices.StatusUnknown {
		t.Fatalf("history content: %+v", h)
	}
}

func TestOnlyDueDevicesAreProbed(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	e.add(t, "a", "10.0.0.1", true)
	e.add(t, "disabled", "10.0.0.2", false)
	e.prober.set("10.0.0.1", 22, true)

	e.mon.CheckDue(ctx)
	if e.prober.count("10.0.0.1", 22) != 1 {
		t.Fatal("a new device must be probed on the first tick")
	}
	e.clock.Advance(10 * time.Second)
	e.mon.CheckDue(ctx)
	e.clock.Advance(15 * time.Second) // 25 s since the probe: not due yet
	e.mon.CheckDue(ctx)
	if n := e.prober.count("10.0.0.1", 22); n != 1 {
		t.Fatalf("probed %d times before the interval elapsed", n)
	}
	e.clock.Advance(6 * time.Second) // 31 s
	e.mon.CheckDue(ctx)
	if n := e.prober.count("10.0.0.1", 22); n != 2 {
		t.Fatalf("probed %d times, want 2 after the interval", n)
	}
	if n := e.prober.count("10.0.0.2", 22); n != 0 {
		t.Fatalf("a disabled device was probed %d times", n)
	}
}

func TestJitterSpreadsNextProbe(t *testing.T) {
	// Jitter 0 → next = at + 90 % of the interval; jitter max → 110 %.
	for _, tc := range []struct {
		name    string
		jitter  func(time.Duration) time.Duration
		dueAt   time.Duration
		notYet  time.Duration
		wantTwo bool
	}{
		{"early", func(time.Duration) time.Duration { return 0 }, 27 * time.Second, 26 * time.Second, true},
		{"late", func(m time.Duration) time.Duration { return m }, 33 * time.Second, 32 * time.Second, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, func(c *Config) { c.Jitter = tc.jitter })
			ctx := context.Background()
			e.add(t, "a", "10.0.0.1", true)
			e.prober.set("10.0.0.1", 22, true)
			e.mon.CheckDue(ctx)
			e.clock.Advance(tc.notYet)
			e.mon.CheckDue(ctx)
			if e.prober.count("10.0.0.1", 22) != 1 {
				t.Fatal("probed too early")
			}
			e.clock.Advance(tc.dueAt - tc.notYet)
			e.mon.CheckDue(ctx)
			if e.prober.count("10.0.0.1", 22) != 2 {
				t.Fatal("not probed when due")
			}
		})
	}
}

func TestDeviceWithActiveCommandIsProbedEveryTickEvenWhenDisabled(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	d := e.add(t, "sleeper", "10.0.0.3", false)
	e.prober.set("10.0.0.3", 22, false)
	e.mon.CheckDue(ctx)
	if e.prober.count("10.0.0.3", 22) != 0 {
		t.Fatal("disabled and idle: must not be probed")
	}

	if _, err := e.db.ExecContext(ctx, `INSERT INTO commands (id, device_id, type, status, created_at, updated_at) VALUES (?, ?, 'wake', 'sent', 0, 0)`, uuid.NewString(), d.ID); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		e.clock.Advance(5 * time.Second) // one tick, far below the 30 s interval
		e.mon.CheckDue(ctx)
		if n := e.prober.count("10.0.0.3", 22); n != i {
			t.Fatalf("tick %d: probed %d times", i, n)
		}
	}
	// The observer hears about every observation of an urgent device, not only transitions.
	if seen := e.obs.all(); len(seen) != 3 {
		t.Fatalf("observer calls: %v", seen)
	}
}

func TestTrackerIsSeededFromStoredStatus(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	d := e.add(t, "pc", "10.0.0.1", true)
	// Pretend a previous run stored "online".
	if err := e.repo.RecordObservation(ctx, devices.Observation{DeviceID: d.ID, At: e.clock.Now(), Reachable: true, Status: devices.StatusOnline, Previous: devices.StatusUnknown, Changed: true, Reason: "probe"}); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(time.Minute)

	// A fresh monitor (a restart): first success must not append a transition.
	e.prober.set("10.0.0.1", 22, true)
	e.mon.CheckDue(ctx)
	if h := e.history(t, d.ID); len(h) != 1 {
		t.Fatalf("restart added a spurious transition: %+v", h)
	}
	if got := e.device(t, d.ID); got.Status != devices.StatusOnline {
		t.Fatalf("status %s", got.Status)
	}
	if seen := e.obs.all(); len(seen) != 0 {
		t.Fatalf("no transition, no active command: observer must stay quiet, got %v", seen)
	}
}

func TestObserverSeesTransitions(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	d := e.add(t, "pc", "10.0.0.1", true)
	e.prober.set("10.0.0.1", 22, true)
	e.mon.CheckDue(ctx) // unknown → online
	e.clock.Advance(31 * time.Second)
	e.mon.CheckDue(ctx) // still online
	e.prober.set("10.0.0.1", 22, false)
	e.clock.Advance(31 * time.Second)
	e.mon.CheckDue(ctx) // failure 1
	e.clock.Advance(31 * time.Second)
	e.mon.CheckDue(ctx) // failure 2 → offline
	want := []observed{{d.ID, devices.StatusOnline}, {d.ID, devices.StatusOffline}}
	got := e.obs.all()
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("observer calls %v, want %v", got, want)
	}
}

func TestProbeNowFeedsTheTrackerAndPersists(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	d := e.add(t, "pc", "10.0.0.1", false) // disabled devices can still be probed by hand
	e.prober.set("10.0.0.1", 22, true)

	out, err := e.mon.ProbeNow(ctx, d.ID)
	if err != nil || !out.Result.Reachable || out.Status != devices.StatusOnline {
		t.Fatalf("probe: %+v %v", out, err)
	}
	got := e.device(t, d.ID)
	if got.Status != devices.StatusOnline || got.LastLatencyMs == nil || got.LastCheckedAt == nil {
		t.Fatalf("manual probe not persisted: %+v", got)
	}
	if h := e.history(t, d.ID); len(h) != 1 || h[0].Reason != "manual probe" {
		t.Fatalf("history: %+v", h)
	}
	if seen := e.obs.all(); len(seen) != 1 {
		t.Fatalf("manual probes always notify the observer: %v", seen)
	}

	// One failed manual probe does not flip an online device (hysteresis); a second does.
	e.prober.set("10.0.0.1", 22, false)
	out, _ = e.mon.ProbeNow(ctx, d.ID)
	if out.Result.Reachable || out.Status != devices.StatusOnline || out.Result.FailureReason() != "unreachable" {
		t.Fatalf("first failure: %+v", out)
	}
	if out, _ = e.mon.ProbeNow(ctx, d.ID); out.Status != devices.StatusOffline {
		t.Fatalf("second failure: %+v", out)
	}

	if _, err := e.mon.ProbeNow(ctx, uuid.NewString()); !errors.Is(err, devices.ErrNotFound) {
		t.Fatalf("unknown device: %v", err)
	}
}

func TestDeletedDeviceIsForgotten(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	d := e.add(t, "pc", "10.0.0.1", true)
	e.prober.set("10.0.0.1", 22, true)
	e.mon.CheckDue(ctx)
	if err := e.svc.Delete(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(time.Minute)
	e.mon.CheckDue(ctx)
	e.mon.mu.Lock()
	n := len(e.mon.state)
	e.mon.mu.Unlock()
	if n != 0 {
		t.Fatalf("state for %d deleted devices kept", n)
	}
	if _, err := e.mon.ProbeNow(ctx, d.ID); !errors.Is(err, devices.ErrNotFound) {
		t.Fatalf("probe of a deleted device: %v", err)
	}
}

func TestWorkerPoolIsBounded(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.Concurrency = 2 })
	e.prober.block = make(chan struct{})
	ctx := context.Background()
	for i := range 6 {
		e.add(t, "pc"+strconv.Itoa(i), "10.0.1."+strconv.Itoa(i+1), true)
	}
	done := make(chan struct{})
	go func() { e.mon.CheckDue(ctx); close(done) }()
	deadline := time.Now().Add(2 * time.Second)
	for e.prober.cur.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond) // give a wrongly unbounded pool the chance to show itself
	if got := e.prober.cur.Load(); got != 2 {
		t.Fatalf("%d probes in flight, want exactly the pool size 2", got)
	}
	close(e.prober.block)
	<-done
	if peak := e.prober.peak.Load(); peak > 2 {
		t.Fatalf("peak concurrency %d exceeds the pool size", peak)
	}
	for i := range 6 {
		if e.prober.count("10.0.1."+strconv.Itoa(i+1), 22) != 1 {
			t.Fatalf("device %d was not probed exactly once", i)
		}
	}
}

func TestSameDeviceIsNeverProbedConcurrently(t *testing.T) {
	e := newEnv(t, nil)
	e.prober.block = make(chan struct{})
	ctx := context.Background()
	d := e.add(t, "pc", "10.0.0.1", true)
	e.prober.set("10.0.0.1", 22, true)

	tickDone := make(chan struct{})
	go func() { e.mon.CheckDue(ctx); close(tickDone) }()
	for e.prober.cur.Load() < 1 {
		time.Sleep(time.Millisecond)
	}
	// A second tick while the first probe is still running must skip the device, and a manual probe
	// must wait for it.
	e.mon.CheckDue(ctx)
	manual := make(chan ProbeOutcome, 1)
	go func() { out, _ := e.mon.ProbeNow(ctx, d.ID); manual <- out }()
	time.Sleep(20 * time.Millisecond)
	if e.prober.cur.Load() != 1 {
		t.Fatalf("%d concurrent probes of one device", e.prober.cur.Load())
	}
	close(e.prober.block)
	<-tickDone
	<-manual
	if e.prober.peak.Load() != 1 {
		t.Fatalf("peak %d", e.prober.peak.Load())
	}
}

func TestShutdownDoesNotRecordAbortedProbes(t *testing.T) {
	e := newEnv(t, nil)
	e.prober.block = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	d := e.add(t, "pc", "10.0.0.1", true)
	done := make(chan struct{})
	go func() { e.mon.CheckDue(ctx); close(done) }()
	for e.prober.cur.Load() < 1 {
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	if got := e.device(t, d.ID); got.LastCheckedAt != nil || got.Status != devices.StatusUnknown {
		t.Fatalf("an aborted probe was recorded: %+v", got)
	}
}

func TestRunStopsOnCancelAndLeaksNoGoroutines(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.Tick = 5 * time.Millisecond })
	for i := range 3 {
		e.add(t, "pc"+strconv.Itoa(i), "10.0.2."+strconv.Itoa(i+1), true)
		e.prober.set("10.0.2."+strconv.Itoa(i+1), 22, true)
	}
	before := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- e.mon.Run(ctx) }()
	time.Sleep(60 * time.Millisecond) // several ticks
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after the context was cancelled")
	}
	deadline := time.Now().Add(time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutines leaked: %d before, %d after", before, after)
	}
	if e.prober.count("10.0.2.1", 22) == 0 {
		t.Fatal("Run never probed anything")
	}
}

func TestFailureReason(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	if r := Probe(context.Background(), "127.0.0.1", port, time.Second); r.FailureReason() != "connection refused" {
		t.Fatalf("closed port: %q (%v)", r.FailureReason(), r.Err)
	}
	if r := Probe(context.Background(), "no-such-host.invalid", 22, time.Second); r.FailureReason() != "host name could not be resolved" {
		t.Fatalf("unknown host: %q (%v)", r.FailureReason(), r.Err)
	}
	if (ProbeResult{Reachable: true}).FailureReason() != "" {
		t.Fatal("success has no failure reason")
	}
	if (ProbeResult{Err: context.DeadlineExceeded}).FailureReason() != "timed out" {
		t.Fatal("deadline")
	}
}

type transitionRecorder struct {
	mu  sync.Mutex
	log *[]string // shared with the Observer so the order can be checked
	got []Transition
}

func (r *transitionRecorder) DeviceTransitioned(_ context.Context, t Transition) {
	r.mu.Lock()
	r.got = append(r.got, t)
	*r.log = append(*r.log, "transition:"+string(t.To))
	r.mu.Unlock()
}

type orderObserver struct {
	mu  *sync.Mutex
	log *[]string
}

func (o orderObserver) DeviceObserved(_ context.Context, _ string, s devices.Status, _ time.Time) {
	o.mu.Lock()
	*o.log = append(*o.log, "observed:"+string(s))
	o.mu.Unlock()
}

// REQ-ALERT-004: a TransitionObserver hears every status change, with where it came from, and hears
// it before the Observer (the commands engine completes the command that explains the change).
func TestTransitionObserverSeesChangesBeforeTheObserver(t *testing.T) {
	var log []string
	tr := &transitionRecorder{log: &log}
	e := newEnv(t, func(c *Config) { c.Transitions = tr; c.Observer = orderObserver{&tr.mu, &log} })
	ctx := context.Background()
	d := e.add(t, "pc", "10.0.0.1", true)

	e.prober.set("10.0.0.1", 22, true)
	e.mon.CheckDue(ctx) // unknown → online
	e.clock.Advance(31 * time.Second)
	e.mon.CheckDue(ctx) // still online: no transition
	e.prober.set("10.0.0.1", 22, false)
	e.clock.Advance(31 * time.Second)
	e.mon.CheckDue(ctx) // failure 1: hysteresis, no transition
	e.clock.Advance(31 * time.Second)
	e.mon.CheckDue(ctx) // failure 2 → offline

	if len(tr.got) != 2 {
		t.Fatalf("transitions: %+v", tr.got)
	}
	first, second := tr.got[0], tr.got[1]
	if first.From != devices.StatusUnknown || first.To != devices.StatusOnline || first.Device.ID != d.ID || first.Device.Name != "pc" || first.Reason != "probe" {
		t.Fatalf("first: %+v", first)
	}
	if second.From != devices.StatusOnline || second.To != devices.StatusOffline || second.At.IsZero() {
		t.Fatalf("second: %+v", second)
	}
	want := []string{"transition:online", "observed:online", "transition:offline", "observed:offline"}
	if !slices.Equal(log, want) {
		t.Fatalf("order %v, want %v", log, want)
	}

	// A manual probe is an observation like any other and reports its reason.
	e.prober.set("10.0.0.1", 22, true)
	if _, err := e.mon.ProbeNow(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if last := tr.got[len(tr.got)-1]; last.To != devices.StatusOnline || last.Reason != "manual probe" {
		t.Fatalf("manual probe: %+v", last)
	}
}
