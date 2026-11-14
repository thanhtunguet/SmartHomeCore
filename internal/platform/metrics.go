package platform

import (
	"context"
	"slices"
	"time"
)

// Metric names used in Metrics.Restricted and in the API's `restricted` list.
const (
	MetricCPUPercent    = "cpu_percent"
	MetricMemory        = "memory"
	MetricDisk          = "disk"
	MetricUptime        = "uptime_seconds"
	MetricLoadAverage   = "load_average"
	MetricKernel        = "kernel"
	MetricLocalIPs      = "local_ips"
	MetricProcessRSS    = "process.rss_bytes"
	MetricProcessUptime = "process.uptime_seconds"
)

// Metrics is a snapshot of the host running HomeCore. Zero values mean "not available", except
// CPUPercent, where 0 % is a valid reading and absence is nil. Restricted names the metrics the
// platform cannot provide (for example everything under /proc on Android), so clients can explain
// why they are missing instead of guessing.
type Metrics struct {
	CollectedAt time.Time
	Platform    string // GOOS/GOARCH
	Hostname    string
	Kernel      string

	UptimeSeconds int64
	CPUPercent    *float64  // utilisation of all cores over the last sampling window, 0..100
	CPUCores      int       // logical CPUs
	LoadAverage   []float64 // 1, 5 and 15 minutes; nil where the OS has none

	MemTotal     uint64
	MemAvailable uint64
	MemUsed      uint64

	DiskPath  string // the file system holding the data directory
	DiskTotal uint64
	DiskFree  uint64
	DiskUsed  uint64

	LocalIPs []string

	PID                  int
	ProcessUptimeSeconds int64
	ProcessRSS           uint64
	Goroutines           int
	HeapAllocBytes       uint64

	Restricted []string
}

// Restrict records that the platform cannot provide the named metric (once).
func (m *Metrics) Restrict(name string) {
	if !slices.Contains(m.Restricted, name) {
		m.Restricted = append(m.Restricted, name)
	}
}

// MetricsProvider collects host metrics. Linux/macOS/Windows: gopsutil-based (internal/system).
// Android: supplied by the Kotlin host through the mobile bindings (Phase 5), because the app
// sandbox hides most of /proc.
type MetricsProvider interface {
	Collect(ctx context.Context) (Metrics, error)
}
