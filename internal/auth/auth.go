// Package auth handles users, password hashing, JWT access tokens, rotating refresh tokens and
// role-based authorization for the Controller REST API.
//
// TODO(phase-1/2): argon2id password hashing, HS256 JWT issuing/verification with a dedicated
// signing key (sealed in system_settings, distinct from agent keys), refresh token rotation with
// family-based reuse detection, first-run admin bootstrap (HOMECORE_ADMIN_PASSWORD or a random
// password logged once), and the login/refresh/logout/me handlers.
package auth

import (
	"context"
	"errors"
)

// Role is a coarse permission level.
type Role string

const (
	RoleAdmin  Role = "admin"
	RoleViewer Role = "viewer"
)

// Principal is the authenticated caller of an API request.
type Principal struct {
	UserID   string
	Username string
	Role     Role
}

// HasRole reports whether p satisfies role r. Admin satisfies every role.
func (p Principal) HasRole(r Role) bool { return p.Role == RoleAdmin || p.Role == r }

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
