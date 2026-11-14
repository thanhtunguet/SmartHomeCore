// Package retention prunes old rows so the database stays small on SD cards and phones: terminal
// commands, device status history and DDNS sync history older than controller.retention_days
// (docs/design/database.md §5). Each table's SQL lives in its own domain package; this package only
// schedules the work and deletes in batches so no single statement holds the write lock for long.
package retention

import (
	"context"
	"log/slog"
	"time"
)

// Target is one prunable table. Prune deletes at most limit rows older than cutoff and returns how
// many it deleted; fewer than limit means nothing is left to delete.
type Target struct {
	Name  string
	Prune func(ctx context.Context, cutoff time.Time, limit int) (int64, error)
}

// Defaults.
const (
	DefaultInterval  = time.Hour
	DefaultBatchSize = 500
)

// Config configures a Job.
type Config struct {
	Targets   []Target
	Days      int // keep this many days; 0 or less disables pruning entirely
	Interval  time.Duration
	BatchSize int
	Now       func() time.Time
	Log       *slog.Logger
}

// Job prunes the targets once at startup and then every Interval.
type Job struct{ cfg Config }

// New returns a Job.
func New(cfg Config) *Job {
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultInterval
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = DefaultBatchSize
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &Job{cfg: cfg}
}

// Run prunes now and then every Interval until ctx is cancelled. With Days <= 0 it only waits.
func (j *Job) Run(ctx context.Context) error {
	if j.cfg.Days <= 0 {
		j.cfg.Log.Info("retention disabled (retention_days <= 0)")
		<-ctx.Done()
		return nil
	}
	j.RunOnce(ctx)
	ticker := time.NewTicker(j.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			j.RunOnce(ctx)
		}
	}
}

// RunOnce prunes every target once, in batches, and returns the number of rows deleted per target.
func (j *Job) RunOnce(ctx context.Context) map[string]int64 {
	cutoff := j.cfg.Now().Add(-time.Duration(j.cfg.Days) * 24 * time.Hour)
	deleted := map[string]int64{}
	for _, t := range j.cfg.Targets {
		for ctx.Err() == nil {
			n, err := t.Prune(ctx, cutoff, j.cfg.BatchSize)
			deleted[t.Name] += n
			if err != nil {
				if ctx.Err() == nil {
					j.cfg.Log.Warn("retention: prune failed", "table", t.Name, "err", err)
				}
				break
			}
			if n < int64(j.cfg.BatchSize) {
				break
			}
		}
		if deleted[t.Name] > 0 {
			j.cfg.Log.Info("retention: pruned", "table", t.Name, "rows", deleted[t.Name])
		}
	}
	return deleted
}
