// Package middleware contains HTTP middleware for the Controller API.
package middleware

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/go-chi/chi/v5"

	"github.com/thanhtunguet/homecore/internal/auth"
	"github.com/thanhtunguet/homecore/internal/httpx"
)

// SecuritySchemeName is the only security scheme the API uses.
const SecuritySchemeName = "bearerAuth"

// Requirement is the access rule of one operation: public, or any authenticated user.
type Requirement struct {
	Public bool
}

// Policy maps "METHOD /route/{pattern}" to its Requirement. It is derived from the OpenAPI spec,
// so the spec is the single source of truth for who may call what.
type Policy map[string]Requirement

// PolicyFromSpec builds a Policy from the spec's global and per-operation security requirements.
// There are no roles (ADR-011): bearerAuth must not carry scopes, so a stray `[admin]` aborts
// startup instead of silently granting everyone access.
func PolicyFromSpec(spec *openapi3.T) (Policy, error) {
	p := Policy{}
	for path, item := range spec.Paths.Map() {
		for method, op := range item.Operations() {
			reqs := spec.Security
			if op.Security != nil {
				reqs = *op.Security
			}
			var r Requirement
			if len(reqs) == 0 {
				r.Public = true
			}
			for _, sr := range reqs {
				if len(sr) == 0 { // `- {}` makes auth optional → treat as public
					r.Public = true
				}
				for name, scopes := range sr {
					if name != SecuritySchemeName {
						return nil, fmt.Errorf("%s %s: unsupported security scheme %q", method, path, name)
					}
					if len(scopes) > 0 {
						return nil, fmt.Errorf("%s %s: scopes %v on %s are not supported (no roles)", method, path, scopes, name)
					}
				}
			}
			p[strings.ToUpper(method)+" "+path] = r
		}
	}
	return p, nil
}

// Authorize enforces the Policy. It must run inside the generated router (as a
// gen.MiddlewareFunc) so the chi route pattern is already resolved. Unknown routes fail closed.
func Authorize(policy Policy, authn auth.Authenticator, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			pattern := ""
			if rc := chi.RouteContext(r.Context()); rc != nil {
				pattern = rc.RoutePattern()
			}
			req, ok := policy[r.Method+" "+pattern]
			if !ok {
				log.Error("no security policy for route; denying", "method", r.Method, "pattern", pattern)
				httpx.WriteError(w, http.StatusForbidden, "forbidden", "access denied")
				return
			}
			if req.Public {
				next.ServeHTTP(w, r)
				return
			}

			token, ok := bearerToken(r.Header.Get("Authorization"))
			if !ok {
				w.Header().Set("WWW-Authenticate", `Bearer realm="homecore"`)
				httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing bearer token")
				return
			}
			principal, err := authn.Authenticate(r.Context(), token)
			if err != nil {
				w.Header().Set("WWW-Authenticate", `Bearer realm="homecore", error="invalid_token"`)
				httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid or expired token")
				return
			}
			next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), principal)))
		})
	}
}

func bearerToken(h string) (string, bool) {
	scheme, token, ok := strings.Cut(h, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}
