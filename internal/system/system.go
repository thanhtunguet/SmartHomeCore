// Package system exposes metrics of the host running HomeCore (GET /api/v1/system/metrics) via
// platform.MetricsProvider.
//
// Two providers: Gopsutil (Linux, macOS, Windows; CPU, memory, disk, load, uptime, kernel, process
// RSS) and RuntimeProvider (what the Go runtime and the standard library know). Gopsutil is built
// on RuntimeProvider, so when a gopsutil call fails the corresponding metric is simply reported as
// restricted and the rest of the snapshot is still delivered. The Android provider is injected by
// the mobile bindings (Phase 5).
package system

import (
	"context"
	"net"
	"net/netip"
	"os"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/thanhtunguet/homecore/internal/platform"
)

// RuntimeProvider reports Go runtime and standard-library metrics only. Always available, used as
// the base of the other providers and as the fallback.
type RuntimeProvider struct {
	// Started is when this process started, for process.uptime_seconds; zero = unknown (restricted).
	Started time.Time
	// Now is the clock; nil = time.Now.
	Now func() time.Time
}

var _ platform.MetricsProvider = RuntimeProvider{}

// Collect implements platform.MetricsProvider. It never fails.
func (p RuntimeProvider) Collect(context.Context) (platform.Metrics, error) {
	now := time.Now
	if p.Now != nil {
		now = p.Now
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	m := platform.Metrics{
		CollectedAt:    now().UTC(),
		Platform:       runtime.GOOS + "/" + runtime.GOARCH,
		CPUCores:       runtime.NumCPU(),
		PID:            os.Getpid(),
		Goroutines:     runtime.NumGoroutine(),
		HeapAllocBytes: ms.HeapAlloc,
	}
	if h, err := os.Hostname(); err == nil {
		m.Hostname = h
	}
	if !p.Started.IsZero() {
		m.ProcessUptimeSeconds = int64(now().Sub(p.Started).Seconds())
	} else {
		m.Restrict(platform.MetricProcessUptime)
	}
	if ips := LocalIPs(); len(ips) > 0 {
		m.LocalIPs = ips
	} else {
		m.Restrict(platform.MetricLocalIPs)
	}
	// Beyond the runtime's own view, the standard library knows nothing about the machine.
	for _, name := range []string{platform.MetricCPUPercent, platform.MetricMemory, platform.MetricDisk,
		platform.MetricUptime, platform.MetricLoadAverage, platform.MetricKernel, platform.MetricProcessRSS} {
		m.Restrict(name)
	}
	return m, nil
}

// LocalIPs lists the unicast addresses of the up, non-loopback interfaces, sorted, link-local
// excluded (an fe80:: address is no use to someone typing an address into a browser).
func LocalIPs() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []string
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			pfx, err := netip.ParsePrefix(a.String())
			if err != nil {
				continue
			}
			ip := pfx.Addr().Unmap()
			if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified() {
				continue
			}
			out = append(out, ip.String())
		}
	}
	sort.Strings(out)
	return out
}

// Cached serves one snapshot to every caller for TTL, so the endpoint cannot make the host work
// harder than once per TTL however often it is polled, and so that the CPU sampling window of the
// provider is at least TTL long.
type Cached struct {
	Provider platform.MetricsProvider
	TTL      time.Duration
	Now      func() time.Time // nil = time.Now

	mu   sync.Mutex
	at   time.Time
	last platform.Metrics
}

var _ platform.MetricsProvider = (*Cached)(nil)

// Collect implements platform.MetricsProvider. A provider error is not cached.
func (c *Cached) Collect(ctx context.Context) (platform.Metrics, error) {
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.at.IsZero() && now().Sub(c.at) < c.TTL {
		return c.last, nil
	}
	m, err := c.Provider.Collect(ctx)
	if err != nil {
		return platform.Metrics{}, err
	}
	c.at, c.last = now(), m
	return m, nil
}
