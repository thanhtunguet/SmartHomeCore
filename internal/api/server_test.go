package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/thanhtunguet/homecore/internal/auth"
	"github.com/thanhtunguet/homecore/internal/config"
	"github.com/thanhtunguet/homecore/internal/storage"
	"github.com/thanhtunguet/homecore/internal/systemsettings"
)

// tokenAuth maps fixed tokens to principals.
type tokenAuth map[string]auth.Principal

func (t tokenAuth) Authenticate(_ context.Context, tok string) (auth.Principal, error) {
	if p, ok := t[tok]; ok {
		return p, nil
	}
	return auth.Principal{}, auth.ErrUnauthenticated
}

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	settings := systemsettings.New(systemsettings.NewSQLiteRepository(db))
	if err := settings.SetOpenAPIEnabled(ctx, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	srv, err := New(Deps{
		Config:         config.Default().Controller,
		Log:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		DB:             db,
		SystemSettings: settings,
		Authn: tokenAuth{
			"token-a": {UserID: "u1", Username: "alice"},
			"token-b": {UserID: "u2", Username: "bob"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return srv.Handler()
}

func do(h http.Handler, method, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRoutesAndPolicy(t *testing.T) {
	h := newTestHandler(t)
	devID := "0193a0b2-7c4e-7f00-8000-000000000001"

	tests := []struct {
		name, method, path, token string
		want                      int
	}{
		{"healthz public", "GET", "/healthz", "", 200},
		{"readyz public", "GET", "/readyz", "", 200},
		{"spec served", "GET", "/api/openapi.json", "", 200},
		{"swagger ui served offline", "GET", "/api/docs/", "", 200},
		{"swagger asset served", "GET", "/api/docs/swagger-ui-bundle.js", "", 200},
		{"frontend index", "GET", "/", "", 200},
		{"spa fallback", "GET", "/devices/123", "", 200},
		{"unknown api path is json 404", "GET", "/api/v1/nope", "", 404},

		{"protected without token", "GET", "/api/v1/devices", "", 401},
		{"protected with bad token", "GET", "/api/v1/devices", "bogus", 401},
		{"any user can list (not implemented yet)", "GET", "/api/v1/devices", "token-b", 501},
		{"any user can create (not implemented yet)", "POST", "/api/v1/devices", "token-b", 501},
		{"any user can shutdown (not implemented yet)", "POST", "/api/v1/devices/" + devID + "/shutdown", "token-b", 501},
		{"any user can use DDNS (not implemented yet)", "GET", "/api/v1/ddns", "token-b", 501},
		{"cloudflare lookup needs a token", "POST", "/api/v1/ddns/cloudflare/zones", "", 401},
		{"cloudflare lookup (not implemented yet)", "POST", "/api/v1/ddns/cloudflare/zones", "token-a", 501},
		{"login is public (not implemented yet)", "POST", "/api/v1/auth/login", "", 501},
		{"bad uuid path param", "GET", "/api/v1/devices/not-a-uuid", "token-a", 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(h, tt.method, tt.path, tt.token)
			if rec.Code != tt.want {
				t.Fatalf("%s %s → %d, want %d; body=%s", tt.method, tt.path, rec.Code, tt.want, rec.Body)
			}
		})
	}
}

func TestLoopbackPatchMethodOverride(t *testing.T) {
	h := newTestHandler(t)
	request := func(remote string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{}`))
		req.RemoteAddr = remote
		req.Header.Set("X-HomeCore-Method-Override", http.MethodPatch)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if got := request("127.0.0.1:12345").Code; got != http.StatusMethodNotAllowed {
		t.Fatalf("loopback override reached POST login: got %d, want PATCH route 405", got)
	}
	if got := request("192.0.2.1:12345").Code; got != http.StatusNotImplemented {
		t.Fatalf("non-loopback request must remain POST login: got %d, want 501 unimplemented envelope", got)
	}
}

func TestHealthBodyAndSpec(t *testing.T) {
	h := newTestHandler(t)

	var health struct{ Status, Time, Version string }
	if err := json.Unmarshal(do(h, "GET", "/healthz", "").Body.Bytes(), &health); err != nil || health.Status != "ok" || health.Time == "" {
		t.Fatalf("bad health body: %+v %v", health, err)
	}
	var spec struct {
		OpenAPI string         `json:"openapi"`
		Paths   map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(do(h, "GET", "/api/openapi.json", "").Body.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	if spec.OpenAPI == "" || spec.Paths["/api/v1/devices"] == nil {
		t.Fatalf("spec incomplete: openapi=%q paths=%d", spec.OpenAPI, len(spec.Paths))
	}
}

func TestUnimplementedUsesErrorEnvelope(t *testing.T) {
	h := newTestHandler(t)
	rec := do(h, "GET", "/api/v1/devices", "token-a")
	if rec.Code != 501 {
		t.Fatalf("status %d, want 501", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type %q", ct)
	}
	var body struct {
		Error struct{ Code, Message string }
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error.Code != "not_implemented" || body.Error.Message == "" {
		t.Fatalf("bad envelope %q: %+v %v", rec.Body, body, err)
	}
}

func TestRemoveDeviceAgentPolicy(t *testing.T) {
	h := newTestHandler(t)
	p := "/api/v1/devices/0193a0b2-7c4e-7f00-8000-000000000001/agent"
	if got := do(h, "DELETE", p, "").Code; got != 401 {
		t.Fatalf("anonymous → %d, want 401", got)
	}
	if got := do(h, "DELETE", p, "token-a").Code; got != 501 {
		t.Fatalf("user → %d, want 501 (planned)", got)
	}
}

func TestSystemInfo(t *testing.T) {
	h := newTestHandler(t)
	if got := do(h, "GET", "/api/v1/system/info", "").Code; got != 401 {
		t.Fatalf("anonymous → %d, want 401", got)
	}
	rec := do(h, "GET", "/api/v1/system/info", "token-a")
	if rec.Code != 200 {
		t.Fatalf("user → %d: %s", rec.Code, rec.Body)
	}
	var info struct {
		Version, Mode string
		Listen        string
		MonitorDef    struct {
			IntervalSeconds int `json:"interval_seconds"`
			TimeoutMs       int `json:"timeout_ms"`
		} `json:"monitor_defaults"`
		AccessTTL int `json:"access_token_ttl_seconds"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	def := config.Default().Controller
	if info.Version == "" || info.Mode != "controller" || info.Listen != def.Listen ||
		info.MonitorDef.IntervalSeconds != int(def.Monitor.DefaultInterval.Seconds()) ||
		info.AccessTTL != int(def.Auth.AccessTokenTTL.Seconds()) {
		t.Fatalf("unexpected info: %+v", info)
	}
	for _, leak := range []string{"key", "secret", "path", "dir"} {
		if strings.Contains(strings.ToLower(rec.Body.String()), leak) {
			t.Errorf("response mentions %q: %s", leak, rec.Body)
		}
	}
}

func TestStaticCacheHeaders(t *testing.T) {
	files := fstest.MapFS{
		"index.html":    {Data: []byte("<html></html>")},
		"assets/app.js": {Data: []byte("x")},
	}
	h := staticHandler(files)
	for _, tt := range []struct{ path, want string }{
		{"/assets/app.js", "public, max-age=31536000, immutable"},
		{"/", "no-cache"},
		{"/index.html", "no-cache"},
		{"/devices/1", "no-cache"}, // SPA fallback
		{"/assets/missing.js", "no-cache"},
	} {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest("GET", tt.path, nil))
		if got := rec.Header().Get("Cache-Control"); got != tt.want {
			t.Errorf("%s: Cache-Control %q, want %q", tt.path, got, tt.want)
		}
	}
}

// REQ-API-018: one wake/shutdown request per device per second, across users and command types.
func TestDeviceCommandThrottle(t *testing.T) {
	h := newTestHandler(t)
	devA := "0193a0b2-7c4e-7f00-8000-00000000000a"
	devB := "0193a0b2-7c4e-7f00-8000-00000000000b"

	if got := do(h, "POST", "/api/v1/devices/"+devA+"/wake", "token-a").Code; got != 501 {
		t.Fatalf("first wake → %d, want 501 (admitted; handler not implemented yet)", got)
	}
	rec := do(h, "POST", "/api/v1/devices/"+devA+"/wake", "token-b") // another user, same device
	if rec.Code != 429 || rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("second wake → %d Retry-After=%q, want 429 / 1", rec.Code, rec.Header().Get("Retry-After"))
	}
	var body struct {
		Error struct{ Code string }
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error.Code != "rate_limited" {
		t.Fatalf("bad envelope %q: %v", rec.Body, err)
	}
	if got := do(h, "POST", "/api/v1/devices/"+devA+"/shutdown", "token-a").Code; got != 429 {
		t.Fatalf("shutdown right after wake → %d, want 429 (shared per device)", got)
	}
	if got := do(h, "POST", "/api/v1/devices/"+devB+"/wake", "token-a").Code; got != 501 {
		t.Fatalf("other device → %d, want 501 (independent)", got)
	}
	if got := do(h, "POST", "/api/v1/devices/"+devA+"/probe", "token-a").Code; got != 501 {
		t.Fatalf("probe is not a power command → %d, want 501", got)
	}
}

// An anonymous request must not use up a device's slot.
func TestDeviceCommandThrottleAfterAuth(t *testing.T) {
	h := newTestHandler(t)
	p := "/api/v1/devices/0193a0b2-7c4e-7f00-8000-00000000000c/shutdown"
	for range 3 {
		if got := do(h, "POST", p, "").Code; got != 401 {
			t.Fatalf("anonymous → %d, want 401", got)
		}
	}
	if got := do(h, "POST", p, "token-a").Code; got != 501 {
		t.Fatalf("authenticated → %d, want 501 (slot untouched)", got)
	}
}
