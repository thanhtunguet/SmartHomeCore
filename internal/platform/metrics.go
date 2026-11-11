package platform

import (
	"context"
	"time"
)

// Metrics is a snapshot of the host running HomeCore. Zero values mean "not available".
type Metrics struct {
	CollectedAt    time.Time
	Platform       string
	Hostname       string
	UptimeSeconds  int64
	CPUPercent     float64
	MemTotal       uint64
	MemAvailable   uint64
	MemUsed        uint64
	DiskPath       string
	DiskTotal      uint64
	DiskFree       uint64
	LocalIPs       []string
	ProcessRSS     uint64
	Goroutines     int
	HeapAllocBytes uint64
}

// MetricsProvider collects host metrics. Linux/macOS: gopsutil-based (Phase 4). Android: supplied
// by the Kotlin host through the mobile bindings, because the app sandbox hides most of /proc.
type MetricsProvider interface {
	Collect(ctx context.Context) (Metrics, error)
}
