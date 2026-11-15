package commands

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/thanhtunguet/homecore/internal/devices"
	"github.com/thanhtunguet/homecore/internal/page"
)

// Controller-defined error codes of failed commands (agent error codes are passed through by the
// shutdown dispatcher).
const (
	ErrCodeWOLSend     = "wol_send_failed"
	ErrCodeInterrupted = "interrupted"
	ErrCodeInternal    = "internal"
)

// Default timings; the deadlines come from configuration.
const (
	DefaultSweepInterval   = 5 * time.Second
	DefaultDispatchTimeout = 10 * time.Second
)

// Result is what a Dispatcher reports for one command.
type Result struct {
	// Status is StatusSent, StatusAcknowledged (shutdown only: the agent already answered) or
	// StatusFailed. Anything else is treated as a failure.
	Status    Status
	ErrorCode string // for StatusFailed
	Error     string // for StatusFailed
	Attempts  int    // dispatch attempts made (at least 1)
}

// Dispatcher delivers one type of command. Wake sends a magic packet (WakeDispatcher); Phase 3
// registers the signed agent request for shutdown. Implementations must be safe for concurrent
// use and must not block longer than their context allows.
type Dispatcher interface {
	// Precheck rejects a request that can never work for this device (no MAC, no agent binding).
	// It must return an error matching ErrPrecondition (use *PreconditionError).
	Precheck(d devices.Device) error
	// Dispatch performs the one-shot delivery of a freshly created, still pending command.
	// Retrying the same command (after a restart) must be safe: it carries the same id.
	Dispatch(ctx context.Context, c Command, d devices.Device) Result
}

// DeviceSource looks devices up (implemented by *devices.Service).
type DeviceSource interface {
	Get(ctx context.Context, id string) (devices.Device, error)
}

// Prober makes one fresh reachability check of a device (implemented by the monitor through a
// small adapter in app). It is optional: without it a stored status is trusted.
type Prober interface {
	Probe(ctx context.Context, deviceID string) (reachable bool, err error)
}

// IssueObserver is told about every command the Engine created and dispatched, whoever asked
// (REST, the legacy endpoints, MCP): the command is in its post-dispatch state (sent, acknowledged
// or failed). It is not called for a request answered with an already active command, nor for a
// command born completed because the device was already in the target state. It runs on the
// request's goroutine with the request's context (so the caller's principal is available) and must
// be quick: queue and return. Alerts use it (REQ-ALERT-003).
type IssueObserver interface {
	CommandIssued(ctx context.Context, c Command, d devices.Device)
}

// Config configures an Engine.
type Config struct {
	Repo    Repository
	Devices DeviceSource
	Prober  Prober        // optional
	Issued  IssueObserver // optional
	Log     *slog.Logger

	WakeDeadline     time.Duration
	ShutdownDeadline time.Duration

	SweepInterval   time.Duration // default DefaultSweepInterval
	DispatchTimeout time.Duration // default DefaultDispatchTimeout
	Now             func() time.Time
}

// Engine runs the command lifecycle for every type (architecture §8.2): it creates commands
// (idempotently, ADR-012), hands them to the type's Dispatcher, completes them when the monitor
// reports the target status, and times them out at their deadline.
type Engine struct {
	cfg         Config
	dispatchers map[Type]Dispatcher
}

// NewEngine returns an Engine. Register dispatchers before calling Request or Run.
func NewEngine(cfg Config) *Engine {
	if cfg.SweepInterval <= 0 {
		cfg.SweepInterval = DefaultSweepInterval
	}
	if cfg.DispatchTimeout <= 0 {
		cfg.DispatchTimeout = DefaultDispatchTimeout
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &Engine{cfg: cfg, dispatchers: map[Type]Dispatcher{}}
}

// Register sets the Dispatcher of a command type. Call it during wiring, before Run.
func (e *Engine) Register(t Type, d Dispatcher) { e.dispatchers[t] = d }

func (e *Engine) deadline(t Type) time.Duration {
	if t == TypeShutdown {
		return e.cfg.ShutdownDeadline
	}
	return e.cfg.WakeDeadline
}

// targetStatus is the device status a command type is waiting for.
func targetStatus(t Type) devices.Status {
	if t == TypeShutdown {
		return devices.StatusOffline
	}
	return devices.StatusOnline
}

// Request is a user's wish to wake or shut down a device.
type Request struct {
	DeviceID    string
	Type        Type
	RequestedBy string // user id from the authenticated principal
}

// Request creates (or returns) the command for r and dispatches it. The returned command is in its
// post-dispatch state: sent (or acknowledged), failed, or completed when the device was already in
// the target state. It never waits for the machine itself.
//
// Idempotent (ADR-012): an active command of the same type is returned unchanged; a device already
// in the target state gets a command that is created completed, without a packet or agent request.
// Errors: devices.ErrNotFound, ErrPrecondition (*PreconditionError), ErrNoDispatcher.
func (e *Engine) Request(ctx context.Context, r Request) (Command, error) {
	disp, ok := e.dispatchers[r.Type]
	if !ok {
		return Command{}, ErrNoDispatcher
	}
	dev, err := e.cfg.Devices.Get(ctx, r.DeviceID)
	if err != nil {
		return Command{}, err
	}
	if err := disp.Precheck(dev); err != nil {
		return Command{}, err
	}
	if c, err := e.cfg.Repo.Active(ctx, dev.ID, r.Type); err == nil {
		return c, nil
	} else if !errors.Is(err, ErrNotFound) {
		return Command{}, err
	}

	now := e.cfg.Now().UTC().Truncate(time.Millisecond) // the database keeps milliseconds
	id, err := uuid.NewV7()
	if err != nil {
		return Command{}, fmt.Errorf("new command id: %w", err)
	}
	c := Command{
		ID: id.String(), DeviceID: dev.ID, Type: r.Type, Status: StatusPending, RequestedBy: r.RequestedBy,
		CreatedAt: now, UpdatedAt: now,
	}
	if dev.Status == targetStatus(r.Type) && e.confirmTarget(ctx, dev, r.Type) {
		// Already there: nothing to send. The command is born completed.
		c.Status, c.CompletedAt = StatusCompleted, &now
		if err := e.cfg.Repo.Insert(ctx, c); err != nil {
			return Command{}, err
		}
		return c, nil
	}
	dl := now.Add(e.deadline(r.Type))
	c.DeadlineAt = &dl
	if err := e.cfg.Repo.Insert(ctx, c); err != nil {
		if errors.Is(err, ErrActiveExists) { // lost a race: the database says who won
			return e.cfg.Repo.Active(ctx, dev.ID, r.Type)
		}
		return Command{}, err
	}
	out, err := e.dispatch(ctx, disp, c, dev)
	if err == nil && e.cfg.Issued != nil {
		e.cfg.Issued.CommandIssued(ctx, out, dev)
	}
	return out, err
}

// confirmTarget double-checks a stored "already in the target state" with one fresh probe, so a
// stale status does not make a wake request a no-op. Without a Prober the stored status stands.
func (e *Engine) confirmTarget(ctx context.Context, dev devices.Device, t Type) bool {
	if e.cfg.Prober == nil {
		return true
	}
	reachable, err := e.cfg.Prober.Probe(ctx, dev.ID)
	if err != nil {
		return false
	}
	return reachable == (targetStatus(t) == devices.StatusOnline)
}

// dispatch runs the Dispatcher for a pending command and records the outcome. The dispatch is not
// tied to the caller's cancellation, so a client that hangs up cannot leave a command half-done.
func (e *Engine) dispatch(ctx context.Context, disp Dispatcher, c Command, dev devices.Device) (Command, error) {
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), e.cfg.DispatchTimeout)
	defer cancel()
	res := disp.Dispatch(dctx, c, dev)
	now := e.cfg.Now().UTC().Truncate(time.Millisecond) // the database keeps milliseconds
	attempts := max(res.Attempts, 1)

	switch res.Status {
	case StatusSent, StatusAcknowledged:
		if _, err := e.advance(ctx, c, StatusPending, Change{To: StatusSent, Attempts: attempts, At: now}); err != nil {
			return Command{}, err
		}
		if res.Status == StatusAcknowledged {
			if _, err := e.advance(ctx, c, StatusSent, Change{To: StatusAcknowledged, At: now}); err != nil {
				return Command{}, err
			}
		}
	default:
		code, msg := res.ErrorCode, res.Error
		if res.Status != StatusFailed {
			code, msg = ErrCodeInternal, fmt.Sprintf("dispatcher returned status %q", res.Status)
		}
		if _, err := e.advance(ctx, c, StatusPending, Change{To: StatusFailed, ErrorCode: code, Error: msg, Attempts: attempts, At: now}); err != nil {
			return Command{}, err
		}
		e.cfg.Log.Warn("command dispatch failed", "command", c.ID, "type", c.Type, "device", c.DeviceID, "code", code)
	}
	return e.cfg.Repo.Get(ctx, c.ID)
}

// advance validates the transition and applies it compare-and-set.
func (e *Engine) advance(ctx context.Context, c Command, from Status, ch Change) (bool, error) {
	if err := Transition(c.Type, from, ch.To); err != nil {
		return false, err
	}
	return e.cfg.Repo.Update(ctx, c.ID, from, ch)
}

// Get returns one command. ErrNotFound if it does not exist.
func (e *Engine) Get(ctx context.Context, id string) (Command, error) {
	return e.cfg.Repo.Get(ctx, id)
}

// ListParams selects a page of the command history.
type ListParams struct {
	Limit    int    // 0 = default
	Cursor   string // opaque, from a previous page
	DeviceID string // optional filter (UUID)
	Status   Status // optional filter
}

// Page is one page of commands, newest first.
type Page struct {
	Items      []Command
	NextCursor string
}

type cursor struct {
	T int64  `json:"t"` // created_at (unix ms) of the last item
	I string `json:"i"` // id of the last item
}

// List returns a page of commands, newest first. page.ErrBadLimit, page.ErrBadCursor and
// ErrInvalid describe a bad request.
func (e *Engine) List(ctx context.Context, p ListParams) (Page, error) {
	limit, err := page.Normalize(p.Limit)
	if err != nil {
		return Page{}, err
	}
	if p.Status != "" && !p.Status.Valid() {
		return Page{}, fmt.Errorf("%w: unknown status %q", ErrInvalid, p.Status)
	}
	var after *ListAfter
	if p.Cursor != "" {
		var c cursor
		if err := page.Decode(p.Cursor, &c); err != nil || c.I == "" {
			return Page{}, page.ErrBadCursor
		}
		after = &ListAfter{CreatedAt: time.UnixMilli(c.T).UTC(), ID: c.I}
	}
	items, err := e.cfg.Repo.List(ctx, ListFilter{DeviceID: p.DeviceID, Status: p.Status}, after, limit+1)
	if err != nil {
		return Page{}, err
	}
	out := Page{Items: items}
	if len(items) > limit {
		out.Items = items[:limit]
		last := out.Items[limit-1]
		out.NextCursor = page.Encode(cursor{T: last.CreatedAt.UnixMilli(), I: last.ID})
	}
	return out, nil
}

// PowerTransitions derives power_transition for the given devices from their active commands. A
// device without one is absent from the map (callers use TransitionNone). If a wake and a shutdown
// are both active, the newer one wins.
func (e *Engine) PowerTransitions(ctx context.Context, deviceIDs []string) (map[string]devices.PowerTransition, error) {
	refs, err := e.cfg.Repo.ActiveForDevices(ctx, deviceIDs)
	if err != nil {
		return nil, err
	}
	newest := map[string]ActiveRef{}
	for _, r := range refs {
		if cur, ok := newest[r.DeviceID]; !ok || r.CreatedAt.After(cur.CreatedAt) {
			newest[r.DeviceID] = r
		}
	}
	out := make(map[string]devices.PowerTransition, len(newest))
	for id, r := range newest {
		if r.Type == TypeShutdown {
			out[id] = devices.TransitionShuttingDown
		} else {
			out[id] = devices.TransitionWaking
		}
	}
	return out, nil
}

// DeviceObserved is the monitor's hook (monitoring.Observer): it completes the active command that
// was waiting for this status (wake: online, shutdown: offline). It is cheap when no command is
// active and idempotent. A command still `pending` is left alone: the next observation (one tick
// later while a command is active) completes it.
func (e *Engine) DeviceObserved(ctx context.Context, deviceID string, status devices.Status, at time.Time) {
	for _, t := range []Type{TypeWake, TypeShutdown} {
		if status != targetStatus(t) {
			continue
		}
		c, err := e.cfg.Repo.Active(ctx, deviceID, t)
		if err != nil {
			if !errors.Is(err, ErrNotFound) && ctx.Err() == nil {
				e.cfg.Log.Warn("command lookup failed", "device", deviceID, "err", err)
			}
			continue
		}
		if !CanTransition(t, c.Status, StatusCompleted) {
			continue
		}
		ok, err := e.advance(ctx, c, c.Status, Change{To: StatusCompleted, At: at})
		if err != nil {
			e.cfg.Log.Warn("complete command failed", "command", c.ID, "err", err)
		} else if ok {
			e.cfg.Log.Info("command completed", "command", c.ID, "type", t, "device", deviceID)
		}
	}
}

// Sweep applies the deadlines once: sent/acknowledged commands past deadline_at become timed_out.
func (e *Engine) Sweep(ctx context.Context) error {
	n, err := e.cfg.Repo.Expire(ctx, e.cfg.Now().UTC())
	if err != nil {
		return err
	}
	if n > 0 {
		e.cfg.Log.Info("commands expired", "count", n)
	}
	return nil
}

// Recover runs once at startup. Active commands past their deadline are expired; commands still
// `pending` (the Controller stopped between creating and dispatching them) are dispatched again with
// the same id; the others simply continue: the monitor completes them and Sweep times them out.
func (e *Engine) Recover(ctx context.Context) error {
	if err := e.Sweep(ctx); err != nil {
		return err
	}
	pending, err := e.cfg.Repo.Pending(ctx)
	if err != nil {
		return err
	}
	for _, c := range pending {
		disp, ok := e.dispatchers[c.Type]
		if !ok {
			continue
		}
		dev, err := e.cfg.Devices.Get(ctx, c.DeviceID)
		if err != nil {
			continue // the device is gone; the command expires with its deadline
		}
		if _, err := e.dispatch(ctx, disp, c, dev); err != nil {
			e.cfg.Log.Warn("re-dispatch after restart failed", "command", c.ID, "err", err)
		}
	}
	return nil
}

// Run recovers once, then applies the deadlines every SweepInterval until ctx is cancelled.
func (e *Engine) Run(ctx context.Context) error {
	if err := e.Recover(ctx); err != nil && ctx.Err() == nil {
		e.cfg.Log.Error("command recovery failed", "err", err)
	}
	ticker := time.NewTicker(e.cfg.SweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := e.Sweep(ctx); err != nil && ctx.Err() == nil {
				e.cfg.Log.Warn("command sweep failed", "err", err)
			}
		}
	}
}
