// Package middleware contains HTTP middleware for the Controller API.
package middleware

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/go-chi/chi/v5"

	"github.com/thanhtunguet/homecore/internal/auth"
	"github.com/thanhtunguet/homecore/internal/httpx"
)

// SecuritySchemeName is the only security scheme the API uses.
const SecuritySchemeName = "bearerAuth"

// LegacyTag marks, in the spec, the operations a legacy-scoped token may call (ADR-017). It is
// shorthand for listing the scope "legacy" in the operation's `x-token-scopes`.
const LegacyTag = "legacy"

// ScopesExtension is the operation-level OpenAPI extension that lists the token scopes accepted
// besides a normal session token, for example `x-token-scopes: [legacy, mcp]` (ADR-018). An
// operation without it (and without the `legacy` tag) accepts session tokens only: a scoped token
// or service account key is refused with 403 there.
const ScopesExtension = "x-token-scopes"

// Requirement is the access rule of one operation: public, or any authenticated user. Scopes
// lists the restricted tokens (legacy tokens, service account keys) that are accepted as well.
type Requirement struct {
	Public bool
	Scopes []string
}

// AllowsScope reports whether a token restricted to scope may call the operation. The empty scope
// (a session token) is always allowed once authenticated.
func (r Requirement) AllowsScope(scope string) bool {
	return scope == "" || slices.Contains(r.Scopes, scope)
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
			for _, t := range op.Tags {
				if t == LegacyTag {
					r.Scopes = append(r.Scopes, auth.ScopeLegacy)
				}
			}
			extra, err := scopesOf(op)
			if err != nil {
				return nil, fmt.Errorf("%s %s: %w", method, path, err)
			}
			for _, sc := range extra {
				if !slices.Contains(r.Scopes, sc) {
					r.Scopes = append(r.Scopes, sc)
				}
			}
			slices.Sort(r.Scopes)
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
			// A scoped token (a legacy token or a service account key) is valid only where the spec
			// lists its scope; everywhere else it is refused even though the signature is good.
			if !req.AllowsScope(principal.Scope) {
				httpx.WriteError(w, http.StatusForbidden, "forbidden", "this token is not valid for this endpoint")
				return
			}
			next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), principal)))
		})
	}
}

// scopesOf reads the operation's x-token-scopes: a list of non-empty strings.
func scopesOf(op *openapi3.Operation) ([]string, error) {
	raw, ok := op.Extensions[ScopesExtension]
	if !ok {
		return nil, nil
	}
	b, err := json.Marshal(raw) // the loader stores the node as raw JSON or as decoded YAML
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ScopesExtension, err)
	}
	var scopes []string
	if err := json.Unmarshal(b, &scopes); err != nil {
		return nil, fmt.Errorf("%s must be a list of strings: %w", ScopesExtension, err)
	}
	for _, sc := range scopes {
		if sc == "" {
			return nil, fmt.Errorf("%s: empty scope", ScopesExtension)
		}
	}
	return scopes, nil
}

func bearerToken(h string) (string, bool) {
	scheme, token, ok := strings.Cut(h, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}
