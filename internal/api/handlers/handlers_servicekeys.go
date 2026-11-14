package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/thanhtunguet/homecore/internal/api/gen"
	"github.com/thanhtunguet/homecore/internal/api/middleware"
	"github.com/thanhtunguet/homecore/internal/auth"
	"github.com/thanhtunguet/homecore/internal/httpx"
	"github.com/thanhtunguet/homecore/internal/servicekey"
)

// Service account keys (ADR-018). Creating, listing and revoking need a session token: the
// operations carry no `x-token-scopes`, so Authorize refuses any key there. Rotation is the one
// operation a key calls on itself.

// CreateServiceAccountKey generates a key for the caller and returns its credential once.
func (h *Handlers) CreateServiceAccountKey(w http.ResponseWriter, r *http.Request) {
	if h.ServiceKeys == nil {
		h.Unimplemented.CreateServiceAccountKey(w, r)
		return
	}
	caller, ok := h.keyCaller(w, r)
	if !ok {
		return
	}
	var req gen.CreateServiceAccountKeyRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	issued, err := h.ServiceKeys.Create(r.Context(), caller, req.Name, string(req.Scope), seconds(req.TtlSeconds))
	if err != nil {
		h.keyError(w, r, "create service account key", err)
		return
	}
	writeNoStore(w)
	httpx.WriteJSON(w, http.StatusCreated, toCreatedKey(issued, h.Now()))
}

// ListServiceAccountKeys lists the caller's keys (metadata only).
func (h *Handlers) ListServiceAccountKeys(w http.ResponseWriter, r *http.Request) {
	if h.ServiceKeys == nil {
		h.Unimplemented.ListServiceAccountKeys(w, r)
		return
	}
	caller, ok := h.keyCaller(w, r)
	if !ok {
		return
	}
	keys, err := h.ServiceKeys.List(r.Context(), caller.UserID)
	if err != nil {
		h.keyError(w, r, "list service account keys", err)
		return
	}
	now := h.Now()
	items := make([]gen.ServiceAccountKey, 0, len(keys))
	for _, k := range keys {
		items = append(items, toAPIKey(k, now))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// RevokeServiceAccountKey ends one of the caller's keys at once.
func (h *Handlers) RevokeServiceAccountKey(w http.ResponseWriter, r *http.Request, id gen.ServiceAccountKeyId) {
	if h.ServiceKeys == nil {
		h.Unimplemented.RevokeServiceAccountKey(w, r, id)
		return
	}
	caller, ok := h.keyCaller(w, r)
	if !ok {
		return
	}
	if err := h.ServiceKeys.Revoke(r.Context(), caller, id.String()); err != nil {
		h.keyError(w, r, "revoke service account key", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// RotateServiceAccountKey replaces the key that authenticated the request. Authorize already
// verified the key; the service validates it again inside the rotation's transaction.
func (h *Handlers) RotateServiceAccountKey(w http.ResponseWriter, r *http.Request) {
	if h.ServiceKeys == nil {
		h.Unimplemented.RotateServiceAccountKey(w, r)
		return
	}
	caller, ok := h.keyCaller(w, r)
	if !ok {
		return
	}
	if caller.ViaKeyID == "" {
		httpx.WriteError(w, http.StatusForbidden, "forbidden", "only a service account key can rotate itself; use the service-account-keys operations with a session token")
		return
	}
	var req gen.RotateServiceAccountKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) { // the body is optional
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "request body must be a JSON object")
		return
	}
	rot, err := h.ServiceKeys.Rotate(r.Context(), caller, caller.ViaKeyID, seconds(req.TtlSeconds))
	if err != nil {
		h.keyError(w, r, "rotate service account key", err)
		return
	}
	out := toCreatedKey(rot.Issued, h.Now())
	if prev, err := uuid.Parse(rot.PreviousID); err == nil {
		out.PreviousKeyId = &prev
	}
	until := rot.PreviousValidUntil.UTC()
	out.PreviousKeyValidUntil = &until
	writeNoStore(w)
	httpx.WriteJSON(w, http.StatusOK, out)
}

// keyCaller builds the audit identity of the authenticated caller.
func (h *Handlers) keyCaller(w http.ResponseWriter, r *http.Request) (servicekey.Caller, bool) {
	p, ok := auth.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing bearer token")
		return servicekey.Caller{}, false
	}
	return servicekey.Caller{UserID: p.UserID, Username: p.Username, RemoteAddr: middleware.ClientIP(r), ViaKeyID: p.KeyID}, true
}

func (h *Handlers) keyError(w http.ResponseWriter, r *http.Request, op string, err error) {
	var verr *servicekey.ValidationError
	switch {
	case errors.As(err, &verr):
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", verr.Error())
	case errors.Is(err, servicekey.ErrNotFound) && op == "rotate service account key":
		// The key that authenticated this request vanished (pruned) in the meantime.
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid or expired token")
	case errors.Is(err, servicekey.ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "not_found", "service account key not found")
	case errors.Is(err, servicekey.ErrInactive):
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid or expired token")
	case errors.Is(err, servicekey.ErrRotatedAway):
		httpx.WriteError(w, http.StatusConflict, "conflict", "this key was already rotated and its successor is no longer usable; create a new key")
	default:
		h.Log.Error("service account key handler failed", "op", op, "err", err, "path", r.URL.Path)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
	}
}

// seconds converts an optional ttl_seconds; absent means "default" (0).
func seconds(n *int) time.Duration {
	if n == nil {
		return 0
	}
	return time.Duration(*n) * time.Second
}

// writeNoStore keeps a response that carries a key out of browser and proxy caches.
func writeNoStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
}

func toAPIKey(k servicekey.Key, now time.Time) gen.ServiceAccountKey {
	id, _ := uuid.Parse(k.ID)
	out := gen.ServiceAccountKey{
		Id: id, Name: k.Name, Scope: gen.ServiceAccountKeyScope(k.Scope), Status: gen.ServiceAccountKeyStatus(k.Status(now)),
		TtlSeconds: int(k.TTL.Seconds()), CreatedAt: k.CreatedAt, ExpiresAt: k.ExpiresAt, LastUsedAt: k.LastUsedAt,
	}
	if next, err := uuid.Parse(k.RotatedTo); err == nil {
		out.RotatedTo = &next
	}
	return out
}

func toCreatedKey(i servicekey.Issued, now time.Time) gen.ServiceAccountKeyCreated {
	k := toAPIKey(i.Key, now)
	return gen.ServiceAccountKeyCreated{
		Id: k.Id, Name: k.Name, Scope: k.Scope, Status: gen.ServiceAccountKeyCreatedStatus(k.Status),
		TtlSeconds: k.TtlSeconds, CreatedAt: k.CreatedAt, ExpiresAt: k.ExpiresAt, LastUsedAt: k.LastUsedAt,
		RotatedTo: k.RotatedTo, Key: i.Credential,
	}
}
