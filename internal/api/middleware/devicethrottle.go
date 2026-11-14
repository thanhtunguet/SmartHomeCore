package middleware

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/thanhtunguet/homecore/internal/httpx"
)

// DeviceThrottle allows one wake/shutdown request per device per interval, shared by every user
// and by both command types (REQ-API-018). It protects the machines (and the LAN) from being
// toggled by button mashing or a loop in a client; the UI applies the same guard first.
//
// It must run inside the generated router after Authorize, so that the chi route pattern and the
// `id` path parameter are resolved and anonymous requests cannot consume a device's slot.
type DeviceThrottle struct {
	interval time.Duration
	now      func() time.Time

	mu        sync.Mutex
	last      map[string]time.Time // device id → time of the last admitted request
	lastSweep time.Time
}

// NewDeviceThrottle returns a throttle admitting one request per device per interval.
// An interval <= 0 disables it.
func NewDeviceThrottle(interval time.Duration) *DeviceThrottle {
	return &DeviceThrottle{interval: interval, now: time.Now, last: map[string]time.Time{}}
}

// throttled reports whether the operation is a device power command.
func throttled(method, pattern string) bool {
	if method != http.MethodPost {
		return false
	}
	return pattern == "/api/v1/devices/{id}/wake" || pattern == "/api/v1/devices/{id}/shutdown"
}

// Admit records a power request for the device and returns 0, or the time to wait when it must be
// rejected. The legacy endpoints call it after resolving a device by name, so both spellings of one
// device share one slot with the /api/v1 endpoints. A nil or disabled throttle admits everything.
func (t *DeviceThrottle) Admit(deviceID string) time.Duration {
	if t == nil || t.interval <= 0 {
		return 0
	}
	return t.admit(strings.ToLower(deviceID))
}

// WriteRejection answers `429 rate_limited` with Retry-After for a wait returned by Admit.
func WriteRejection(w http.ResponseWriter, wait time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(wait.Seconds())))))
	httpx.WriteError(w, http.StatusTooManyRequests, "rate_limited",
		"this device already received a wake/shutdown request; retry in a moment")
}

// admit records a request for key and returns 0, or the time to wait when it must be rejected.
// A rejected request does not extend the window.
func (t *DeviceThrottle) admit(key string) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	if now.Sub(t.lastSweep) > time.Minute {
		for k, at := range t.last {
			if now.Sub(at) >= t.interval {
				delete(t.last, k)
			}
		}
		t.lastSweep = now
	}
	if at, ok := t.last[key]; ok {
		if wait := t.interval - now.Sub(at); wait > 0 {
			return wait
		}
	}
	t.last[key] = now
	return 0
}

// Middleware answers `429 rate_limited` with `Retry-After` (whole seconds, at least 1) when the
// device received a wake/shutdown request less than one interval ago.
func (t *DeviceThrottle) Middleware(next http.Handler) http.Handler {
	if t.interval <= 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc := chi.RouteContext(r.Context())
		if rc == nil || !throttled(r.Method, rc.RoutePattern()) {
			next.ServeHTTP(w, r)
			return
		}
		if wait := t.admit(strings.ToLower(chi.URLParam(r, "id"))); wait > 0 {
			WriteRejection(w, wait)
			return
		}
		next.ServeHTTP(w, r)
	})
}
