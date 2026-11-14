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
	"errors"
)

// Principal is the authenticated caller of an API request.
type Principal struct {
	UserID   string
	Username string
	Scope    string // empty for a session token, ScopeLegacy for a legacy client token
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
