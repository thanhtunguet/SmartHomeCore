// Package monitoring determines device reachability with TCP connect probes (not Telnet) and
// applies hysteresis so flaky networks do not make status flap.
//
// Files: monitoring.go (probe and hysteresis Tracker), monitor.go (the Monitor component: tick,
// due devices, worker pool, persistence, manual probe, observer hook).
package monitoring

import (
	"context"
	"errors"
	"net"
	"strconv"
	"syscall"
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

// FailureReason is a short, stable description of why the probe failed ("" when it succeeded).
func (r ProbeResult) FailureReason() string {
	var dnsErr *net.DNSError
	switch {
	case r.Err == nil:
		return ""
	case errors.As(r.Err, &dnsErr):
		return "host name could not be resolved"
	case errors.Is(r.Err, syscall.ECONNREFUSED):
		return "connection refused"
	case errors.Is(r.Err, context.DeadlineExceeded), isTimeout(r.Err):
		return "timed out"
	case errors.Is(r.Err, context.Canceled):
		return "cancelled"
	}
	return "unreachable"
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
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
	return NewTrackerFrom(offlineThreshold, devices.StatusUnknown)
}

// NewTrackerFrom starts in the given status (the one stored for the device), so a restart does not
// record a spurious transition. The consecutive-failure counter always starts at zero.
func NewTrackerFrom(offlineThreshold int, status devices.Status) *Tracker {
	return &Tracker{threshold: max(offlineThreshold, 1), status: status}
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
