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
