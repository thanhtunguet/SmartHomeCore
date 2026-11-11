// Package monitoring determines device reachability with TCP connect probes (not Telnet) and
// applies hysteresis so flaky networks do not make status flap.
//
// TODO(phase-2): Monitor component — tick every cfg.Tick, pick due devices, run probes through a
// bounded worker pool with jitter, persist transitions to device_status_history, update
// last_seen_at/last_checked_at, and expose ProbeNow(id) plus an observer hook for the commands
// dispatcher.
package monitoring

import (
	"context"
	"net"
	"strconv"
	"time"

	"github.com/thanhtunguet/homecore/internal/devices"
)

// ProbeResult is the outcome of one TCP connect attempt.
type ProbeResult struct {
	Reachable bool
	Latency   time.Duration
	CheckedAt time.Time
	Err       error
}

// Probe opens and immediately closes a TCP connection to host:port. Success proves that some
// service accepts connections on that port — not that the whole OS is healthy.
func Probe(ctx context.Context, host string, port int, timeout time.Duration) ProbeResult {
	start := time.Now()
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	res := ProbeResult{CheckedAt: start, Latency: time.Since(start), Err: err}
	if err == nil {
		res.Reachable = true
		_ = conn.Close()
	}
	return res
}

// Tracker applies hysteresis to probe results for one device: one success → online;
// threshold consecutive failures → offline. Not safe for concurrent use (one per device,
// owned by the monitor).
type Tracker struct {
	threshold int
	failures  int
	status    devices.Status
}

// NewTracker starts in StatusUnknown.
func NewTracker(offlineThreshold int) *Tracker {
	return &Tracker{threshold: max(offlineThreshold, 1), status: devices.StatusUnknown}
}

// Status returns the current status.
func (t *Tracker) Status() devices.Status { return t.status }

// Observe records a probe result and returns the (possibly unchanged) status and whether it
// changed.
func (t *Tracker) Observe(reachable bool) (devices.Status, bool) {
	prev := t.status
	if reachable {
		t.failures = 0
		t.status = devices.StatusOnline
	} else {
		t.failures++
		if t.failures >= t.threshold {
			t.status = devices.StatusOffline
		}
	}
	return t.status, t.status != prev
}
