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

	"github.com/thanhtunguet/homecore/internal/auth"
	"github.com/thanhtunguet/homecore/internal/config"
	"github.com/thanhtunguet/homecore/internal/storage"
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
	srv, err := New(Deps{
		Config: config.Default().Controller,
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		DB:     db,
		Authn: tokenAuth{
			"admin-token":  {UserID: "u1", Username: "admin", Role: auth.RoleAdmin},
			"viewer-token": {UserID: "u2", Username: "viewer", Role: auth.RoleViewer},
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
		{"viewer can list (not implemented yet)", "GET", "/api/v1/devices", "viewer-token", 501},
		{"viewer cannot create", "POST", "/api/v1/devices", "viewer-token", 403},
		{"viewer cannot shutdown", "POST", "/api/v1/devices/" + devID + "/shutdown", "viewer-token", 403},
		{"admin passes policy for shutdown", "POST", "/api/v1/devices/" + devID + "/shutdown", "admin-token", 501},
		{"login is public (not implemented yet)", "POST", "/api/v1/auth/login", "", 501},
		{"bad uuid path param", "GET", "/api/v1/devices/not-a-uuid", "viewer-token", 400},
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
	rec := do(h, "GET", "/api/v1/devices", "viewer-token")
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
	if got := do(h, "DELETE", p, "viewer-token").Code; got != 403 {
		t.Fatalf("viewer → %d, want 403", got)
	}
	if got := do(h, "DELETE", p, "admin-token").Code; got != 501 {
		t.Fatalf("admin → %d, want 501 (planned)", got)
	}
}

func TestSystemInfo(t *testing.T) {
	h := newTestHandler(t)
	if got := do(h, "GET", "/api/v1/system/info", "").Code; got != 401 {
		t.Fatalf("anonymous → %d, want 401", got)
	}
	rec := do(h, "GET", "/api/v1/system/info", "viewer-token")
	if rec.Code != 200 {
		t.Fatalf("viewer → %d: %s", rec.Code, rec.Body)
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
