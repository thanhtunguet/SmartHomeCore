package system

import (
	"context"
	"errors"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"

	"github.com/thanhtunguet/homecore/internal/platform"
)

// REQ-SYS-001: every field of the snapshot is either present or named in `restricted`; nothing
// is silently missing.
func TestGopsutilSnapshotHasEveryFieldOrRestrictsIt(t *testing.T) {
	started := time.Now().Add(-time.Minute)
	g := NewGopsutil(GopsutilConfig{DataDir: t.TempDir(), Started: started})
	begin := time.Now()
	m, err := g.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first := time.Since(begin); first > 1500*time.Millisecond {
		t.Fatalf("the first collection took %v: CPU sampling must not block for about a second", first)
	}
	for i := 0; i < 5 && m.CPUPercent == nil && !slices.Contains(m.Restricted, platform.MetricCPUPercent); i++ {
		time.Sleep(time.Second) // coarse OS counters: the reading appears on a later call
		if m, err = g.Collect(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	has := func(name string) bool { return !slices.Contains(m.Restricted, name) }

	if m.Platform != runtime.GOOS+"/"+runtime.GOARCH || m.CollectedAt.IsZero() || m.Hostname == "" {
		t.Fatalf("platform/hostname/collected_at: %+v", m)
	}
	if m.CPUCores < 1 || m.PID == 0 || m.Goroutines < 1 || m.HeapAllocBytes == 0 {
		t.Fatalf("runtime fields: cores=%d pid=%d goroutines=%d heap=%d", m.CPUCores, m.PID, m.Goroutines, m.HeapAllocBytes)
	}
	// Linux and macOS must provide everything below; other platforms may restrict it, never omit it.
	type field struct {
		name string
		ok   bool
	}
	for _, f := range []field{
		{platform.MetricCPUPercent, m.CPUPercent != nil},
		{platform.MetricMemory, m.MemTotal > 0 && m.MemAvailable > 0 && m.MemUsed > 0},
		{platform.MetricDisk, m.DiskTotal > 0 && m.DiskPath != "" && m.DiskFree > 0},
		{platform.MetricUptime, m.UptimeSeconds > 0},
		{platform.MetricLoadAverage, len(m.LoadAverage) == 3},
		{platform.MetricKernel, m.Kernel != ""},
		{platform.MetricProcessRSS, m.ProcessRSS > 0},
	} {
		switch {
		case f.ok && !has(f.name):
			t.Errorf("%s is present but also listed as restricted", f.name)
		case !f.ok && has(f.name):
			t.Errorf("%s is neither present nor restricted: %+v", f.name, m)
		case !f.ok && (runtime.GOOS == "linux" || runtime.GOOS == "darwin") && f.name != platform.MetricLoadAverage:
			t.Errorf("%s is unavailable on %s", f.name, runtime.GOOS)
		}
	}
	if !has(platform.MetricProcessUptime) || m.ProcessUptimeSeconds < 0 {
		t.Errorf("process uptime: %d, restricted=%v", m.ProcessUptimeSeconds, m.Restricted)
	}
	if m.MemUsed > m.MemTotal || m.DiskFree > m.DiskTotal || m.DiskUsed > m.DiskTotal {
		t.Errorf("inconsistent sizes: %+v", m)
	}
	if m.CPUPercent != nil && (*m.CPUPercent < 0 || *m.CPUPercent > 100) {
		t.Errorf("cpu_percent %v out of range", *m.CPUPercent)
	}

	// A second call must not block on a new sampling window.
	begin = time.Now()
	if _, err := g.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(begin); d > time.Second {
		t.Fatalf("second collection took %v", d)
	}
}

func TestGopsutilFirstCollectionHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	g := NewGopsutil(GopsutilConfig{DataDir: t.TempDir()})
	m, err := g.Collect(ctx) // must return promptly and still produce a snapshot
	if err != nil {
		t.Fatal(err)
	}
	if m.Platform == "" {
		t.Fatal("no snapshot")
	}
}

// REQ-SYS-002: the runtime fallback reports what it knows and restricts the rest.
func TestRuntimeProviderRestrictsWhatItCannotKnow(t *testing.T) {
	m, err := RuntimeProvider{}.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if m.Platform == "" || m.Goroutines < 1 || m.PID == 0 || m.CPUCores < 1 {
		t.Fatalf("runtime fields: %+v", m)
	}
	for _, name := range []string{platform.MetricCPUPercent, platform.MetricMemory, platform.MetricDisk,
		platform.MetricUptime, platform.MetricLoadAverage, platform.MetricKernel, platform.MetricProcessRSS} {
		if !slices.Contains(m.Restricted, name) {
			t.Errorf("%s must be restricted in the runtime provider", name)
		}
	}
	if m.CPUPercent != nil || m.MemTotal != 0 || m.DiskTotal != 0 {
		t.Fatalf("runtime provider invented values: %+v", m)
	}
}

func TestBusyPercent(t *testing.T) {
	a := cpu.TimesStat{User: 10, System: 10, Idle: 80}
	cases := []struct {
		name string
		b    cpu.TimesStat
		want float64
		ok   bool
	}{
		{"half busy", cpu.TimesStat{User: 20, System: 20, Idle: 160}, 20, true},
		{"fully busy", cpu.TimesStat{User: 70, System: 30, Idle: 80}, 100, true},
		{"idle", cpu.TimesStat{User: 10, System: 10, Idle: 180}, 0, true},
		{"iowait counts as idle", cpu.TimesStat{User: 10, System: 10, Idle: 80, Iowait: 100}, 0, true},
		{"no time passed", a, 0, false},
		{"counter went backwards", cpu.TimesStat{User: 1}, 0, false},
	}
	for _, tc := range cases {
		got, ok := busyPercent(a, tc.b)
		if ok != tc.ok || (ok && (got < tc.want-0.001 || got > tc.want+0.001)) {
			t.Errorf("%s: got %v %v, want %v %v", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}

type countingProvider struct {
	n   int
	err error
}

func (c *countingProvider) Collect(context.Context) (platform.Metrics, error) {
	c.n++
	return platform.Metrics{Platform: "test", Goroutines: c.n}, c.err
}

// REQ-SYS-003: polling is cheap: one snapshot per TTL, errors are not cached.
func TestCachedServesOneSnapshotPerTTL(t *testing.T) {
	now := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	p := &countingProvider{}
	c := &Cached{Provider: p, TTL: 5 * time.Second, Now: func() time.Time { return now }}
	for range 5 {
		if m, err := c.Collect(context.Background()); err != nil || m.Goroutines != 1 {
			t.Fatalf("within the TTL: %+v %v", m, err)
		}
	}
	now = now.Add(5 * time.Second)
	if m, _ := c.Collect(context.Background()); m.Goroutines != 2 || p.n != 2 {
		t.Fatalf("after the TTL: %+v calls=%d", m, p.n)
	}
	now = now.Add(10 * time.Second)
	p.err = errors.New("boom")
	if _, err := c.Collect(context.Background()); err == nil {
		t.Fatal("the provider error was swallowed")
	}
	p.err = nil
	if m, err := c.Collect(context.Background()); err != nil || m.Goroutines != 4 {
		t.Fatalf("an error must not be cached: %+v %v", m, err)
	}
}

func TestLocalIPsArePublicOrPrivateUnicastOnly(t *testing.T) {
	for _, ip := range LocalIPs() {
		if ip == "127.0.0.1" || ip == "::1" || len(ip) > 4 && ip[:4] == "fe80" {
			t.Errorf("unexpected local ip %s", ip)
		}
	}
}
