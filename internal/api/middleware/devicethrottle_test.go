package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

func throttleRouter(th *DeviceThrottle) http.Handler {
	// Like the generated wrapper, the throttle runs on the endpoint, after chi resolved the route
	// pattern (a router-level Use would run before routing and see no pattern).
	r := chi.NewRouter().With(th.Middleware)
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) })
	r.Post("/api/v1/devices/{id}/wake", ok)
	r.Post("/api/v1/devices/{id}/shutdown", ok)
	r.Post("/api/v1/devices/{id}/probe", ok)
	r.Get("/api/v1/devices/{id}/wake", ok)
	return r
}

func hit(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestDeviceThrottleWindow(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	th := NewDeviceThrottle(time.Second)
	th.now = func() time.Time { return now }
	h := throttleRouter(th)
	wake := "/api/v1/devices/AAA/wake"

	if got := hit(h, "POST", wake).Code; got != 202 {
		t.Fatalf("first → %d", got)
	}
	now = now.Add(400 * time.Millisecond)
	rec := hit(h, "POST", wake)
	if rec.Code != 429 || rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("within window → %d Retry-After=%q", rec.Code, rec.Header().Get("Retry-After"))
	}
	// A rejected request must not extend the window: 1 s after the first admitted one it passes.
	now = now.Add(600 * time.Millisecond)
	if got := hit(h, "POST", wake).Code; got != 202 {
		t.Fatalf("after the interval → %d, want 202", got)
	}
}

func TestDeviceThrottleScope(t *testing.T) {
	th := NewDeviceThrottle(time.Second)
	h := throttleRouter(th)
	if hit(h, "POST", "/api/v1/devices/AAA/wake").Code != 202 {
		t.Fatal("first wake")
	}
	for _, tc := range []struct {
		name, method, path string
		want               int
	}{
		{"shutdown shares the device slot", "POST", "/api/v1/devices/AAA/shutdown", 429},
		{"device id is case-insensitive", "POST", "/api/v1/devices/aaa/wake", 429},
		{"other device is independent", "POST", "/api/v1/devices/BBB/wake", 202},
		{"probe is not throttled", "POST", "/api/v1/devices/AAA/probe", 202},
		{"GET is not throttled", "GET", "/api/v1/devices/AAA/wake", 202},
	} {
		if got := hit(h, tc.method, tc.path).Code; got != tc.want {
			t.Errorf("%s → %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestDeviceThrottleDisabledAndSweep(t *testing.T) {
	off := throttleRouter(NewDeviceThrottle(0))
	for range 3 {
		if got := hit(off, "POST", "/api/v1/devices/AAA/wake").Code; got != 202 {
			t.Fatalf("disabled throttle → %d", got)
		}
	}

	now := time.Unix(1_700_000_000, 0)
	th := NewDeviceThrottle(time.Second)
	th.now = func() time.Time { return now }
	h := throttleRouter(th)
	for _, id := range []string{"A", "B", "C"} {
		hit(h, "POST", "/api/v1/devices/"+id+"/wake")
	}
	now = now.Add(2 * time.Minute)
	hit(h, "POST", "/api/v1/devices/D/wake") // triggers the sweep
	th.mu.Lock()
	n := len(th.last)
	th.mu.Unlock()
	if n != 1 {
		t.Fatalf("stale entries kept: %d, want 1", n)
	}
}
