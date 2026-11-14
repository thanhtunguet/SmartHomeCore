package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thanhtunguet/homecore/internal/audit"
	"github.com/thanhtunguet/homecore/internal/auth"
	"github.com/thanhtunguet/homecore/internal/config"
	"github.com/thanhtunguet/homecore/internal/secrets"
	"github.com/thanhtunguet/homecore/internal/storage"
)

const pw = "correct horse battery staple"

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type authEnv struct {
	h     http.Handler
	db    *storage.DB
	clk   *clock
	fixed string // when set, every request comes from this client address
	calls int
}

// addr gives each request its own client address (so the per-IP credential rate limit does not
// interfere with tests about something else), unless a test pins one.
func (e *authEnv) addr() string {
	if e.fixed != "" {
		return e.fixed
	}
	e.calls++
	return "192.0.2." + strconv.Itoa(e.calls%250+1) + ":5555"
}

// newAuthEnv builds the real router with the real auth.Service (JWT, refresh tokens, audit log)
// on a temp SQLite database and one account, alice.
func newAuthEnv(t *testing.T) *authEnv {
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
	sealer, err := secrets.NewSealer(bytes.Repeat([]byte{3}, secrets.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	clk := &clock{t: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Default().Controller
	svc, err := auth.NewService(ctx, auth.Config{
		DB: db, Sealer: sealer, Audit: audit.NewWriter(db, clk.Now), Log: log,
		AccessTTL: cfg.Auth.AccessTokenTTL, RefreshTTL: cfg.Auth.RefreshTokenTTL, Now: clk.Now,
		Params: auth.Params{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Accounts().Create(ctx, "alice", pw, "test"); err != nil {
		t.Fatal(err)
	}
	srv, err := New(Deps{Config: cfg, Log: log, DB: db, Auth: svc})
	if err != nil {
		t.Fatal(err)
	}
	return &authEnv{h: srv.Handler(), db: db, clk: clk}
}

func (e *authEnv) post(path string, body any, bearer string) *httptest.ResponseRecorder {
	var rdr io.Reader = http.NoBody
	switch b := body.(type) {
	case nil:
	case string:
		rdr = strings.NewReader(b)
	default:
		raw, _ := json.Marshal(b)
		rdr = bytes.NewReader(raw)
	}
	req := httptest.NewRequest("POST", path, rdr)
	req.RemoteAddr = e.addr()
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func (e *authEnv) get(path, bearer string) *httptest.ResponseRecorder {
	return do(e.h, "GET", path, bearer)
}

type pair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

func (e *authEnv) login(t *testing.T) pair {
	t.Helper()
	rec := e.post("/api/v1/auth/login", map[string]string{"username": "alice", "password": pw}, "")
	if rec.Code != 200 {
		t.Fatalf("login → %d: %s", rec.Code, rec.Body)
	}
	return decodePair(t, rec)
}

func decodePair(t *testing.T, rec *httptest.ResponseRecorder) pair {
	t.Helper()
	var p pair
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("bad token pair %q: %v", rec.Body, err)
	}
	return p
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var b struct {
		Error struct{ Code, Message string }
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil || b.Error.Code == "" || b.Error.Message == "" {
		t.Fatalf("not the error envelope: %q (%v)", rec.Body, err)
	}
	return b.Error.Code
}

// REQ-API-007, REQ-API-022: the contract of POST /auth/login and GET /auth/me.
func TestLoginAndMe(t *testing.T) {
	e := newAuthEnv(t)
	rec := e.post("/api/v1/auth/login", map[string]string{"username": "alice", "password": pw}, "")
	if rec.Code != 200 {
		t.Fatalf("login → %d: %s", rec.Code, rec.Body)
	}
	p := decodePair(t, rec)
	if p.TokenType != "Bearer" || p.ExpiresIn != 900 || p.AccessToken == "" || p.RefreshToken == "" {
		t.Fatalf("unexpected token pair %+v", p)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control %q on a token response", cc)
	}

	me := e.get("/api/v1/auth/me", p.AccessToken)
	if me.Code != 200 {
		t.Fatalf("me → %d: %s", me.Code, me.Body)
	}
	var u struct{ ID, Username string }
	if err := json.Unmarshal(me.Body.Bytes(), &u); err != nil || u.Username != "alice" || len(u.ID) != 36 {
		t.Fatalf("me body %q: %v", me.Body, err)
	}
	if strings.Contains(strings.ToLower(me.Body.String()), "hash") || strings.Contains(me.Body.String(), "role") {
		t.Fatalf("me leaks fields: %s", me.Body)
	}

	if got := e.get("/api/v1/auth/me", "").Code; got != 401 {
		t.Fatalf("me without token → %d", got)
	}
	bad := e.get("/api/v1/auth/me", "garbage")
	if bad.Code != 401 || !strings.Contains(bad.Header().Get("WWW-Authenticate"), "invalid_token") {
		t.Fatalf("me with garbage → %d %q", bad.Code, bad.Header().Get("WWW-Authenticate"))
	}
}

func TestLoginFailureIsGeneric401(t *testing.T) {
	e := newAuthEnv(t)
	wrong := e.post("/api/v1/auth/login", map[string]string{"username": "alice", "password": "wrong wrong wrong"}, "")
	unknown := e.post("/api/v1/auth/login", map[string]string{"username": "nobody", "password": pw}, "")
	for name, rec := range map[string]*httptest.ResponseRecorder{"wrong password": wrong, "unknown user": unknown} {
		if rec.Code != 401 || errCode(t, rec) != "unauthorized" {
			t.Fatalf("%s → %d %s", name, rec.Code, rec.Body)
		}
	}
	if wrong.Body.String() != unknown.Body.String() {
		t.Fatalf("responses differ and leak which part was wrong:\n%s\n%s", wrong.Body, unknown.Body)
	}
	if !strings.Contains(wrong.Body.String(), `"message":"invalid username or password"`) {
		t.Fatalf("message not generic: %s", wrong.Body)
	}
}

func TestLoginRejectsMalformedRequests(t *testing.T) {
	e := newAuthEnv(t)
	for name, body := range map[string]any{
		"not json":         "{nope",
		"empty body":       "",
		"empty password":   map[string]string{"username": "alice", "password": ""},
		"empty username":   map[string]string{"username": "", "password": pw},
		"long password":    map[string]string{"username": "alice", "password": strings.Repeat("a", 257)},
		"long username":    map[string]string{"username": strings.Repeat("a", 65), "password": pw},
		"trailing garbage": `{"username":"alice","password":"x"} {}`,
	} {
		rec := e.post("/api/v1/auth/login", body, "")
		if rec.Code != 400 || errCode(t, rec) != "bad_request" {
			t.Errorf("%s → %d %s", name, rec.Code, rec.Body)
		}
	}
}

// REQ-API-012: credential endpoints are rate limited per IP (10/min, burst 5 by default).
func TestLoginRateLimit(t *testing.T) {
	e := newAuthEnv(t)
	e.fixed = "192.0.2.10:5555"
	var codes []int
	var last *httptest.ResponseRecorder
	for range 8 {
		last = e.post("/api/v1/auth/login", map[string]string{"username": "alice", "password": "wrong wrong wrong"}, "")
		codes = append(codes, last.Code)
	}
	if codes[0] != 401 || codes[4] != 401 || codes[5] != 429 || codes[7] != 429 {
		t.Fatalf("status sequence %v, want 401 x5 then 429", codes)
	}
	if last.Header().Get("Retry-After") == "" || errCode(t, last) != "rate_limited" {
		t.Fatalf("429 lacks Retry-After/envelope: %v %s", last.Header(), last.Body)
	}
	// Even the right password is refused while limited, but ordinary API calls are not affected.
	if got := e.post("/api/v1/auth/login", map[string]string{"username": "alice", "password": pw}, "").Code; got != 429 {
		t.Fatalf("login while limited → %d", got)
	}
	if got := e.get("/healthz", "").Code; got != 200 {
		t.Fatalf("healthz while login-limited → %d", got)
	}
	// Another client address has its own budget.
	e.fixed = "198.51.100.77:1234"
	if got := e.post("/api/v1/auth/login", map[string]string{"username": "alice", "password": pw}, "").Code; got != 200 {
		t.Fatalf("other IP → %d", got)
	}
}

// Protected, not-yet-implemented operations: 401 without a token, 501 with a real one.
func TestProtectedRoutesWithRealAuthenticator(t *testing.T) {
	e := newAuthEnv(t)
	p := e.login(t)
	devID := "0193a0b2-7c4e-7f00-8000-000000000001"
	for _, tt := range []struct{ method, path string }{
		{"POST", "/api/v1/devices"},
		{"GET", "/api/v1/devices"},
		{"GET", "/api/v1/devices/" + devID},
		{"POST", "/api/v1/devices/" + devID + "/shutdown"},
		{"GET", "/api/v1/ddns"},
		{"GET", "/api/v1/commands"},
		{"GET", "/api/v1/system/metrics"},
	} {
		if got := do(e.h, tt.method, tt.path, "").Code; got != 401 {
			t.Errorf("%s %s without token → %d, want 401", tt.method, tt.path, got)
		}
		rec := do(e.h, tt.method, tt.path, p.AccessToken)
		if rec.Code != 501 || errCode(t, rec) != "not_implemented" {
			t.Errorf("%s %s with token → %d %s, want 501 envelope", tt.method, tt.path, rec.Code, rec.Body)
		}
	}
	// /system/info is implemented and takes the real token.
	if got := e.get("/api/v1/system/info", p.AccessToken).Code; got != 200 {
		t.Errorf("system/info with real token → %d", got)
	}
	// Public endpoints stay public.
	if got := e.get("/healthz", "").Code; got != 200 {
		t.Errorf("healthz → %d", got)
	}
}

func TestAccessTokenExpiryAndRefresh(t *testing.T) {
	e := newAuthEnv(t)
	p := e.login(t)

	e.clk.Advance(14*time.Minute + 59*time.Second)
	if got := e.get("/api/v1/auth/me", p.AccessToken).Code; got != 200 {
		t.Fatalf("1 s before expiry → %d", got)
	}
	e.clk.Advance(time.Second)
	rec := e.get("/api/v1/auth/me", p.AccessToken)
	if rec.Code != 401 || !strings.Contains(rec.Header().Get("WWW-Authenticate"), "invalid_token") || errCode(t, rec) != "unauthorized" {
		t.Fatalf("expired access token → %d %q %s", rec.Code, rec.Header().Get("WWW-Authenticate"), rec.Body)
	}

	ref := e.post("/api/v1/auth/refresh", map[string]string{"refresh_token": p.RefreshToken}, "")
	if ref.Code != 200 {
		t.Fatalf("refresh → %d: %s", ref.Code, ref.Body)
	}
	np := decodePair(t, ref)
	if np.RefreshToken == p.RefreshToken || np.TokenType != "Bearer" || np.ExpiresIn != 900 {
		t.Fatalf("bad refreshed pair %+v", np)
	}
	if got := e.get("/api/v1/auth/me", np.AccessToken).Code; got != 200 {
		t.Fatalf("refreshed access token → %d", got)
	}
}

func TestRefreshGraceWindowAndReuseThroughRouter(t *testing.T) {
	e := newAuthEnv(t)
	p := e.login(t)
	a := e.post("/api/v1/auth/refresh", map[string]string{"refresh_token": p.RefreshToken}, "")
	if a.Code != 200 {
		t.Fatalf("refresh → %d %s", a.Code, a.Body)
	}
	e.clk.Advance(2 * time.Second)
	b := e.post("/api/v1/auth/refresh", map[string]string{"refresh_token": p.RefreshToken}, "")
	if b.Code != 200 {
		t.Fatalf("second tab inside the grace window → %d %s", b.Code, b.Body)
	}
	pa, pb := decodePair(t, a), decodePair(t, b)
	if pa.RefreshToken != pb.RefreshToken {
		t.Fatal("grace window must return the same refresh token to both tabs")
	}

	e.clk.Advance(20 * time.Second)
	c := e.post("/api/v1/auth/refresh", map[string]string{"refresh_token": p.RefreshToken}, "")
	if c.Code != 401 || errCode(t, c) != "unauthorized" {
		t.Fatalf("reuse outside the window → %d %s", c.Code, c.Body)
	}
	// The family is revoked: the successor no longer works either.
	d := e.post("/api/v1/auth/refresh", map[string]string{"refresh_token": pa.RefreshToken}, "")
	if d.Code != 401 {
		t.Fatalf("successor after reuse detection → %d", d.Code)
	}
}

func TestLogoutThroughRouter(t *testing.T) {
	e := newAuthEnv(t)
	p := e.login(t)

	if got := e.post("/api/v1/auth/logout", map[string]string{"refresh_token": p.RefreshToken}, "").Code; got != 401 {
		t.Fatalf("logout without bearer → %d", got)
	}
	rec := e.post("/api/v1/auth/logout", map[string]string{"refresh_token": p.RefreshToken}, p.AccessToken)
	if rec.Code != 204 || rec.Body.Len() != 0 {
		t.Fatalf("logout → %d %q", rec.Code, rec.Body)
	}
	old := e.post("/api/v1/auth/refresh", map[string]string{"refresh_token": p.RefreshToken}, "")
	if old.Code != 401 || errCode(t, old) != "unauthorized" {
		t.Fatalf("refresh after logout → %d %s", old.Code, old.Body)
	}
}

func TestRefreshRejectsBadInput(t *testing.T) {
	e := newAuthEnv(t)
	if rec := e.post("/api/v1/auth/refresh", map[string]string{"refresh_token": "nope"}, ""); rec.Code != 401 || errCode(t, rec) != "unauthorized" {
		t.Fatalf("unknown token → %d %s", rec.Code, rec.Body)
	}
	if rec := e.post("/api/v1/auth/refresh", `{}`, ""); rec.Code != 400 {
		t.Fatalf("missing token → %d", rec.Code)
	}
	if rec := e.post("/api/v1/auth/refresh", `nonsense`, ""); rec.Code != 400 {
		t.Fatalf("bad json → %d", rec.Code)
	}
}

func TestDisabledUserLosesAccessImmediately(t *testing.T) {
	e := newAuthEnv(t)
	p := e.login(t)
	if _, err := e.db.Exec(`UPDATE users SET disabled = 1`); err != nil {
		t.Fatal(err)
	}
	if got := e.get("/api/v1/auth/me", p.AccessToken).Code; got != 401 {
		t.Fatalf("access token of a disabled user → %d, want 401 at once", got)
	}
	if got := e.post("/api/v1/auth/refresh", map[string]string{"refresh_token": p.RefreshToken}, "").Code; got != 401 {
		t.Fatalf("refresh of a disabled user → %d", got)
	}
}

func TestLoginWritesAuditRows(t *testing.T) {
	e := newAuthEnv(t)
	e.fixed = "192.0.2.10:5555"
	e.post("/api/v1/auth/login", map[string]string{"username": "alice", "password": "wrong wrong wrong"}, "")
	e.login(t)
	rows, err := e.db.Query(`SELECT actor, result, remote_addr FROM audit_log WHERE action = 'auth.login' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var a, r, ip string
		rows.Scan(&a, &r, &ip)
		got = append(got, a+"/"+r+"/"+ip)
	}
	want := []string{"alice/failed/192.0.2.10", "alice/ok/192.0.2.10"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("audit rows %v, want %v", got, want)
	}
}
