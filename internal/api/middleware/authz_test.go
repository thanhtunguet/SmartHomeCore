package middleware

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/thanhtunguet/homecore/internal/api/gen"
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

// Only operations tagged `legacy` accept legacy-scoped tokens (ADR-017), and the real spec tags
// exactly the three old endpoints.
func TestPolicyMarksTheLegacyOperationsOnly(t *testing.T) {
	spec, err := gen.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	p, err := PolicyFromSpec(spec)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for k, r := range p {
		if r.Legacy {
			got = append(got, k)
			if r.Public {
				t.Errorf("%s is legacy and public", k)
			}
		}
	}
	sort.Strings(got)
	if want := []string{"GET /api/is-online", "GET /api/turn-off", "GET /api/turn-on"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("legacy operations: %v, want %v", got, want)
	}
}
