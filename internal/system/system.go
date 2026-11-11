// Package system exposes metrics of the host running HomeCore (GET /api/v1/system/metrics) via
// platform.MetricsProvider.
//
// TODO(phase-4): gopsutil-based provider for Linux/macOS; runtime-only fallback; Android provider
// injected from the mobile bindings.
package system

import (
	"context"
	"runtime"
	"time"

	"github.com/thanhtunguet/homecore/internal/platform"
)

// RuntimeProvider reports only Go runtime/process metrics. Always available, used as fallback.
type RuntimeProvider struct{}

func (RuntimeProvider) Collect(context.Context) (platform.Metrics, error) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return platform.Metrics{
		CollectedAt:    time.Now().UTC(),
		Platform:       runtime.GOOS + "/" + runtime.GOARCH,
		Goroutines:     runtime.NumGoroutine(),
		HeapAllocBytes: ms.HeapAlloc,
	}, nil
}
