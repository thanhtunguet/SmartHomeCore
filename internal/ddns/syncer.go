package ddns

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/thanhtunguet/homecore/internal/audit"
)

// Syncer defaults.
const (
	DefaultTick        = 5 * time.Second
	DefaultConcurrency = 4
	BackoffBase        = time.Second
	BackoffMax         = 5 * time.Minute
)

// BackoffDelay is the wait after the attempt-th consecutive transient failure (1-based): 1 s, 2 s,
// 4 s ... capped at 5 min, scaled by jitter in [0.5, 1] so that configurations that failed together
// do not retry together. jitter is a number in [0, 1).
func BackoffDelay(attempt int, jitter float64) time.Duration {
	d := BackoffMax
	if attempt >= 1 && attempt <= 20 {
		d = min(BackoffBase<<(attempt-1), BackoffMax)
	}
	return time.Duration(float64(d) * (0.5 + 0.5*jitter))
}

// SyncerConfig configures a Syncer.
type SyncerConfig struct {
	Service     *Service
	Tick        time.Duration    // how often due configurations are looked for; 0 = DefaultTick
	Concurrency int              // syncs in flight at once; 0 = DefaultConcurrency
	Now         func() time.Time // nil = time.Now
	Jitter      func() float64   // in [0, 1); nil = math/rand/v2
	Audit       *audit.Writer    // optional: a record update by the scheduler is audited as ddns.sync
	Log         *slog.Logger     // nil = slog.Default()
}

// Syncer is the periodic job: it syncs each enabled configuration every `interval_seconds`. One
// configuration's failures, backoff or slowness never delay another: every sync runs in its own
// goroutine (bounded by Concurrency) and each configuration keeps its own schedule.
type Syncer struct {
	svc    *Service
	tick   time.Duration
	now    func() time.Time
	jitter func() float64
	audit  *audit.Writer
	log    *slog.Logger
	sem    chan struct{}
	wg     sync.WaitGroup

	mu      sync.Mutex
	entries map[string]*entry
}

// entry is the in-memory schedule of one configuration. Nothing here is persisted: after a restart
// every enabled configuration is due immediately.
type entry struct {
	version time.Time // UpdatedAt the schedule was computed for; an edit makes the config due at once
	next    time.Time
	fails   int // consecutive transient failures
	running bool
}

// NewSyncer returns a Syncer.
func NewSyncer(c SyncerConfig) *Syncer {
	s := &Syncer{svc: c.Service, tick: c.Tick, now: c.Now, jitter: c.Jitter, audit: c.Audit, log: c.Log,
		entries: map[string]*entry{}}
	if s.tick <= 0 {
		s.tick = DefaultTick
	}
	if c.Concurrency <= 0 {
		c.Concurrency = DefaultConcurrency
	}
	s.sem = make(chan struct{}, c.Concurrency)
	if s.now == nil {
		s.now = time.Now
	}
	if s.jitter == nil {
		s.jitter = rand.Float64
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	return s
}

// Run schedules syncs until ctx is cancelled, then waits for those in flight (their context is
// cancelled too, so this is quick). It always returns nil.
func (s *Syncer) Run(ctx context.Context) error {
	defer s.wg.Wait()
	t := time.NewTicker(s.tick)
	defer t.Stop()
	s.dispatch(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			s.dispatch(ctx)
		}
	}
}

// dispatch starts a goroutine for every enabled configuration that is due and idle.
func (s *Syncer) dispatch(ctx context.Context) {
	cfgs, err := s.svc.List(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn("ddns: cannot list configurations", "err", err)
		}
		return
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := make(map[string]bool, len(cfgs))
	for _, c := range cfgs {
		seen[c.ID] = true
		e := s.entries[c.ID]
		if e == nil {
			e = &entry{}
			s.entries[c.ID] = e
		}
		if !c.UpdatedAt.Equal(e.version) { // new, or edited: apply the change now, forget old failures
			e.version, e.next, e.fails = c.UpdatedAt, time.Time{}, 0
		}
		if !c.Enabled || e.running || now.Before(e.next) || ctx.Err() != nil {
			continue
		}
		select {
		case s.sem <- struct{}{}:
		default:
			continue // all workers busy: try again next tick
		}
		e.running = true
		s.wg.Add(1)
		go s.runOne(ctx, c, e)
	}
	for id, e := range s.entries {
		if !seen[id] && !e.running {
			delete(s.entries, id)
		}
	}
}

func (s *Syncer) runOne(ctx context.Context, c Config, e *entry) {
	defer s.wg.Done()
	defer func() { <-s.sem }()
	defer func() {
		if p := recover(); p != nil {
			s.log.Error("ddns: sync panicked", "config_id", c.ID, "panic", p)
			s.finish(e, c, SyncResult{}, errors.New("panic"))
		}
	}()
	res, err := s.svc.Sync(ctx, c.ID)
	s.finish(e, c, res, err)
	if err == nil {
		s.report(ctx, c, res)
	}
}

// finish computes the next run of a configuration from the outcome of a sync.
func (s *Syncer) finish(e *entry, c Config, res SyncResult, err error) {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	e.running = false
	switch {
	case err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)):
		// shutting down; nothing to schedule
	case errors.Is(err, ErrNotFound):
		// deleted while syncing; dispatch drops the entry
	case err != nil: // storage failure: back off like a transient error
		e.fails++
		e.next = now.Add(min(BackoffDelay(e.fails, s.jitter()), c.Interval))
	case res.Result != ResultFailed:
		e.fails, e.next = 0, now.Add(c.Interval)
	case !IsTransient(res.Cause): // bad token, record mismatch: wait for the next normal interval
		e.fails, e.next = 0, now.Add(c.Interval)
	default:
		e.fails++
		delay := min(BackoffDelay(e.fails, s.jitter()), c.Interval)
		var cf *CloudflareError
		if errors.As(res.Cause, &cf) && cf.RateLimited() {
			delay = max(BackoffDelay(e.fails, s.jitter()), cf.RetryAfter) // Cloudflare asked us to wait
		}
		e.next = now.Add(delay)
	}
}

// report logs the outcome and audits a record update made by the scheduler.
func (s *Syncer) report(ctx context.Context, c Config, res SyncResult) {
	log := s.log.With("config_id", c.ID, "name", c.Name, "record", c.RecordName)
	switch res.Result {
	case ResultUpdated:
		log.Info("ddns: record updated", "ip", res.IP)
		if s.audit != nil {
			err := s.audit.Write(ctx, audit.Entry{Actor: "system", Action: "ddns.sync", TargetType: "ddns_config",
				TargetID: c.ID, Result: audit.ResultOK, Details: map[string]any{
					"name": c.Name, "record_name": c.RecordName, "ip": res.IP, "sync_result": string(ResultUpdated), "trigger": "scheduler"}})
			if err != nil && ctx.Err() == nil {
				log.Warn("audit write failed", "action", "ddns.sync", "err", err)
			}
		}
	case ResultFailed:
		log.Warn("ddns: sync failed", "err", res.Error, "transient", IsTransient(res.Cause))
	default:
		log.Debug("ddns: record already up to date", "ip", res.IP)
	}
}
