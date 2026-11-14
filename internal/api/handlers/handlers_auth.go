package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/thanhtunguet/homecore/internal/api/gen"
	"github.com/thanhtunguet/homecore/internal/api/middleware"
	"github.com/thanhtunguet/homecore/internal/auth"
	"github.com/thanhtunguet/homecore/internal/httpx"
)

// Generic messages: they must not tell an attacker which part of the credentials was wrong.
const (
	msgBadCredentials = "invalid username or password"
	msgBadRefresh     = "invalid or expired refresh token"
)

// Login exchanges a username and password for a token pair.
func (h *Handlers) Login(w http.ResponseWriter, r *http.Request) {
	if h.Auth == nil {
		h.Unimplemented.Login(w, r)
		return
	}
	var req gen.LoginRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Username == "" || len(req.Username) > auth.MaxUsernameLength || req.Password == "" || tooLong(req.Password) {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "username and password are required (password at most 256 characters)")
		return
	}
	pair, err := h.Auth.Login(r.Context(), req.Username, req.Password, middleware.ClientIP(r))
	if errors.Is(err, auth.ErrInvalidCredentials) {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", msgBadCredentials)
		return
	}
	if err != nil {
		h.internalError(w, r, "login", err)
		return
	}
	writePair(w, pair)
}

// RefreshToken rotates a refresh token.
func (h *Handlers) RefreshToken(w http.ResponseWriter, r *http.Request) {
	if h.Auth == nil {
		h.Unimplemented.RefreshToken(w, r)
		return
	}
	var req gen.RefreshRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.RefreshToken == "" || len(req.RefreshToken) > 512 {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "refresh_token is required")
		return
	}
	pair, err := h.Auth.Refresh(r.Context(), req.RefreshToken, middleware.ClientIP(r))
	if errors.Is(err, auth.ErrInvalidRefresh) {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", msgBadRefresh)
		return
	}
	if err != nil {
		h.internalError(w, r, "refresh", err)
		return
	}
	writePair(w, pair)
}

// Logout revokes the family of the given refresh token.
func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	if h.Auth == nil {
		h.Unimplemented.Logout(w, r)
		return
	}
	p, ok := auth.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing bearer token")
		return
	}
	var req gen.RefreshRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.RefreshToken) > 512 {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "refresh_token is invalid")
		return
	}
	if err := h.Auth.Logout(r.Context(), p, req.RefreshToken, middleware.ClientIP(r)); err != nil {
		h.internalError(w, r, "logout", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetCurrentUser returns the authenticated user.
func (h *Handlers) GetCurrentUser(w http.ResponseWriter, r *http.Request) {
	if h.Auth == nil {
		h.Unimplemented.GetCurrentUser(w, r)
		return
	}
	p, ok := auth.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing bearer token")
		return
	}
	u, err := h.Auth.CurrentUser(r.Context(), p)
	if errors.Is(err, auth.ErrUserNotFound) {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid or expired token")
		return
	}
	if err != nil {
		h.internalError(w, r, "current user", err)
		return
	}
	id, err := uuid.Parse(u.ID)
	if err != nil {
		h.internalError(w, r, "current user", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, gen.User{Id: id, Username: u.Username})
}

func writePair(w http.ResponseWriter, p auth.TokenPair) {
	// Tokens must never be cached by browsers or proxies.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	httpx.WriteJSON(w, http.StatusOK, gen.TokenPair{
		AccessToken: p.AccessToken, RefreshToken: p.RefreshToken, TokenType: "Bearer", ExpiresIn: p.ExpiresIn,
	})
}

// tooLong reports whether a password exceeds the spec's 256-character limit.
func tooLong(pw string) bool { return auth.ValidateMaxPassword(pw) != nil }

// decodeJSON reads a JSON body (unknown fields are tolerated, trailing data is not) and writes
// a 400 itself on failure.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil || dec.More() {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "request body must be a JSON object")
		return false
	}
	return true
}

func (h *Handlers) internalError(w http.ResponseWriter, r *http.Request, what string, err error) {
	h.Log.Error("auth handler failed", "op", what, "err", err, "path", r.URL.Path)
	httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
}
