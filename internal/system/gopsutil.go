package system

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/process"

	"github.com/thanhtunguet/homecore/internal/platform"
)

// firstSampleWindow is how long the very first CPU reading waits to have two samples to compare.
// Later readings compare with the previous call, so they never block.
const firstSampleWindow = 250 * time.Millisecond

// Gopsutil collects host metrics with github.com/shirou/gopsutil/v4 on top of RuntimeProvider.
// Whatever gopsutil cannot read is listed in Metrics.Restricted; the call itself does not fail.
type Gopsutil struct {
	runtime RuntimeProvider
	dataDir string
	log     *slog.Logger

	mu       sync.Mutex // guards the CPU sampler
	cpuLast  cpu.TimesStat
	cpuHave  bool
	cpuPct   float64
	cpuKnown bool
}

var _ platform.MetricsProvider = (*Gopsutil)(nil)

// GopsutilConfig configures NewGopsutil.
type GopsutilConfig struct {
	DataDir string       // the disk metrics describe the file system holding it
	Started time.Time    // process start, fallback for process.uptime_seconds
	Log     *slog.Logger // nil = slog.Default()
}

// NewGopsutil returns a Gopsutil provider.
func NewGopsutil(c GopsutilConfig) *Gopsutil {
	g := &Gopsutil{runtime: RuntimeProvider{Started: c.Started}, dataDir: c.DataDir, log: c.Log}
	if g.log == nil {
		g.log = slog.Default()
	}
	if g.dataDir == "" {
		g.dataDir = "."
	}
	return g
}

// Collect implements platform.MetricsProvider.
func (g *Gopsutil) Collect(ctx context.Context) (platform.Metrics, error) {
	m, _ := g.runtime.Collect(ctx)
	m.Restricted = nil // the runtime base restricts everything gopsutil is about to fill
	if m.Hostname == "" {
		m.Restrict("hostname")
	}
	if len(m.LocalIPs) == 0 {
		m.Restrict(platform.MetricLocalIPs)
	}
	if m.ProcessUptimeSeconds == 0 && g.runtime.Started.IsZero() {
		m.Restrict(platform.MetricProcessUptime)
	}

	if info, err := host.InfoWithContext(ctx); err != nil {
		g.note("host", err)
		m.Restrict(platform.MetricUptime)
		m.Restrict(platform.MetricKernel)
	} else {
		m.UptimeSeconds = int64(info.Uptime)
		m.Kernel = info.KernelVersion
		if info.Hostname != "" {
			m.Hostname = info.Hostname
		}
		if m.UptimeSeconds == 0 {
			m.Restrict(platform.MetricUptime)
		}
		if m.Kernel == "" {
			m.Restrict(platform.MetricKernel)
		}
	}

	switch pct, st := g.cpuPercent(ctx); st {
	case cpuOK:
		m.CPUPercent = &pct
	case cpuUnavailable:
		m.Restrict(platform.MetricCPUPercent)
	}
	if n, err := cpu.CountsWithContext(ctx, true); err == nil && n > 0 {
		m.CPUCores = n
	}

	if vm, err := mem.VirtualMemoryWithContext(ctx); err != nil || vm.Total == 0 {
		g.note("memory", err)
		m.Restrict(platform.MetricMemory)
	} else {
		m.MemTotal, m.MemAvailable, m.MemUsed = vm.Total, vm.Available, vm.Used
	}

	if runtime.GOOS == "windows" { // gopsutil emulates a load average there; do not present it as one
		m.Restrict(platform.MetricLoadAverage)
	} else if avg, err := load.AvgWithContext(ctx); err != nil {
		g.note("load", err)
		m.Restrict(platform.MetricLoadAverage)
	} else {
		m.LoadAverage = []float64{avg.Load1, avg.Load5, avg.Load15}
	}

	path := g.dataDir
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if du, err := disk.UsageWithContext(ctx, path); err != nil || du.Total == 0 {
		g.note("disk", err)
		m.Restrict(platform.MetricDisk)
	} else {
		m.DiskPath, m.DiskTotal, m.DiskFree, m.DiskUsed = path, du.Total, du.Free, du.Used
	}

	g.process(ctx, &m)
	return m, nil
}

// process fills RSS and the process uptime from the OS (the runtime cannot see its own RSS).
func (g *Gopsutil) process(ctx context.Context, m *platform.Metrics) {
	p, err := process.NewProcessWithContext(ctx, int32(os.Getpid()))
	if err != nil {
		g.note("process", err)
		m.Restrict(platform.MetricProcessRSS)
		return
	}
	if mi, err := p.MemoryInfoWithContext(ctx); err != nil || mi.RSS == 0 {
		g.note("process rss", err)
		m.Restrict(platform.MetricProcessRSS)
	} else {
		m.ProcessRSS = mi.RSS
	}
	if ct, err := p.CreateTimeWithContext(ctx); err == nil && ct > 0 {
		if up := time.Since(time.UnixMilli(ct)); up > 0 {
			m.ProcessUptimeSeconds = int64(up.Seconds())
			m.Restricted = removeName(m.Restricted, platform.MetricProcessUptime)
		}
	}
}

func removeName(list []string, name string) []string {
	out := list[:0]
	for _, s := range list {
		if s != name {
			out = append(out, s)
		}
	}
	return out
}

// note logs a gopsutil failure at debug level: on a platform that never has the metric it would be
// noise on every request.
func (g *Gopsutil) note(what string, err error) {
	if err != nil {
		g.log.Debug("metrics: gopsutil call failed", "what", what, "err", err)
	}
}

type cpuStatus int

const (
	cpuOK          cpuStatus = iota // a reading exists
	cpuWarming                      // the platform can provide it but no reading exists yet: absent, not restricted
	cpuUnavailable                  // the OS gives no CPU times: restricted
)

// cpuPercent is the utilisation between the previous reading and this one. The very first call has
// nothing to compare with and waits up to 3 x firstSampleWindow for the OS counters to move (some
// kernels update them in coarse steps); after that nothing blocks: the window is whatever passed
// since the last reading that saw the counters move (the Cached wrapper keeps it >= TTL), and a
// call that sees no movement keeps the previous value and the old baseline.
func (g *Gopsutil) cpuPercent(ctx context.Context) (float64, cpuStatus) {
	g.mu.Lock()
	defer g.mu.Unlock()
	status := func() cpuStatus {
		if g.cpuKnown {
			return cpuOK
		}
		return cpuWarming
	}
	cur, err := cpuTimes(ctx)
	if err != nil {
		g.note("cpu", err)
		if g.cpuKnown {
			return g.cpuPct, cpuOK
		}
		return 0, cpuUnavailable
	}
	if !g.cpuHave {
		g.cpuLast, g.cpuHave = cur, true
		for range 3 {
			select {
			case <-time.After(firstSampleWindow):
			case <-ctx.Done():
				return 0, cpuWarming
			}
			if cur, err = cpuTimes(ctx); err != nil {
				g.note("cpu", err)
				return 0, cpuUnavailable
			}
			if pct, ok := busyPercent(g.cpuLast, cur); ok {
				g.cpuPct, g.cpuKnown, g.cpuLast = pct, true, cur
				break
			}
		}
		return g.cpuPct, status()
	}
	if pct, ok := busyPercent(g.cpuLast, cur); ok {
		g.cpuPct, g.cpuKnown, g.cpuLast = pct, true, cur
	}
	return g.cpuPct, status()
}

func cpuTimes(ctx context.Context) (cpu.TimesStat, error) {
	ts, err := cpu.TimesWithContext(ctx, false)
	if err != nil {
		return cpu.TimesStat{}, err
	}
	if len(ts) == 0 {
		return cpu.TimesStat{}, os.ErrNotExist
	}
	return ts[0], nil
}

// busyPercent is the share of non-idle time between two cumulative snapshots, clamped to 0..100.
// ok is false when no time passed between them.
func busyPercent(a, b cpu.TimesStat) (float64, bool) {
	total := b.Total() - a.Total()
	if total <= 0 {
		return 0, false
	}
	idle := (b.Idle + b.Iowait) - (a.Idle + a.Iowait)
	pct := (total - idle) / total * 100
	return min(max(pct, 0), 100), true
}
