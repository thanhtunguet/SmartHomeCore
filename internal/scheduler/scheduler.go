// Package scheduler runs periodic background jobs. It is deliberately tiny: no cron syntax, no
// persistence. Jobs re-derive their work from the database on every run.
package scheduler

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"time"
)

// Job is one unit of periodic work. Returning an error only logs it; the schedule continues.
type Job func(ctx context.Context) error

// Every runs job every interval (± jitter) until ctx is cancelled. The first run happens after
// a random delay in [0, jitter] so that several jobs do not fire together at startup.
// Panics inside job are recovered and logged.
func Every(ctx context.Context, log *slog.Logger, name string, interval, jitter time.Duration, job Job) {
	if interval <= 0 {
		panic("scheduler: interval must be > 0")
	}
	next := randDur(jitter)
	for {
		timer := time.NewTimer(next)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		run(ctx, log, name, job)
		next = max(interval+randDur(2*jitter)-jitter, time.Second)
	}
}

func run(ctx context.Context, log *slog.Logger, name string, job Job) {
	defer func() {
		if p := recover(); p != nil {
			log.Error("scheduled job panicked", "job", name, "panic", p)
		}
	}()
	start := time.Now()
	if err := job(ctx); err != nil && ctx.Err() == nil {
		log.Warn("scheduled job failed", "job", name, "err", err, "took", time.Since(start))
	}
}

func randDur(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(max) + 1))
}
