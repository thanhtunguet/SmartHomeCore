package commands

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/thanhtunguet/homecore/internal/devices"
	"github.com/thanhtunguet/homecore/internal/page"
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

type sentPacket struct {
	mac       string
	broadcast string
	port      int
}

// fakeSender records magic packets; err makes every send fail.
type fakeSender struct {
	mu   sync.Mutex
	sent []sentPacket
	err  error
}

func (f *fakeSender) SendWake(_ context.Context, mac net.HardwareAddr, broadcast string, port int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, sentPacket{mac.String(), broadcast, port})
	return nil
}

func (f *fakeSender) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.sent) }

type fakeProber struct {
	reachable bool
	err       error
	calls     atomic.Int32
}

func (p *fakeProber) Probe(context.Context, string) (bool, error) {
	p.calls.Add(1)
	return p.reachable, p.err
}

type env struct {
	db     *storage.DB
	devs   *devices.Service
	repo   *SQLiteRepository
	eng    *Engine
	clock  *fakeClock
	sender *fakeSender
	user   string
}

func newEnv(t *testing.T, prober Prober) *env {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "cmd.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	e := &env{
		db:     db,
		clock:  &fakeClock{t: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)},
		sender: &fakeSender{},
		user:   uuid.NewString(),
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO users (id, username, password_hash, created_at, updated_at) VALUES (?, 'alice', 'x', 0, 0)`, e.user); err != nil {
		t.Fatal(err)
	}
	e.devs = devices.NewService(devices.NewSQLiteRepository(db), e.clock.Now)
	e.repo = NewSQLiteRepository(db)
	e.eng = NewEngine(Config{
		Repo: e.repo, Devices: e.devs, Prober: prober, Now: e.clock.Now,
		Log:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		WakeDeadline: 180 * time.Second, ShutdownDeadline: 120 * time.Second,
	})
	e.eng.Register(TypeWake, WakeDispatcher{Sender: e.sender})
	return e
}

func (e *env) device(t *testing.T, name, mac string) devices.Device {
	t.Helper()
	d, err := e.devs.Create(context.Background(), devices.CreateInput{
		Name: name, IPAddress: "192.168.1.20", MonitorPort: 22, MACAddress: mac,
		WOLBroadcastAddress: "192.168.1.255", WOLPort: ptr(7),
	})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func ptr[T any](v T) *T { return &v }

func (e *env) setStatus(t *testing.T, id string, s devices.Status) {
	t.Helper()
	if _, err := e.db.ExecContext(context.Background(), `UPDATE devices SET status = ? WHERE id = ?`, string(s), id); err != nil {
		t.Fatal(err)
	}
}

func (e *env) wake(t *testing.T, deviceID string) Command {
	t.Helper()
	c, err := e.eng.Request(context.Background(), Request{DeviceID: deviceID, Type: TypeWake, RequestedBy: e.user})
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	return c
}

func TestWakeSendsMagicPacketAndIsSent(t *testing.T) {
	e := newEnv(t, nil)
	d := e.device(t, "pc", "AA:BB:CC:DD:EE:FF")
	c := e.wake(t, d.ID)

	if c.Status != StatusSent || c.Type != TypeWake || c.DeviceID != d.ID || c.RequestedBy != e.user || c.Attempts != 1 {
		t.Fatalf("command: %+v", c)
	}
	if c.DeadlineAt == nil || !c.DeadlineAt.Equal(e.clock.Now().Add(180*time.Second)) {
		t.Fatalf("deadline %v, want now+180s", c.DeadlineAt)
	}
	if c.SentAt == nil || c.CompletedAt != nil {
		t.Fatalf("timestamps: %+v", c)
	}
	if uuid.MustParse(c.ID).Version() != 7 {
		t.Fatalf("command id %s is not a UUIDv7", c.ID)
	}
	want := sentPacket{mac: "aa:bb:cc:dd:ee:ff", broadcast: "192.168.1.255", port: 7}
	if e.sender.count() != 1 || e.sender.sent[0] != want {
		t.Fatalf("packet: %+v, want %+v", e.sender.sent, want)
	}
	if got, err := e.eng.Get(context.Background(), c.ID); err != nil || got.Status != StatusSent {
		t.Fatalf("stored: %+v %v", got, err)
	}
}

func TestWakeWithoutMACIsAPreconditionFailure(t *testing.T) {
	e := newEnv(t, nil)
	d := e.device(t, "pc", "")
	_, err := e.eng.Request(context.Background(), Request{DeviceID: d.ID, Type: TypeWake, RequestedBy: e.user})
	var pe *PreconditionError
	if !errors.Is(err, ErrPrecondition) || !errors.As(err, &pe) || pe.Message == "" {
		t.Fatalf("got %v", err)
	}
	if e.sender.count() != 0 {
		t.Fatal("no packet may be sent")
	}
	if _, err := e.eng.Request(context.Background(), Request{DeviceID: uuid.NewString(), Type: TypeWake}); !errors.Is(err, devices.ErrNotFound) {
		t.Fatalf("unknown device: %v", err)
	}
	if _, err := e.eng.Request(context.Background(), Request{DeviceID: d.ID, Type: TypeShutdown}); !errors.Is(err, ErrNoDispatcher) {
		t.Fatalf("unregistered type: %v", err)
	}
}

func TestWakeIsIdempotent(t *testing.T) {
	e := newEnv(t, nil)
	d := e.device(t, "pc", "aa:bb:cc:dd:ee:ff")
	first := e.wake(t, d.ID)
	second := e.wake(t, d.ID)
	if second.ID != first.ID || second.Status != StatusSent {
		t.Fatalf("second request returned %+v, want the active command %s", second, first.ID)
	}
	if e.sender.count() != 1 {
		t.Fatalf("%d packets for one logical wake", e.sender.count())
	}
	// Once terminal, a new request starts a new command.
	e.eng.DeviceObserved(context.Background(), d.ID, devices.StatusOnline, e.clock.Now())
	e.setStatus(t, d.ID, devices.StatusOffline)
	third := e.wake(t, d.ID)
	if third.ID == first.ID || third.Status != StatusSent || e.sender.count() != 2 {
		t.Fatalf("third: %+v packets=%d", third, e.sender.count())
	}
}

func TestConcurrentWakesCollapseIntoOneCommand(t *testing.T) {
	e := newEnv(t, nil)
	d := e.device(t, "pc", "aa:bb:cc:dd:ee:ff")
	const n = 16
	ids := make([]string, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := e.eng.Request(context.Background(), Request{DeviceID: d.ID, Type: TypeWake, RequestedBy: e.user})
			if err != nil {
				t.Errorf("request %d: %v", i, err)
				return
			}
			ids[i] = c.ID
		}()
	}
	wg.Wait()
	for _, id := range ids {
		if id != ids[0] {
			t.Fatalf("different commands for one device: %v", ids)
		}
	}
	var rows int
	_ = e.db.QueryRow(`SELECT count(*) FROM commands`).Scan(&rows)
	if rows != 1 || e.sender.count() != 1 {
		t.Fatalf("rows=%d packets=%d, want 1 and 1", rows, e.sender.count())
	}
}

func TestDatabaseEnforcesOneActiveCommand(t *testing.T) {
	e := newEnv(t, nil)
	d := e.device(t, "pc", "aa:bb:cc:dd:ee:ff")
	now := e.clock.Now()
	mk := func(status Status) Command {
		return Command{ID: uuid.NewString(), DeviceID: d.ID, Type: TypeWake, Status: status, CreatedAt: now, UpdatedAt: now}
	}
	ctx := context.Background()
	if err := e.repo.Insert(ctx, mk(StatusPending)); err != nil {
		t.Fatal(err)
	}
	if err := e.repo.Insert(ctx, mk(StatusSent)); !errors.Is(err, ErrActiveExists) {
		t.Fatalf("second active command: %v", err)
	}
	if err := e.repo.Insert(ctx, mk(StatusCompleted)); err != nil {
		t.Fatalf("terminal commands are not limited: %v", err)
	}
	other := mk(StatusPending)
	other.Type = TypeShutdown
	if err := e.repo.Insert(ctx, other); err != nil {
		t.Fatalf("the other type is independent: %v", err)
	}
}

func TestWakeOfOnlineDeviceCompletesImmediately(t *testing.T) {
	prober := &fakeProber{reachable: true}
	e := newEnv(t, prober)
	d := e.device(t, "pc", "aa:bb:cc:dd:ee:ff")
	e.setStatus(t, d.ID, devices.StatusOnline)

	c := e.wake(t, d.ID)
	if c.Status != StatusCompleted || c.CompletedAt == nil || c.DeadlineAt != nil {
		t.Fatalf("command: %+v", c)
	}
	if e.sender.count() != 0 || prober.calls.Load() != 1 {
		t.Fatalf("packets=%d probes=%d, want 0 and 1 fresh probe", e.sender.count(), prober.calls.Load())
	}
	// Repeating it is just another completed command, never an error.
	if again := e.wake(t, d.ID); again.Status != StatusCompleted || again.ID == c.ID {
		t.Fatalf("again: %+v", again)
	}
}

func TestStaleOnlineStatusIsConfirmedByAFreshProbe(t *testing.T) {
	prober := &fakeProber{reachable: false}
	e := newEnv(t, prober)
	d := e.device(t, "pc", "aa:bb:cc:dd:ee:ff")
	e.setStatus(t, d.ID, devices.StatusOnline) // stored status is stale: the machine is off

	c := e.wake(t, d.ID)
	if c.Status != StatusSent || e.sender.count() != 1 {
		t.Fatalf("a stale online status must not swallow the wake: %+v packets=%d", c, e.sender.count())
	}
	// A probe error is not evidence either.
	prober.err = errors.New("boom")
	e.eng.DeviceObserved(context.Background(), d.ID, devices.StatusOnline, e.clock.Now())
	prober.reachable = true
	e.setStatus(t, d.ID, devices.StatusOnline)
	if c := e.wake(t, d.ID); c.Status != StatusSent || e.sender.count() != 2 {
		t.Fatalf("probe error: %+v", c)
	}
}

func TestOfflineAndUnknownDevicesAreNotProbedBeforeSending(t *testing.T) {
	prober := &fakeProber{}
	e := newEnv(t, prober)
	d := e.device(t, "pc", "aa:bb:cc:dd:ee:ff") // status unknown
	if c := e.wake(t, d.ID); c.Status != StatusSent {
		t.Fatalf("%+v", c)
	}
	if prober.calls.Load() != 0 {
		t.Fatal("the wake button must not wait for a probe of a machine that is off")
	}
}

func TestSendFailureFailsTheCommandAtOnce(t *testing.T) {
	e := newEnv(t, nil)
	e.sender.err = errors.New("network is unreachable")
	d := e.device(t, "pc", "aa:bb:cc:dd:ee:ff")
	c := e.wake(t, d.ID)
	if c.Status != StatusFailed || c.ErrorCode != ErrCodeWOLSend || c.Error == "" || c.CompletedAt == nil || c.Attempts != 1 {
		t.Fatalf("command: %+v", c)
	}
	// A failed command is terminal: the next request is a fresh one.
	e.sender.err = nil
	if next := e.wake(t, d.ID); next.ID == c.ID || next.Status != StatusSent {
		t.Fatalf("next: %+v", next)
	}
}

func TestMonitorTransitionCompletesWake(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	d := e.device(t, "pc", "aa:bb:cc:dd:ee:ff")
	c := e.wake(t, d.ID)

	pt, err := e.eng.PowerTransitions(ctx, []string{d.ID})
	if err != nil || pt[d.ID] != devices.TransitionWaking {
		t.Fatalf("power transition while active: %v %v", pt, err)
	}

	// Offline observations and observations of other devices change nothing.
	e.eng.DeviceObserved(ctx, d.ID, devices.StatusOffline, e.clock.Now())
	e.eng.DeviceObserved(ctx, uuid.NewString(), devices.StatusOnline, e.clock.Now())
	if got, _ := e.eng.Get(ctx, c.ID); got.Status != StatusSent {
		t.Fatalf("completed too early: %+v", got)
	}

	e.clock.Advance(42 * time.Second)
	e.eng.DeviceObserved(ctx, d.ID, devices.StatusOnline, e.clock.Now())
	got, _ := e.eng.Get(ctx, c.ID)
	if got.Status != StatusCompleted || got.CompletedAt == nil || !got.CompletedAt.Equal(e.clock.Now()) {
		t.Fatalf("after the monitor saw the device online: %+v", got)
	}
	if pt, _ := e.eng.PowerTransitions(ctx, []string{d.ID}); len(pt) != 0 {
		t.Fatalf("power_transition must be none after completion: %v", pt)
	}
	// Idempotent: a second observation changes nothing.
	e.clock.Advance(time.Second)
	e.eng.DeviceObserved(ctx, d.ID, devices.StatusOnline, e.clock.Now())
	if again, _ := e.eng.Get(ctx, c.ID); !again.UpdatedAt.Equal(got.UpdatedAt) {
		t.Fatal("a completed command was touched again")
	}
}

func TestDeadlineTimesOutSentCommands(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	d := e.device(t, "pc", "aa:bb:cc:dd:ee:ff")
	c := e.wake(t, d.ID)

	e.clock.Advance(179 * time.Second)
	if err := e.eng.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.eng.Get(ctx, c.ID); got.Status != StatusSent {
		t.Fatalf("timed out 1 s early: %+v", got)
	}
	e.clock.Advance(time.Second) // exactly deadline_at
	if err := e.eng.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := e.eng.Get(ctx, c.ID)
	if got.Status != StatusTimedOut || got.CompletedAt == nil {
		t.Fatalf("after the deadline: %+v", got)
	}
	// A late observation cannot resurrect it.
	e.eng.DeviceObserved(ctx, d.ID, devices.StatusOnline, e.clock.Now())
	if got, _ = e.eng.Get(ctx, c.ID); got.Status != StatusTimedOut {
		t.Fatalf("terminal state changed: %+v", got)
	}
	if next := e.wake(t, d.ID); next.ID == c.ID {
		t.Fatal("a timed out command must not be returned as active")
	}
}

func TestRecoveryAfterRestart(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	now := e.clock.Now()
	mk := func(name, mac string, typ Type, status Status, deadline time.Duration) Command {
		d := e.device(t, name, mac)
		dl := now.Add(deadline)
		c := Command{ID: uuid.NewString(), DeviceID: d.ID, Type: typ, Status: status, RequestedBy: e.user, DeadlineAt: &dl, CreatedAt: now.Add(-10 * time.Minute), UpdatedAt: now.Add(-10 * time.Minute)}
		if err := e.repo.Insert(ctx, c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	sentExpired := mk("sent-expired", "00:00:00:00:00:01", TypeWake, StatusSent, -time.Minute)
	sentLive := mk("sent-live", "00:00:00:00:00:02", TypeWake, StatusSent, time.Minute)
	ackExpired := mk("ack-expired", "00:00:00:00:00:03", TypeShutdown, StatusAcknowledged, -time.Second)
	pendingLive := mk("pending-live", "00:00:00:00:00:04", TypeWake, StatusPending, time.Minute)
	pendingExpired := mk("pending-expired", "00:00:00:00:00:05", TypeWake, StatusPending, -time.Minute)

	if err := e.eng.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	status := func(c Command) Command {
		got, err := e.eng.Get(ctx, c.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := status(sentExpired); got.Status != StatusTimedOut {
		t.Fatalf("sent past deadline: %+v", got)
	}
	if got := status(ackExpired); got.Status != StatusTimedOut {
		t.Fatalf("acknowledged past deadline: %+v", got)
	}
	if got := status(sentLive); got.Status != StatusSent {
		t.Fatalf("sent within deadline must continue: %+v", got)
	}
	if got := status(pendingExpired); got.Status != StatusFailed || got.ErrorCode != ErrCodeInterrupted {
		t.Fatalf("pending past deadline: %+v", got)
	}
	// A pending command that is still wanted is dispatched again with the same id.
	if got := status(pendingLive); got.Status != StatusSent || got.ID != pendingLive.ID {
		t.Fatalf("pending within deadline: %+v", got)
	}
	if e.sender.count() != 1 {
		t.Fatalf("%d packets sent by recovery, want 1 (only the live pending command)", e.sender.count())
	}
	// The surviving command is completed by a monitor observation as usual.
	e.eng.DeviceObserved(ctx, sentLive.DeviceID, devices.StatusOnline, now)
	if got := status(sentLive); got.Status != StatusCompleted {
		t.Fatalf("recovered command did not complete: %+v", got)
	}
}

func TestRunSweepsAndStopsOnCancel(t *testing.T) {
	e := newEnv(t, nil)
	e.eng.cfg.Now = time.Now
	e.eng.cfg.WakeDeadline = 40 * time.Millisecond
	e.eng.cfg.SweepInterval = 10 * time.Millisecond
	d := e.device(t, "pc", "aa:bb:cc:dd:ee:ff")
	c, err := e.eng.Request(context.Background(), Request{DeviceID: d.ID, Type: TypeWake, RequestedBy: e.user})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- e.eng.Run(ctx) }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		got, _ := e.eng.Get(context.Background(), c.ID)
		if got.Status == StatusTimedOut {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Run never timed the command out: %+v", got)
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop")
	}
}

// fakeShutdown stands in for the Phase 3 dispatcher: it proves the engine is type-generic.
type fakeShutdown struct{ result Result }

func (fakeShutdown) Precheck(d devices.Device) error {
	if d.Agent == nil {
		return &PreconditionError{Message: "no agent"}
	}
	return nil
}
func (f fakeShutdown) Dispatch(context.Context, Command, devices.Device) Result { return f.result }

func TestEngineIsGenericAcrossTypes(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	e.eng.Register(TypeShutdown, fakeShutdown{result: Result{Status: StatusAcknowledged, Attempts: 2}})
	d, err := e.devs.Create(ctx, devices.CreateInput{Name: "srv", IPAddress: "10.0.0.2", MonitorPort: 22,
		MACAddress: "aa:bb:cc:dd:ee:ff", Agent: &devices.AgentInput{AgentID: uuid.NewString()}})
	if err != nil {
		t.Fatal(err)
	}

	w := e.wake(t, d.ID)
	e.clock.Advance(time.Second)
	s, err := e.eng.Request(ctx, Request{DeviceID: d.ID, Type: TypeShutdown, RequestedBy: e.user})
	if err != nil {
		t.Fatal(err)
	}
	if s.Status != StatusAcknowledged || s.Attempts != 2 || s.SentAt == nil || s.AcknowledgedAt == nil {
		t.Fatalf("shutdown: %+v", s)
	}
	if !s.DeadlineAt.Equal(s.CreatedAt.Add(120 * time.Second)) {
		t.Fatalf("shutdown deadline %v", s.DeadlineAt)
	}
	// Wake and shutdown are independent; the newer one defines power_transition.
	if w.ID == s.ID {
		t.Fatal("same command for both types")
	}
	pt, _ := e.eng.PowerTransitions(ctx, []string{d.ID})
	if pt[d.ID] != devices.TransitionShuttingDown {
		t.Fatalf("power transition %v", pt)
	}
	// The monitor completes each in its own direction: offline completes only the shutdown.
	e.eng.DeviceObserved(ctx, d.ID, devices.StatusOffline, e.clock.Now())
	if got, _ := e.eng.Get(ctx, s.ID); got.Status != StatusCompleted {
		t.Fatalf("shutdown after offline: %+v", got)
	}
	if got, _ := e.eng.Get(ctx, w.ID); got.Status != StatusSent {
		t.Fatalf("wake must not complete on offline: %+v", got)
	}

	// A dispatcher result with an unusable status fails the command instead of corrupting it.
	e.eng.Register(TypeShutdown, fakeShutdown{result: Result{Status: StatusCompleted}})
	bad, err := e.eng.Request(ctx, Request{DeviceID: d.ID, Type: TypeShutdown, RequestedBy: e.user})
	if err != nil || bad.Status != StatusFailed || bad.ErrorCode != ErrCodeInternal {
		t.Fatalf("bad dispatcher result: %+v %v", bad, err)
	}
}

func TestListFiltersAndPagination(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	d1 := e.device(t, "one", "aa:bb:cc:dd:ee:01")
	d2 := e.device(t, "two", "aa:bb:cc:dd:ee:02")

	// Seven commands; four share one created_at millisecond so the id tie-break is exercised.
	base := e.clock.Now()
	var ids []string
	insert := func(dev string, status Status, created time.Time) {
		c := Command{ID: uuid.Must(uuid.NewV7()).String(), DeviceID: dev, Type: TypeWake, Status: status, CreatedAt: created, UpdatedAt: created}
		if err := e.repo.Insert(ctx, c); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, c.ID)
	}
	insert(d1.ID, StatusCompleted, base)
	insert(d1.ID, StatusFailed, base.Add(time.Second))
	insert(d2.ID, StatusCompleted, base.Add(2*time.Second))
	for range 3 {
		insert(d2.ID, StatusTimedOut, base.Add(3*time.Second)) // same ms
	}
	insert(d1.ID, StatusSent, base.Add(4*time.Second))

	var got []string
	cursor := ""
	for {
		p, err := e.eng.List(ctx, ListParams{Limit: 3, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range p.Items {
			got = append(got, c.ID)
		}
		if p.NextCursor == "" {
			break
		}
		cursor = p.NextCursor
	}
	if len(got) != 7 {
		t.Fatalf("paged through %d commands, want 7", len(got))
	}
	seen := map[string]bool{}
	for _, id := range got {
		if seen[id] {
			t.Fatalf("duplicate %s across pages", id)
		}
		seen[id] = true
	}
	// Newest first; UUIDv7 ids created in order, so ties come back in reverse creation order.
	for i := range got {
		if got[i] != ids[len(ids)-1-i] {
			t.Fatalf("order differs from newest-first at %d:\n got %v\nwant reverse of %v", i, got, ids)
		}
	}

	p, _ := e.eng.List(ctx, ListParams{DeviceID: d1.ID})
	if len(p.Items) != 3 {
		t.Fatalf("device filter: %d", len(p.Items))
	}
	p, _ = e.eng.List(ctx, ListParams{Status: StatusTimedOut})
	if len(p.Items) != 3 || p.NextCursor != "" {
		t.Fatalf("status filter: %d", len(p.Items))
	}
	p, _ = e.eng.List(ctx, ListParams{DeviceID: d2.ID, Status: StatusCompleted})
	if len(p.Items) != 1 {
		t.Fatalf("combined filter: %d", len(p.Items))
	}

	if _, err := e.eng.List(ctx, ListParams{Limit: 500}); !errors.Is(err, page.ErrBadLimit) {
		t.Fatalf("limit: %v", err)
	}
	if _, err := e.eng.List(ctx, ListParams{Cursor: "garbage"}); !errors.Is(err, page.ErrBadCursor) {
		t.Fatalf("cursor: %v", err)
	}
	if _, err := e.eng.List(ctx, ListParams{Status: "weird"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("status: %v", err)
	}
	if _, err := e.eng.Get(ctx, uuid.NewString()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown command: %v", err)
	}
}

func TestPruneTerminalKeepsActiveCommands(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	d := e.device(t, "pc", "aa:bb:cc:dd:ee:ff")
	old := e.clock.Now().Add(-90 * 24 * time.Hour)
	for _, st := range []Status{StatusCompleted, StatusFailed, StatusTimedOut} {
		c := Command{ID: uuid.NewString(), DeviceID: d.ID, Type: TypeWake, Status: st, CreatedAt: old, UpdatedAt: old}
		if err := e.repo.Insert(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	live := Command{ID: uuid.NewString(), DeviceID: d.ID, Type: TypeWake, Status: StatusSent, CreatedAt: old, UpdatedAt: old}
	if err := e.repo.Insert(ctx, live); err != nil {
		t.Fatal(err)
	}
	cutoff := e.clock.Now().Add(-30 * 24 * time.Hour)
	n, err := e.repo.PruneTerminal(ctx, cutoff, 2)
	if err != nil || n != 2 {
		t.Fatalf("first batch %d %v", n, err)
	}
	if n, err = e.repo.PruneTerminal(ctx, cutoff, 2); err != nil || n != 1 {
		t.Fatalf("second batch %d %v", n, err)
	}
	if _, err := e.eng.Get(ctx, live.ID); err != nil {
		t.Fatalf("active command was pruned: %v", err)
	}
}

func TestWakeDispatcherSendsARealMagicPacket(t *testing.T) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Skipf("cannot listen on UDP loopback: %v", err)
	}
	defer conn.Close()
	port := conn.LocalAddr().(*net.UDPAddr).Port

	e := newEnv(t, nil)
	e.eng.Register(TypeWake, WakeDispatcher{}) // the real wol.Send
	d, err := e.devs.Create(context.Background(), devices.CreateInput{
		Name: "pc", IPAddress: "127.0.0.1", MonitorPort: 22, MACAddress: "aa:bb:cc:dd:ee:ff",
		WOLBroadcastAddress: "127.0.0.1", WOLPort: &port,
	})
	if err != nil {
		t.Fatal(err)
	}
	c := e.wake(t, d.ID)
	if c.Status != StatusSent {
		t.Fatalf("command: %+v", c)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 256)
	n, _, err := conn.ReadFromUDP(buf)
	if err != nil || n != 102 {
		t.Fatalf("read %d bytes, %v", n, err)
	}
	for i := range 6 {
		if buf[i] != 0xff {
			t.Fatal("magic packet must start with six 0xff bytes")
		}
	}
	if net.HardwareAddr(buf[6:12]).String() != "aa:bb:cc:dd:ee:ff" {
		t.Fatalf("mac in packet: %x", buf[6:12])
	}
}
