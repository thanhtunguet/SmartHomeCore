// Package auth handles users, password hashing, JWT access tokens, rotating refresh tokens and
// authentication for the Controller REST API. There are no roles: every authenticated user is
// equal (ADR-011).
//
// TODO(phase-1/2): argon2id password hashing, HS256 JWT issuing/verification with a dedicated
// signing key (sealed in system_settings, distinct from agent keys), refresh token rotation with
// family-based reuse detection, account management by the `homecore user` command (add, change
// password; there is no first-run bootstrap), and the login/refresh/logout/me handlers.
package auth

import (
	"context"
	"errors"
)

// Principal is the authenticated caller of an API request.
type Principal struct {
	UserID   string
	Username string
}

// ErrUnauthenticated is returned when a token is missing, malformed, expired or revoked.
var ErrUnauthenticated = errors.New("unauthenticated")

// Authenticator validates a bearer access token.
type Authenticator interface {
	Authenticate(ctx context.Context, bearerToken string) (Principal, error)
}

// RejectAll is the placeholder Authenticator used until JWT support lands: every protected
// endpoint answers 401, so nothing is accidentally exposed.
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
