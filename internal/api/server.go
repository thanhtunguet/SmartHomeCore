// Package api is the Controller HTTP server: router, middleware chain, generated OpenAPI routes,
// API docs (spec + offline Swagger UI) and the static frontend.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/thanhtunguet/homecore/internal/activity"
	"github.com/thanhtunguet/homecore/internal/api/gen"
	"github.com/thanhtunguet/homecore/internal/api/handlers"
	"github.com/thanhtunguet/homecore/internal/api/middleware"
	"github.com/thanhtunguet/homecore/internal/audit"
	"github.com/thanhtunguet/homecore/internal/auth"
	"github.com/thanhtunguet/homecore/internal/commands"
	"github.com/thanhtunguet/homecore/internal/config"
	"github.com/thanhtunguet/homecore/internal/controllerkey"
	"github.com/thanhtunguet/homecore/internal/ddns"
	"github.com/thanhtunguet/homecore/internal/devices"
	"github.com/thanhtunguet/homecore/internal/httpx"
	"github.com/thanhtunguet/homecore/internal/monitoring"
	"github.com/thanhtunguet/homecore/internal/platform"
	"github.com/thanhtunguet/homecore/internal/servicekey"
	"github.com/thanhtunguet/homecore/internal/storage"
	"github.com/thanhtunguet/homecore/web"
)

const (
	maxBodyBytes   = 1 << 20
	requestTimeout = 30 * time.Second

	// DeviceCommandInterval is the minimum time between two wake/shutdown requests for the same
	// device, across all users (REQ-API-018).
	DeviceCommandInterval = time.Second
)

// Deps are the Controller API dependencies.
type Deps struct {
	Config          config.Controller
	Mode            config.Mode // reported by GET /system/info; empty is shown as "controller"
	ShutdownTimeout time.Duration
	Log             *slog.Logger
	DB              *storage.DB
	Authn           auth.Authenticator  // defaults to Auth (plus ServiceKeys when set), then to auth.RejectAll
	Auth            *auth.Service       // login/refresh/logout/me; nil answers 501
	ServiceKeys     *servicekey.Service // service account keys (ADR-018); nil answers 501 and EdDSA keys are not accepted

	// Device management; each nil service makes its operations answer 501.
	Devices  *devices.Service
	Commands *commands.Engine
	Monitor  *monitoring.Monitor
	Audit    *audit.Writer

	ControllerKey *controllerkey.Key // GET /system/controller-key; nil answers 501
	Activity      *activity.Service  // GET /activity; nil answers 501

	DDNS    *ddns.Service            // DDNS endpoints; nil answers 501
	Metrics platform.MetricsProvider // GET /system/metrics; nil answers 501
}

// Server is the Controller HTTP server.
type Server struct {
	deps    Deps
	handler http.Handler
}

// New builds the router. It fails if the embedded spec cannot be loaded or contains a security
// requirement the middleware does not understand.
func New(d Deps) (*Server, error) {
	if d.Authn == nil && d.Auth != nil {
		d.Authn = d.Auth
		if d.ServiceKeys != nil { // service account keys are EdDSA JWTs, session tokens are HS256
			d.Authn = auth.Router{Default: d.Auth, ByAlg: map[string]auth.Authenticator{servicekey.Algorithm: d.ServiceKeys}}
		}
	}
	if d.Authn == nil {
		d.Authn = auth.RejectAll{}
	}
	h, err := newRouter(d)
	if err != nil {
		return nil, err
	}
	return &Server{deps: d, handler: h}, nil
}

// Handler returns the root handler (useful for tests).
func (s *Server) Handler() http.Handler { return s.handler }

// Run serves until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	cfg := s.deps.Config
	srv := httpx.NewServer(cfg.Listen, s.handler, s.deps.Log)
	return httpx.Serve(ctx, s.deps.Log, srv, cfg.TLS.CertFile, cfg.TLS.KeyFile, s.deps.ShutdownTimeout)
}

func newRouter(d Deps) (http.Handler, error) {
	spec, err := gen.GetSpec()
	if err != nil {
		return nil, fmt.Errorf("load embedded openapi spec: %w", err)
	}
	policy, err := middleware.PolicyFromSpec(spec)
	if err != nil {
		return nil, fmt.Errorf("derive security policy: %w", err)
	}
	specJSON, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("marshal openapi spec: %w", err)
	}

	cfg := d.Config
	r := chi.NewRouter()
	r.Use(chimw.RequestID)
	// Resolve the client IP for logging and rate limiting. X-Forwarded-For is honoured only when
	// the TCP peer is a configured trusted proxy, so clients cannot spoof their address.
	if len(cfg.TrustedProxies) > 0 {
		r.Use(chimw.ClientIPFromXFF(cfg.TrustedProxies...))
	} else {
		r.Use(chimw.ClientIPFromRemoteAddr)
	}
	r.Use(middleware.Recoverer(d.Log))
	r.Use(middleware.AccessLog(d.Log))
	r.Use(middleware.SecurityHeaders)
	r.Use(middleware.CORS(cfg.CORSAllowedOrigins))
	r.Use(middleware.NewRateLimiter(cfg.Auth.APIRatePerMinute, max(cfg.Auth.APIRatePerMinute/6, 10)).Middleware)
	r.Use(middleware.BodyLimit(maxBodyBytes))
	r.Use(chimw.Timeout(requestTimeout))

	// Stricter limit on credential endpoints (brute-force protection).
	loginLimiter := middleware.NewRateLimiter(cfg.Auth.LoginRatePerMinute, max(cfg.Auth.LoginRatePerMinute/2, 3))
	r.Use(func(next http.Handler) http.Handler {
		limited := loginLimiter.Middleware(next)
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.Method == http.MethodPost && strings.HasPrefix(req.URL.Path, "/api/v1/auth/") {
				limited.ServeHTTP(w, req)
				return
			}
			next.ServeHTTP(w, req)
		})
	})

	if cfg.APIDocs {
		r.Get("/api/openapi.json", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(specJSON)
		})
		r.Get("/api/docs", http.RedirectHandler("/api/docs/", http.StatusMovedPermanently).ServeHTTP)
		r.Handle("/api/docs/*", http.StripPrefix("/api/docs/", http.FileServerFS(web.Swagger())))
	}

	h := handlers.New(d.DB, d.Log)
	h.Auth = d.Auth
	h.ServiceKeys = d.ServiceKeys
	h.Devices, h.Commands, h.Monitor, h.Audit = d.Devices, d.Commands, d.Monitor, d.Audit
	h.ControllerKey, h.Activity = d.ControllerKey, d.Activity
	h.DDNS, h.Metrics = d.DDNS, d.Metrics
	throttle := middleware.NewDeviceThrottle(DeviceCommandInterval)
	h.Throttle = throttle
	h.Info = handlers.InfoSource{Mode: string(d.Mode), Controller: d.Config}
	gen.HandlerWithOptions(h, gen.ChiServerOptions{
		BaseRouter: r,
		// The wrapper applies these in order, so the last is outermost: a request passes
		// NotImplementedEnvelope, then Authorize, then the device throttle, then the handler.
		Middlewares: []gen.MiddlewareFunc{
			throttle.Middleware,
			middleware.Authorize(policy, d.Authn, d.Log),
			middleware.NotImplementedEnvelope,
		},
		ErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			httpx.WriteError(w, http.StatusBadRequest, "bad_request", err.Error())
		},
	})

	r.NotFound(staticHandler(web.Static()))
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	})
	return r, nil
}

// staticHandler serves the embedded frontend with SPA fallback to index.html. Unknown /api paths
// get a JSON 404 instead of HTML.
func staticHandler(files fs.FS) http.HandlerFunc {
	fileServer := http.FileServerFS(files)
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api" {
			httpx.WriteError(w, http.StatusNotFound, "not_found", "no such endpoint")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			httpx.WriteError(w, http.StatusNotFound, "not_found", "not found")
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "."
		}
		if _, err := fs.Stat(files, name); errors.Is(err, fs.ErrNotExist) {
			setStaticCacheHeaders(w, "index.html")
			http.ServeFileFS(w, r, files, "index.html")
			return
		}
		setStaticCacheHeaders(w, name)
		fileServer.ServeHTTP(w, r)
	}
}

// setStaticCacheHeaders: Vite emits content-hashed names under assets/, which never change, so
// they can be cached forever. Everything else (index.html, the SPA fallback) must be revalidated
// so a new build is picked up. Embedded files have no mod-time, hence no Last-Modified/ETag.
func setStaticCacheHeaders(w http.ResponseWriter, name string) {
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
}
