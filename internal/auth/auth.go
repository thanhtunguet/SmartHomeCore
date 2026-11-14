// Package auth handles users, password hashing (argon2id), HS256 JWT access tokens, rotating
// refresh tokens with reuse detection and a short grace window, and authentication for the
// Controller REST API. There are no roles: every authenticated user is equal (ADR-011).
//
// Files: password.go (hashing), jwt.go (access tokens), store.go and accounts.go (users),
// service.go (login, refresh, logout, signing key). Accounts are created by `homecore user add`;
// there is no first-run bootstrap. Design: docs/design/architecture.md §10.1, ADR-013.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// Principal is the authenticated caller of an API request.
type Principal struct {
	UserID   string
	Username string
	Scope    string // empty for a session token; a restricted token or key names its scope (ScopeLegacy, "mcp")
	KeyID    string // id of the service account key that authenticated the request, empty otherwise
}

// ErrUnauthenticated is returned when a token is missing, malformed, expired or revoked.
var ErrUnauthenticated = errors.New("unauthenticated")

// Authenticator validates a bearer access token.
type Authenticator interface {
	Authenticate(ctx context.Context, bearerToken string) (Principal, error)
}

// RejectAll is the fail-closed Authenticator api.New falls back to when none is supplied (tests,
// misconfiguration): every protected endpoint answers 401, so nothing is accidentally exposed.
type RejectAll struct{}

func (RejectAll) Authenticate(context.Context, string) (Principal, error) {
	return Principal{}, ErrUnauthenticated
}

type ctxKey struct{}

// WithPrincipal stores p in ctx.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// FromContext returns the Principal stored by the auth middleware.
func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(Principal)
	return p, ok
}

// Router selects the Authenticator by the `alg` of the token's JWT header, so session and legacy
// tokens (HS256, signed with the JWT key) and service account keys (EdDSA, signed with a per-key
// Ed25519 key) share the one Authorization header. A token with an unknown or unreadable header
// goes to Default, which rejects it.
type Router struct {
	Default Authenticator            // HS256 session tokens
	ByAlg   map[string]Authenticator // for example "EdDSA" -> service account keys
}

// Authenticate implements Authenticator.
func (r Router) Authenticate(ctx context.Context, bearerToken string) (Principal, error) {
	if a, ok := r.ByAlg[TokenAlgorithm(bearerToken)]; ok {
		return a.Authenticate(ctx, bearerToken)
	}
	return r.Default.Authenticate(ctx, bearerToken)
}

// TokenAlgorithm returns the `alg` member of a JWT's header without verifying anything ("" if the
// token is not shaped like a JWT). It only routes; the chosen Authenticator does the real checks.
func TokenAlgorithm(token string) string {
	h, _, ok := strings.Cut(token, ".")
	if !ok || len(h) > 512 {
		return ""
	}
	raw, err := b64url.DecodeString(h)
	if err != nil {
		return ""
	}
	var hdr struct {
		Alg string `json:"alg"`
	}
	if json.Unmarshal(raw, &hdr) != nil {
		return ""
	}
	return hdr.Alg
}
