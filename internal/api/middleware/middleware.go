package middleware

import (
	"log/slog"
	"net"
	"net/http"
	"slices"
	"sync"
	"time"

	chimw "github.com/go-chi/chi/v5/middleware"
	"golang.org/x/time/rate"

	"github.com/thanhtunguet/homecore/internal/httpx"
)

// Recoverer converts panics into 500 responses and logs them.
func Recoverer(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if p := recover(); p != nil {
					if p == http.ErrAbortHandler {
						panic(p)
					}
					log.Error("panic in handler", "panic", p, "path", r.URL.Path,
						"request_id", chimw.GetReqID(r.Context()))
					httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// AccessLog writes one structured line per request. Never logs headers or bodies.
func AccessLog(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			log.Info("http",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"bytes", ww.BytesWritten(),
				"dur", time.Since(start).Round(time.Microsecond),
				"client_ip", ClientIP(r),
				"request_id", chimw.GetReqID(r.Context()),
			)
		})
	}
}

// SecurityHeaders sets conservative browser security headers.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

// BodyLimit caps request bodies.
func BodyLimit(n int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, n)
			next.ServeHTTP(w, r)
		})
	}
}

// CORS allows the listed origins (exact match). With an empty list no CORS headers are sent,
// which keeps the API same-origin only.
func CORS(allowed []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if len(allowed) == 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" && slices.Contains(allowed, origin) {
				h := w.Header()
				h.Set("Access-Control-Allow-Origin", origin)
				h.Add("Vary", "Origin")
				h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
				h.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
				h.Set("Access-Control-Max-Age", "600")
				if r.Method == http.MethodOptions {
					w.WriteHeader(http.StatusNoContent)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ClientIP returns the client address resolved by chi's ClientIPFrom* middleware, falling back to
// the TCP peer address.
func ClientIP(r *http.Request) string {
	if ip := chimw.GetClientIP(r.Context()); ip != "" {
		return ip
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// RateLimiter is a per-client-IP token bucket. Idle buckets are swept periodically.
type RateLimiter struct {
	mu        sync.Mutex
	perMinute int
	burst     int
	clients   map[string]*client
	lastSweep time.Time
}

type client struct {
	lim  *rate.Limiter
	seen time.Time
}

// NewRateLimiter allows perMinute requests per IP with the given burst.
func NewRateLimiter(perMinute, burst int) *RateLimiter {
	return &RateLimiter{perMinute: perMinute, burst: burst, clients: map[string]*client{}, lastSweep: time.Now()}
}

func (rl *RateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	if now.Sub(rl.lastSweep) > time.Minute {
		for k, c := range rl.clients {
			if now.Sub(c.seen) > 10*time.Minute {
				delete(rl.clients, k)
			}
		}
		rl.lastSweep = now
	}
	c, ok := rl.clients[ip]
	if !ok {
		c = &client{lim: rate.NewLimiter(rate.Limit(float64(rl.perMinute)/60), rl.burst)}
		rl.clients[ip] = c
	}
	c.seen = now
	return c.lim.Allow()
}

// Middleware returns 429 when the client exceeds its budget. A perMinute <= 0 disables limiting.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	if rl.perMinute <= 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rl.allow(ClientIP(r)) {
			w.Header().Set("Retry-After", "60")
			httpx.WriteError(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
			return
		}
		next.ServeHTTP(w, r)
	})
}
