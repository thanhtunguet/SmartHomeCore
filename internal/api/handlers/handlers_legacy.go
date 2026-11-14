package handlers

import (
	"net/http"

	"github.com/thanhtunguet/homecore/internal/api/gen"
	"github.com/thanhtunguet/homecore/internal/api/middleware"
	"github.com/thanhtunguet/homecore/internal/devices"
)

// The legacy endpoints (ADR-017) keep apps written for the single-device system working:
// GET /api/turn-on, /api/turn-off and /api/is-online with the device in `?device=`. They are thin
// adapters over the same services as the /api/v1 operations; authentication, the throttle, the
// audit trail and the idempotency rules are the ones of wake and shutdown.

// legacyVia is recorded in the audit details so an entry shows which door was used.
var legacyVia = map[string]any{"via": "legacy"}

// LegacyTurnOn wakes the device (Wake-on-LAN) and answers `true` once the request is accepted.
func (h *Handlers) LegacyTurnOn(w http.ResponseWriter, r *http.Request, params gen.LegacyTurnOnParams) {
	if h.Commands == nil || h.Devices == nil {
		h.Unimplemented.LegacyTurnOn(w, r, params)
		return
	}
	d, ok := h.legacyPowerTarget(w, r, params.Device)
	if !ok {
		return
	}
	if _, err := h.requestWake(r, d.ID, legacyVia); err != nil {
		h.apiError(w, r, "legacy turn-on", err)
		return
	}
	writeLegacyBool(w, true)
}

// LegacyTurnOff requests a signed shutdown and answers `true` once the request is accepted.
func (h *Handlers) LegacyTurnOff(w http.ResponseWriter, r *http.Request, params gen.LegacyTurnOffParams) {
	if h.Commands == nil || h.Devices == nil {
		h.Unimplemented.LegacyTurnOff(w, r, params)
		return
	}
	d, ok := h.legacyPowerTarget(w, r, params.Device)
	if !ok {
		return
	}
	if _, err := h.requestShutdown(r, d.ID, legacyVia); err != nil {
		h.apiError(w, r, "legacy turn-off", err)
		return
	}
	writeLegacyBool(w, true)
}

// LegacyIsOnline answers `true` when the Controller last saw the device online, else `false`.
func (h *Handlers) LegacyIsOnline(w http.ResponseWriter, r *http.Request, params gen.LegacyIsOnlineParams) {
	if h.Devices == nil {
		h.Unimplemented.LegacyIsOnline(w, r, params)
		return
	}
	d, err := h.Devices.Resolve(r.Context(), params.Device)
	if err != nil {
		h.apiError(w, r, "legacy is-online", err)
		return
	}
	writeLegacyBool(w, d.Status == devices.StatusOnline)
}

// legacyPowerTarget resolves the device and applies the per-device wake/shutdown throttle, which
// the generated middleware cannot do because it only knows the query value, not the device id.
// It writes the error response and returns false when the request must stop.
func (h *Handlers) legacyPowerTarget(w http.ResponseWriter, r *http.Request, ref string) (devices.Device, bool) {
	d, err := h.Devices.Resolve(r.Context(), ref)
	if err != nil {
		h.apiError(w, r, "legacy resolve device", err)
		return devices.Device{}, false
	}
	if wait := h.Throttle.Admit(d.ID); wait > 0 {
		middleware.WriteRejection(w, wait)
		return devices.Device{}, false
	}
	return d, true
}

// writeLegacyBool writes the bare text the old apps parse. It is not cacheable: the answer is the
// current state.
func writeLegacyBool(w http.ResponseWriter, v bool) {
	body := "false"
	if v {
		body = "true"
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(body))
}
