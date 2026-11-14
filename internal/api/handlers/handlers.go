// Package handlers implements gen.ServerInterface. Operations not implemented yet fall through to
// the embedded gen.Unimplemented and answer 501 while remaining documented in the spec.
package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/thanhtunguet/homecore/internal/activity"
	"github.com/thanhtunguet/homecore/internal/api/gen"
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
	"github.com/thanhtunguet/homecore/internal/storage"
	"github.com/thanhtunguet/homecore/internal/version"
)

// Handlers is the API implementation. Add domain services as fields as modules are implemented.
type Handlers struct {
	gen.Unimplemented

	DB   *storage.DB
	Auth *auth.Service // nil: the auth operations answer 501
	Log  *slog.Logger
	Now  func() time.Time
	Info InfoSource // what GET /system/info reports; set by the router

	// Device management (Phase 2). Each nil service makes its operations answer 501.
	Devices  *devices.Service    // device CRUD, history
	Commands *commands.Engine    // wake, command history, power_transition
	Monitor  *monitoring.Monitor // probe
	Audit    *audit.Writer       // audit_log entries for device changes (nil: not written)
	// Throttle is the shared wake/shutdown limiter (REQ-API-018); the legacy endpoints apply it
	// after resolving the device. Nil admits everything.
	Throttle *middleware.DeviceThrottle

	// Phase 3.
	ControllerKey *controllerkey.Key // GET /system/controller-key (nil: 501)
	Activity      *activity.Service  // GET /activity (nil: 501)

	// Phase 4.
	DDNS    *ddns.Service            // DDNS configurations, Cloudflare lookups, manual sync (nil: 501)
	Metrics platform.MetricsProvider // GET /system/metrics (nil: 501)
}

var _ gen.ServerInterface = (*Handlers)(nil)

// New returns Handlers with defaults filled in.
func New(db *storage.DB, log *slog.Logger) *Handlers {
	return &Handlers{DB: db, Log: log, Now: time.Now}
}

// GetHealthz reports liveness and the server clock (helps diagnose agent clock_skew errors).
func (h *Handlers) GetHealthz(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, gen.Health{
		Status:  "ok",
		Time:    h.Now().UTC(),
		Version: version.Version,
	})
}

// GetReadyz reports readiness: database reachable and schema up to date.
func (h *Handlers) GetReadyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	checks := map[string]string{}
	ready := true
	if err := h.DB.PingContext(ctx); err != nil {
		checks["database"] = "unreachable"
		ready = false
	} else {
		checks["database"] = "ok"
	}
	if v, err := h.DB.SchemaVersion(ctx); err != nil || v != storage.LatestVersion() {
		checks["migrations"] = "pending"
		ready = false
	} else {
		checks["migrations"] = "ok"
	}

	if ready {
		httpx.WriteJSON(w, http.StatusOK, gen.Readiness{Status: gen.ReadinessStatusReady, Checks: checks})
		return
	}
	httpx.WriteJSON(w, http.StatusServiceUnavailable, gen.Readiness{Status: gen.ReadinessStatusNotReady, Checks: checks})
}

// InfoSource is the configuration GET /system/info reports. It is a copy of non-secret settings.
type InfoSource struct {
	Mode       string
	Controller config.Controller
}

// GetSystemInfo reports version, mode and effective defaults. Nothing secret or file-system
// related (data dir, TLS paths, keys) is exposed.
func (h *Handlers) GetSystemInfo(w http.ResponseWriter, _ *http.Request) {
	c := h.Info.Controller
	mode := h.Info.Mode
	if mode == "" {
		mode = string(config.ModeController)
	}
	info := gen.SystemInfo{
		Version:                version.Version,
		Mode:                   gen.SystemInfoMode(mode),
		Listen:                 &c.Listen,
		Tls:                    ptr(c.TLS.Enabled()),
		ApiDocs:                &c.APIDocs,
		AccessTokenTtlSeconds:  ptr(int(c.Auth.AccessTokenTTL.Seconds())),
		RefreshTokenTtlSeconds: ptr(int(c.Auth.RefreshTokenTTL.Seconds())),
		RetentionDays:          &c.RetentionDays,
		MonitorDefaults: &struct {
			IntervalSeconds  *int `json:"interval_seconds,omitempty"`
			OfflineThreshold *int `json:"offline_threshold,omitempty"`
			TimeoutMs        *int `json:"timeout_ms,omitempty"`
		}{
			IntervalSeconds:  ptr(int(c.Monitor.DefaultInterval.Seconds())),
			OfflineThreshold: &c.Monitor.OfflineThreshold,
			TimeoutMs:        ptr(int(c.Monitor.DefaultTimeout.Milliseconds())),
		},
		CommandDeadlines: &struct {
			ShutdownSeconds *int `json:"shutdown_seconds,omitempty"`
			WakeSeconds     *int `json:"wake_seconds,omitempty"`
		}{
			ShutdownSeconds: ptr(int(c.Commands.ShutdownDeadline.Seconds())),
			WakeSeconds:     ptr(int(c.Commands.WakeDeadline.Seconds())),
		},
	}
	httpx.WriteJSON(w, http.StatusOK, info)
}

func ptr[T any](v T) *T { return &v }
