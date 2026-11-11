// Package handlers implements gen.ServerInterface. Operations not implemented yet fall through to
// the embedded gen.Unimplemented and answer 501 while remaining documented in the spec.
package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/thanhtunguet/homecore/internal/api/gen"
	"github.com/thanhtunguet/homecore/internal/httpx"
	"github.com/thanhtunguet/homecore/internal/storage"
	"github.com/thanhtunguet/homecore/internal/version"
)

// Handlers is the API implementation. Add domain services as fields as modules are implemented.
type Handlers struct {
	gen.Unimplemented

	DB  *storage.DB
	Log *slog.Logger
	Now func() time.Time
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
		httpx.WriteJSON(w, http.StatusOK, gen.Readiness{Status: gen.Ready, Checks: checks})
		return
	}
	httpx.WriteJSON(w, http.StatusServiceUnavailable, gen.Readiness{Status: gen.NotReady, Checks: checks})
}
