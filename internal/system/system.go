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
	if addresses := LocalAddresses(); len(addresses) > 0 {
		m.LocalAddresses = addresses
		for _, address := range addresses {
			m.LocalIPs = append(m.LocalIPs, address.IPAddress)
		}
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

// LocalIPs retains the legacy sorted list of addresses for existing callers.
func LocalIPs() []string {
	var ips []string
	for _, address := range LocalAddresses() {
		ips = append(ips, address.IPAddress)
	}
	return ips
}

// LocalAddresses lists unicast addresses of up, non-loopback interfaces, sorted by IP then name.
// Link-local addresses are excluded, as they are not useful for accessing the Controller.
func LocalAddresses() []platform.LocalAddress {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []platform.LocalAddress
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		out = append(out, interfaceAddresses(ifc.Name, addrs)...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IPAddress == out[j].IPAddress {
			return out[i].InterfaceName < out[j].InterfaceName
		}
		return out[i].IPAddress < out[j].IPAddress
	})
	return out
}

func interfaceAddresses(name string, addrs []net.Addr) []platform.LocalAddress {
	var out []platform.LocalAddress
	for _, a := range addrs {
		pfx, err := netip.ParsePrefix(a.String())
		if err != nil {
			continue
		}
		ip := pfx.Addr().Unmap()
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified() {
			continue
		}
		out = append(out, platform.LocalAddress{InterfaceName: name, IPAddress: ip.String()})
	}
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
