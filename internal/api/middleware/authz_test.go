package middleware

import (
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/thanhtunguet/homecore/internal/api/gen"
	"github.com/thanhtunguet/homecore/internal/auth"
)

func policyFor(t *testing.T, security string) (Policy, error) {
	t.Helper()
	spec, err := openapi3.NewLoader().LoadFromData([]byte(`
openapi: 3.0.3
info: {title: t, version: "1"}
security:
  - bearerAuth: []
paths:
  /a:
    get:
      responses: {"200": {description: ok}}
  /b:
    get:
      security: []
      responses: {"200": {description: ok}}
  /c:
    get:
      security:
` + security + `
      responses: {"200": {description: ok}}
components:
  securitySchemes:
    bearerAuth: {type: http, scheme: bearer}
`))
	if err != nil {
		t.Fatal(err)
	}
	return PolicyFromSpec(spec)
}

func TestPolicyFromSpec(t *testing.T) {
	p, err := policyFor(t, "        - bearerAuth: []")
	if err != nil {
		t.Fatal(err)
	}
	if p["GET /a"].Public || !p["GET /b"].Public || p["GET /c"].Public {
		t.Fatalf("unexpected policy: %+v", p)
	}
}

// With no roles, a leftover `bearerAuth: [admin]` must abort startup rather than be ignored.
func TestPolicyFromSpecRejectsScopes(t *testing.T) {
	_, err := policyFor(t, "        - bearerAuth: [admin]")
	if err == nil || !strings.Contains(err.Error(), "no roles") {
		t.Fatalf("want a 'no roles' error, got %v", err)
	}
}

func TestPolicyFromSpecRejectsUnknownScheme(t *testing.T) {
	if _, err := policyFor(t, "        - apiKey: []"); err == nil {
		t.Fatal("unknown scheme accepted")
	}
}

func realPolicy(t *testing.T) Policy {
	t.Helper()
	spec, err := gen.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	p, err := PolicyFromSpec(spec)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func operationsAllowing(p Policy, scope string) []string {
	var got []string
	for k, r := range p {
		if scope != "" && slices.Contains(r.Scopes, scope) {
			got = append(got, k)
		}
	}
	sort.Strings(got)
	return got
}

// The legacy scope (HS256 legacy tokens, ADR-017, and legacy service account keys, ADR-018) is
// accepted on exactly the three old endpoints plus the key's own rotation.
func TestPolicyMarksTheLegacyOperationsOnly(t *testing.T) {
	p := realPolicy(t)
	got := operationsAllowing(p, auth.ScopeLegacy)
	want := []string{"GET /api/is-online", "GET /api/turn-off", "GET /api/turn-on", "POST /api/v1/auth/service-key/rotate"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("legacy operations: %v, want %v", got, want)
	}
	for _, k := range got {
		if p[k].Public {
			t.Errorf("%s is scoped and public", k)
		}
	}
}

// The mcp scope is an allowlist for AI agents: device control and read-only monitoring, never
// device/DDNS configuration, keys or accounts. A new operation is session-only until it is listed.
func TestPolicyMCPScopeIsAnAllowlist(t *testing.T) {
	p := realPolicy(t)
	got := operationsAllowing(p, "mcp")
	for _, k := range []string{"GET /api/v1/devices", "POST /api/v1/devices/{id}/wake", "POST /api/v1/devices/{id}/shutdown",
		"GET /api/v1/commands/{id}", "POST /api/v1/auth/service-key/rotate"} {
		if !slices.Contains(got, k) {
			t.Errorf("mcp scope should allow %s", k)
		}
	}
	for _, k := range got {
		for _, banned := range []string{"/ddns", "/system/controller-key", "/service-account-keys"} {
			if strings.Contains(k, banned) {
				t.Errorf("mcp scope must not allow %s", k)
			}
		}
		if k == "POST /api/v1/devices" || strings.HasPrefix(k, "PATCH ") || strings.HasPrefix(k, "DELETE ") {
			t.Errorf("mcp scope must not allow %s", k)
		}
	}
}

func TestPolicyKeyManagementIsSessionOnly(t *testing.T) {
	p := realPolicy(t)
	for _, k := range []string{"POST /api/v1/service-account-keys", "GET /api/v1/service-account-keys",
		"DELETE /api/v1/service-account-keys/{id}"} {
		r, ok := p[k]
		if !ok {
			t.Fatalf("no policy for %s", k)
		}
		if r.Public || len(r.Scopes) != 0 || r.AllowsScope("mcp") || r.AllowsScope(auth.ScopeLegacy) {
			t.Errorf("%s must accept session tokens only: %+v", k, r)
		}
		if !r.AllowsScope("") {
			t.Errorf("%s must accept session tokens", k)
		}
	}
}

func TestScopesExtensionIsValidated(t *testing.T) {
	spec := func(ext string) (Policy, error) {
		s, err := openapi3.NewLoader().LoadFromData([]byte(`
openapi: 3.0.3
info: {title: t, version: "1"}
security:
  - bearerAuth: []
paths:
  /a:
    get:
      ` + ext + `
      responses: {"200": {description: ok}}
`))
		if err != nil {
			t.Fatal(err)
		}
		return PolicyFromSpec(s)
	}
	p, err := spec("x-token-scopes: [mcp, legacy]")
	if err != nil || !reflect.DeepEqual(p["GET /a"].Scopes, []string{"legacy", "mcp"}) {
		t.Fatalf("valid extension: %+v %v", p, err)
	}
	for _, bad := range []string{`x-token-scopes: mcp`, `x-token-scopes: [""]`, `x-token-scopes: [1]`} {
		if _, err := spec(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
