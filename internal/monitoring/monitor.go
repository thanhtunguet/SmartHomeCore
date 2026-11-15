package monitoring

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/thanhtunguet/homecore/internal/devices"
)

// Store is the persistence the Monitor needs (implemented by *devices.SQLiteRepository).
type Store interface {
	// MonitorTargets returns every enabled device and any device with an active command.
	MonitorTargets(ctx context.Context) ([]devices.MonitorTarget, error)
	Get(ctx context.Context, id string) (devices.Device, error)
	RecordObservation(ctx context.Context, o devices.Observation) error
}

// Observer is told about observations that matter to someone else: every status transition, and
// every observation of a device that has an active command. The commands engine implements it to
// complete wake and shutdown commands. It runs on the probing goroutine, so it must be quick.
type Observer interface {
	DeviceObserved(ctx context.Context, deviceID string, status devices.Status, at time.Time)
}

// Transition is a change of a device's observed status after hysteresis (never the very first
// observation of a device that was `unknown`: that is a discovery, and the From says so).
type Transition struct {
	Device devices.Device // as stored when the probe ran
	From   devices.Status
	To     devices.Status
	At     time.Time
	Reason string // "probe" (the tick) or "manual probe"
}

// TransitionObserver is told about every status transition, before the Observer, so it can still see
// the wake/shutdown command that explains the change (the commands engine, the Observer, completes it).
// It runs on the probing goroutine, so it must be quick. Alerts use it (REQ-ALERT-004).
type TransitionObserver interface {
	DeviceTransitioned(ctx context.Context, t Transition)
}

// ProbeFunc makes one TCP connect attempt (Probe is the real one; tests inject fakes).
type ProbeFunc func(ctx context.Context, host string, port int, timeout time.Duration) ProbeResult

// Config configures a Monitor.
type Config struct {
	Store    Store
	Observer Observer // optional; may also be set with SetObserver before Run
	// Transitions is told about every status transition; optional, may also be set with
	// SetTransitionObserver before Run.
	Transitions TransitionObserver
	Probe       ProbeFunc
	Log         *slog.Logger

	Tick             time.Duration // how often due devices are selected (controller.monitor.tick)
	Concurrency      int           // worker pool size (controller.monitor.concurrency)
	OfflineThreshold int           // consecutive failures before offline (controller.monitor.offline_threshold)

	Now func() time.Time
	// Jitter returns a duration in [0, max]; the monitor spreads each device's next probe by
	// ±10 % of its interval. Default: uniformly random.
	Jitter func(max time.Duration) time.Duration
}

// Monitor probes devices on their own schedule and keeps their stored status current.
//
//   - Each tick it asks the store for the enabled devices (plus devices with an active command),
//     picks those whose next probe time has come, and probes them through a bounded worker pool.
//   - A device with an active wake/shutdown command is probed on every tick, whatever its interval,
//     so the command completes within about one tick of the machine changing state.
//   - Results go through the hysteresis Tracker; status, last_checked_at, last_seen_at and
//     last_latency_ms are persisted, and a history row is appended only on a transition.
//   - ProbeNow runs one extra observation on demand (the probe endpoint and the wake pre-check).
type Monitor struct {
	cfg Config

	mu    sync.Mutex // guards state and every deviceState.next
	state map[string]*deviceState
}

// deviceState is the in-memory monitoring state of one device. It is rebuilt from the stored
// status after a restart.
type deviceState struct {
	probe   sync.Mutex // held while probing and recording: one probe per device at a time
	tracker *Tracker   // guarded by probe
	next    time.Time  // when the device is due again; zero = now. Guarded by Monitor.mu
}

// New returns a Monitor.
func New(cfg Config) *Monitor {
	if cfg.Probe == nil {
		cfg.Probe = Probe
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Jitter == nil {
		cfg.Jitter = func(max time.Duration) time.Duration {
			if max <= 0 {
				return 0
			}
			return time.Duration(rand.Int64N(int64(max) + 1))
		}
	}
	cfg.Concurrency = max(cfg.Concurrency, 1)
	cfg.Tick = max(cfg.Tick, time.Millisecond)
	return &Monitor{cfg: cfg, state: map[string]*deviceState{}}
}

// SetObserver sets the Observer. Call it during wiring, before Run (it breaks the construction
// cycle between the monitor and the commands engine).
func (m *Monitor) SetObserver(o Observer) { m.cfg.Observer = o }

// SetTransitionObserver sets the TransitionObserver. Call it during wiring, before Run.
func (m *Monitor) SetTransitionObserver(o TransitionObserver) { m.cfg.Transitions = o }

// Run checks due devices at once and then on every tick, until ctx is cancelled. Probes in flight
// are cancelled with ctx and not recorded.
func (m *Monitor) Run(ctx context.Context) error {
	m.CheckDue(ctx)
	ticker := time.NewTicker(m.cfg.Tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			m.CheckDue(ctx)
		}
	}
}

// CheckDue is one tick: probe every due device and wait for the probes to finish.
func (m *Monitor) CheckDue(ctx context.Context) {
	targets, err := m.cfg.Store.MonitorTargets(ctx)
	if err != nil {
		if ctx.Err() == nil {
			m.cfg.Log.Warn("monitor: list devices failed", "err", err)
		}
		return
	}
	due := m.selectDue(targets)

	sem := make(chan struct{}, m.cfg.Concurrency)
	var wg sync.WaitGroup
loop:
	for _, t := range due {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			break loop
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			m.checkTarget(ctx, t)
		}()
	}
	wg.Wait()
}

// selectDue forgets devices that are no longer targets and returns those whose time has come.
func (m *Monitor) selectDue(targets []devices.MonitorTarget) []devices.MonitorTarget {
	now := m.cfg.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	live := make(map[string]struct{}, len(targets))
	var due []devices.MonitorTarget
	for _, t := range targets {
		live[t.Device.ID] = struct{}{}
		st := m.stateLocked(t.Device)
		if t.Urgent || !now.Before(st.next) {
			due = append(due, t)
		}
	}
	for id := range m.state {
		if _, ok := live[id]; !ok {
			delete(m.state, id)
		}
	}
	return due
}

func (m *Monitor) stateLocked(d devices.Device) *deviceState {
	st, ok := m.state[d.ID]
	if !ok {
		st = &deviceState{tracker: NewTrackerFrom(m.cfg.OfflineThreshold, d.Status)}
		m.state[d.ID] = st
	}
	return st
}

func (m *Monitor) stateFor(d devices.Device) *deviceState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stateLocked(d)
}

func (m *Monitor) forget(id string) {
	m.mu.Lock()
	delete(m.state, id)
	m.mu.Unlock()
}

// checkTarget probes one device from the tick; it skips a device already being probed.
func (m *Monitor) checkTarget(ctx context.Context, t devices.MonitorTarget) {
	st := m.stateFor(t.Device)
	if !st.probe.TryLock() {
		return
	}
	defer st.probe.Unlock()
	_, _ = m.observe(ctx, st, t.Device, "probe", t.Urgent)
}

// ProbeOutcome is the result of a manual probe: what happened now and the status after applying
// hysteresis (one failed probe of an online device leaves it online until the threshold is met).
type ProbeOutcome struct {
	Result ProbeResult
	Status devices.Status
}

// ProbeNow probes a device right away, whether or not it is enabled, and records the result like
// any other observation: it feeds the hysteresis tracker, updates last_checked_at, last_seen_at and
// last_latency_ms, may append a status transition (reason "manual probe") and notifies the
// Observer. devices.ErrNotFound if the device does not exist.
func (m *Monitor) ProbeNow(ctx context.Context, deviceID string) (ProbeOutcome, error) {
	dev, err := m.cfg.Store.Get(ctx, deviceID)
	if err != nil {
		return ProbeOutcome{}, err
	}
	st := m.stateFor(dev)
	st.probe.Lock() // wait for a probe of the same device already in flight
	defer st.probe.Unlock()
	// Re-read: the device may have changed (or vanished) while we waited.
	if dev, err = m.cfg.Store.Get(ctx, deviceID); err != nil {
		return ProbeOutcome{}, err
	}
	return m.observe(ctx, st, dev, "manual probe", true)
}

// observe runs one probe and records it. The caller holds st.probe.
func (m *Monitor) observe(ctx context.Context, st *deviceState, dev devices.Device, reason string, notify bool) (ProbeOutcome, error) {
	res := m.cfg.Probe(ctx, dev.ProbeHost(), dev.MonitorPort, dev.MonitorTimeout)
	if err := ctx.Err(); err != nil { // shutting down: the probe was cut short, record nothing
		return ProbeOutcome{}, err
	}
	at := m.cfg.Now().Truncate(time.Millisecond) // the database keeps milliseconds
	res.CheckedAt = at

	saved := *st.tracker
	prev := st.tracker.Status()
	status, changed := st.tracker.Observe(res.Reachable)
	err := m.cfg.Store.RecordObservation(ctx, devices.Observation{
		DeviceID: dev.ID, At: at, Reachable: res.Reachable, Latency: res.Latency,
		Status: status, Previous: prev, Changed: changed, Reason: reason,
	})
	if err != nil {
		*st.tracker = saved // not persisted: do not let the tracker run ahead of the database
		if errors.Is(err, devices.ErrNotFound) {
			m.forget(dev.ID)
		} else if ctx.Err() == nil {
			m.cfg.Log.Warn("monitor: record observation failed", "device", dev.ID, "err", err)
		}
		return ProbeOutcome{}, err
	}

	interval := dev.MonitorInterval
	m.mu.Lock()
	st.next = at.Add(interval - interval/10 + m.cfg.Jitter(interval/5))
	m.mu.Unlock()

	if changed {
		m.cfg.Log.Info("device status changed", "device", dev.ID, "name", dev.Name, "from", prev, "to", status)
	}
	if changed && m.cfg.Transitions != nil {
		m.cfg.Transitions.DeviceTransitioned(ctx, Transition{Device: dev, From: prev, To: status, At: at, Reason: reason})
	}
	if m.cfg.Observer != nil && (changed || notify) {
		m.cfg.Observer.DeviceObserved(ctx, dev.ID, status, at)
	}
	return ProbeOutcome{Result: res, Status: status}, nil
}
