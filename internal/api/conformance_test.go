package api

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"

	"github.com/thanhtunguet/homecore/internal/api/gen"
)

// specChecker validates every response of the implemented operations against api/openapi.yaml,
// the contract the frontend's types are generated from, so handler output cannot drift from it:
// status codes that the operation does not declare, missing required properties, wrong types,
// malformed uuid / date-time values and unknown enum values all fail the test that made the call.
type specChecker struct{ router routers.Router }

var defineFormats sync.Once

func newSpecChecker(t testing.TB) *specChecker {
	t.Helper()
	defineFormats.Do(func() {
		// kin-openapi validates date-time out of the box; uuid is opt-in.
		openapi3.DefineStringFormat("uuid", openapi3.FormatOfStringForUUIDOfRFC9562)
	})
	doc, err := gen.GetSpec()
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}
	router, err := legacy.NewRouter(doc)
	if err != nil {
		t.Fatalf("spec router: %v", err)
	}
	return &specChecker{router: router}
}

// check fails t if rec is not a valid response of the operation req addressed.
func (c *specChecker) check(t testing.TB, req *http.Request, rec *httptest.ResponseRecorder) {
	t.Helper()
	route, pathParams, err := c.router.FindRoute(req)
	if err != nil {
		t.Errorf("%s %s: no such operation in the spec: %v", req.Method, req.URL.Path, err)
		return
	}
	in := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: &openapi3filter.RequestValidationInput{Request: req, PathParams: pathParams, Route: route},
		Status:                 rec.Code,
		Header:                 rec.Header(),
		Options:                &openapi3filter.Options{IncludeResponseStatus: true, MultiError: true},
	}
	in.SetBodyBytes(rec.Body.Bytes())
	if err := openapi3filter.ValidateResponse(context.Background(), in); err != nil {
		t.Errorf("%s %s → %d does not conform to api/openapi.yaml:\n%v\nbody: %s", req.Method, req.URL.RequestURI(), rec.Code, err, rec.Body)
	}
}

// TestSpecCheckerCatchesDrift keeps the checker honest: a checker that accepts everything would make
// every conformance assertion in this package worthless.
func TestSpecCheckerCatchesDrift(t *testing.T) {
	c := newSpecChecker(t)
	req := func(method, path string) *http.Request {
		return httptest.NewRequest(method, path, http.NoBody)
	}
	rec := func(code int, body string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		r.Header().Set("Content-Type", "application/json")
		r.WriteHeader(code)
		_, _ = io.Copy(r.Body, bytes.NewBufferString(body))
		return r
	}
	const dev = "0193a0b2-7c4e-7f00-8000-000000000001"
	good := `{"id":"` + dev + `","name":"pc","ip_address":"10.0.0.1","monitor_port":22,"monitor_interval_seconds":30,
		"monitor_timeout_ms":2000,"enabled":true,"status":"online","power_transition":"none",
		"created_at":"2026-11-14T10:00:00Z","updated_at":"2026-11-14T10:00:00Z"}`

	bad := map[string]*httptest.ResponseRecorder{
		"missing required power_transition": rec(200, `{"id":"`+dev+`","name":"pc","ip_address":"10.0.0.1","monitor_port":22,"monitor_interval_seconds":30,"monitor_timeout_ms":2000,"enabled":true,"status":"online","created_at":"2026-11-14T10:00:00Z","updated_at":"2026-11-14T10:00:00Z"}`),
		"enum value outside the spec":       rec(200, `{"id":"`+dev+`","name":"pc","ip_address":"10.0.0.1","monitor_port":22,"monitor_interval_seconds":30,"monitor_timeout_ms":2000,"enabled":true,"status":"sleeping","power_transition":"none","created_at":"2026-11-14T10:00:00Z","updated_at":"2026-11-14T10:00:00Z"}`),
		"bad date-time":                     rec(200, `{"id":"`+dev+`","name":"pc","ip_address":"10.0.0.1","monitor_port":22,"monitor_interval_seconds":30,"monitor_timeout_ms":2000,"enabled":true,"status":"online","power_transition":"none","created_at":"yesterday","updated_at":"2026-11-14T10:00:00Z"}`),
		"bad uuid":                          rec(200, `{"id":"not-a-uuid","name":"pc","ip_address":"10.0.0.1","monitor_port":22,"monitor_interval_seconds":30,"monitor_timeout_ms":2000,"enabled":true,"status":"online","power_transition":"none","created_at":"2026-11-14T10:00:00Z","updated_at":"2026-11-14T10:00:00Z"}`),
		"wrong type":                        rec(200, `{"id":"`+dev+`","name":7,"ip_address":"10.0.0.1","monitor_port":22,"monitor_interval_seconds":30,"monitor_timeout_ms":2000,"enabled":true,"status":"online","power_transition":"none","created_at":"2026-11-14T10:00:00Z","updated_at":"2026-11-14T10:00:00Z"}`),
		"undeclared status code":            rec(418, `{"error":{"code":"teapot","message":"x"}}`),
		"error envelope without code":       rec(404, `{"error":{"message":"x"}}`),
	}
	for name, r := range bad {
		t.Run(name, func(t *testing.T) {
			ft := &recordingT{TB: t}
			c.check(ft, req("GET", "/api/v1/devices/"+dev), r)
			if !ft.failed {
				t.Fatal("the checker accepted a response that violates the spec")
			}
		})
	}
	t.Run("valid response passes", func(t *testing.T) {
		ft := &recordingT{TB: t}
		c.check(ft, req("GET", "/api/v1/devices/"+dev), rec(200, good))
		c.check(ft, req("GET", "/api/v1/devices/"+dev), rec(404, `{"error":{"code":"not_found","message":"x"}}`))
		if ft.failed {
			t.Fatal("the checker rejected valid responses")
		}
	})
}

// recordingT turns Errorf into a flag, so a test can assert that a check fails.
type recordingT struct {
	testing.TB
	failed bool
}

func (r *recordingT) Errorf(string, ...any) { r.failed = true }
