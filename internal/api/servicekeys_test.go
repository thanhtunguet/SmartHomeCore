package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/thanhtunguet/homecore/internal/api/gen"
	"github.com/thanhtunguet/homecore/internal/servicekey"
)

// REQ-API-029..031, REQ-AUTH-021..027, ADR-018: service account keys through the real router.

func (e *devEnv) mkKey(scope string, ttlSeconds int) gen.ServiceAccountKeyCreated {
	e.t.Helper()
	body := map[string]any{"name": "test key", "scope": scope}
	if ttlSeconds != 0 {
		body["ttl_seconds"] = ttlSeconds
	}
	rec := e.as("POST", "/api/v1/service-account-keys", body)
	want(e.t, rec, 201)
	return decode[gen.ServiceAccountKeyCreated](e.t, rec)
}

func TestServiceKeyIsCreatedOnceWithAOneYearDefault(t *testing.T) {
	e := newDevEnv(t)
	rec := e.as("POST", "/api/v1/service-account-keys", map[string]any{"name": "claude mcp", "scope": "mcp"})
	want(t, rec, 201)
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("a response carrying a key must not be cacheable: %v", rec.Header())
	}
	k := decode[gen.ServiceAccountKeyCreated](t, rec)
	if k.Key == "" || k.Name != "claude mcp" || k.Scope != gen.ServiceAccountKeyScope("mcp") || k.Status != "active" || k.TtlSeconds != 365*24*3600 {
		t.Fatalf("created: %+v", k)
	}
	if d := time.Until(k.ExpiresAt) - 365*24*time.Hour; d > time.Minute || d < -time.Minute {
		t.Fatalf("expires_at %v is not a year away", k.ExpiresAt)
	}

	// The list never shows the key again; it shows the metadata.
	list := e.as("GET", "/api/v1/service-account-keys", nil)
	want(t, list, 200)
	if strings.Contains(list.Body.String(), k.Key) || strings.Contains(list.Body.String(), `"key"`) {
		t.Fatalf("list leaks the key: %s", list.Body)
	}
	items := decode[struct{ Items []gen.ServiceAccountKey }](t, list).Items
	if len(items) != 1 || items[0].Id != k.Id || items[0].Status != "active" || items[0].LastUsedAt != nil {
		t.Fatalf("list: %+v", items)
	}

	// A chosen ttl; out-of-range ones and bad input are 400.
	if k2 := e.mkKey("legacy", 3600); k2.TtlSeconds != 3600 {
		t.Fatalf("ttl: %+v", k2)
	}
	for _, body := range []any{
		map[string]any{"name": "x", "scope": "mcp", "ttl_seconds": 5},
		map[string]any{"name": "x", "scope": "mcp", "ttl_seconds": 999999999999},
		map[string]any{"name": "x", "scope": "admin"},
		map[string]any{"name": "", "scope": "mcp"},
		map[string]any{"name": strings.Repeat("n", 65), "scope": "mcp"},
		map[string]any{"scope": "mcp"},
		`{"name":`,
	} {
		want(t, e.as("POST", "/api/v1/service-account-keys", body), 400)
	}
	want(t, e.call("POST", "/api/v1/service-account-keys", map[string]any{"name": "x", "scope": "mcp"}, ""), 401)
}

// What a key may call is decided by its scope and the spec's x-token-scopes.
func TestServiceKeyScopesAreEnforced(t *testing.T) {
	e := newDevEnv(t)
	d := e.mkDevice(map[string]any{"name": "Gaming PC", "monitor_port": 3333, "mac_address": "AA:BB:CC:DD:EE:FF", "wol_broadcast_address": "192.168.1.255"})
	mcp, legacy := e.mkKey("mcp", 0).Key, e.mkKey("legacy", 0).Key
	api := func(token, method, path string, body any) int { return e.call(method, path, body, token).Code }

	// An mcp key controls and monitors devices, as the owning user.
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v1/devices"}, {"GET", "/api/v1/devices/" + d.Id.String()}, {"GET", "/api/v1/devices/" + d.Id.String() + "/history"},
		{"GET", "/api/v1/commands"}, {"GET", "/api/v1/activity"}, {"GET", "/api/v1/system/info"}, {"GET", "/api/v1/auth/me"},
	} {
		if got := api(mcp, c.method, c.path, nil); got != 200 {
			t.Errorf("mcp key: %s %s = %d, want 200", c.method, c.path, got)
		}
	}
	me := decode[gen.User](t, e.call("GET", "/api/v1/auth/me", nil, mcp))
	if me.Username != "alice" || me.Id.String() != e.userID {
		t.Fatalf("a key must act as its owner: %+v", me)
	}
	want(t, e.call("POST", "/api/v1/devices/"+d.Id.String()+"/wake", nil, mcp), 202)
	if got := api(mcp, "GET", legacyURL("is-online", "gaming pc"), nil); got != 200 {
		t.Errorf("mcp key on a legacy endpoint: %d", got)
	}

	// ...but cannot configure anything, touch DDNS, the controller key, or manage keys.
	for _, c := range []struct{ method, path string }{
		{"POST", "/api/v1/devices"}, {"DELETE", "/api/v1/devices/" + d.Id.String()}, {"PATCH", "/api/v1/devices/" + d.Id.String()},
		{"GET", "/api/v1/ddns"}, {"GET", "/api/v1/system/controller-key"},
		{"GET", "/api/v1/service-account-keys"}, {"POST", "/api/v1/service-account-keys"},
		{"DELETE", "/api/v1/service-account-keys/" + uuid.NewString()}, {"POST", "/api/v1/auth/logout"},
	} {
		rec := e.callRaw(c.method, c.path, map[string]any{"name": "x", "scope": "mcp", "refresh_token": "x"}, mcp)
		if rec.Code != 403 || errCode(t, rec) != "forbidden" {
			t.Errorf("mcp key: %s %s = %d %s, want 403 forbidden", c.method, c.path, rec.Code, rec.Body)
		}
	}

	// A legacy key reaches the three legacy endpoints only.
	settle()
	if got := api(legacy, "GET", legacyURL("turn-on", "gaming pc"), nil); got != 200 {
		t.Errorf("legacy key turn-on: %d", got)
	}
	for _, p := range []string{"/api/v1/devices", "/api/v1/auth/me", "/api/v1/commands"} {
		if got := api(legacy, "GET", p, nil); got != 403 {
			t.Errorf("legacy key GET %s = %d, want 403", p, got)
		}
	}
	if got := api(legacy, "POST", "/api/v1/devices/"+d.Id.String()+"/wake", nil); got != 403 {
		t.Errorf("legacy key on wakeDevice: %d, want 403", got)
	}
}

func TestServiceKeyActionsAreAuditedAsTheOwnerAndNameTheKey(t *testing.T) {
	e := newDevEnv(t)
	d := e.mkDevice(map[string]any{"name": "tv", "monitor_port": 5555, "mac_address": "AA:BB:CC:DD:EE:01", "wol_broadcast_address": "192.168.1.255"})
	k := e.mkKey("mcp", 0)
	want(t, e.call("POST", "/api/v1/devices/"+d.Id.String()+"/wake", nil, k.Key), 202)

	rows := e.auditRows("device.wake")
	if len(rows) != 1 || rows[0].actor != "alice" || !strings.Contains(rows[0].details, `"service_key_id":"`+k.Id.String()+`"`) {
		t.Fatalf("wake audit: %+v", rows)
	}
	create := e.auditRows("service_key.create")
	if len(create) != 1 || create[0].actor != "alice" || create[0].target != k.Id.String() || strings.Contains(create[0].details, k.Key) {
		t.Fatalf("create audit: %+v", create)
	}
	if strings.Contains(e.logs.String(), k.Key) {
		t.Fatal("the key reached the log")
	}
}

func TestServiceKeyRevocationIsImmediate(t *testing.T) {
	e := newDevEnv(t)
	k := e.mkKey("mcp", 0)
	want(t, e.call("GET", "/api/v1/devices", nil, k.Key), 200)

	want(t, e.as("DELETE", "/api/v1/service-account-keys/"+k.Id.String(), nil), 204)
	rec := e.call("GET", "/api/v1/devices", nil, k.Key)
	want(t, rec, 401)
	if errCode(t, rec) != "unauthorized" {
		t.Fatalf("body: %s", rec.Body)
	}
	want(t, e.as("DELETE", "/api/v1/service-account-keys/"+k.Id.String(), nil), 204) // idempotent
	want(t, e.as("DELETE", "/api/v1/service-account-keys/"+uuid.NewString(), nil), 404)
	want(t, e.callRaw("DELETE", "/api/v1/service-account-keys/not-a-uuid", nil, e.token), 400)

	items := decode[struct{ Items []gen.ServiceAccountKey }](t, e.as("GET", "/api/v1/service-account-keys", nil)).Items
	if len(items) != 1 || items[0].Status != "revoked" {
		t.Fatalf("list: %+v", items)
	}
	if len(e.auditRows("service_key.revoke")) != 1 {
		t.Fatal("revocation not audited exactly once")
	}
}

func TestServiceKeysOfOtherUsersAreInvisible(t *testing.T) {
	e := newDevEnv(t)
	bob := uuid.NewString()
	if _, err := e.db.Exec(`INSERT INTO users (id, username, password_hash, disabled, created_at, updated_at) VALUES (?, 'bob', 'x', 0, 1, 1)`, bob); err != nil {
		t.Fatal(err)
	}
	bobKey, err := e.keys.Create(t.Context(), servicekeyCaller(bob, "bob"), "bobs", "mcp", 0)
	if err != nil {
		t.Fatal(err)
	}
	mine := e.mkKey("mcp", 0)

	items := decode[struct{ Items []gen.ServiceAccountKey }](t, e.as("GET", "/api/v1/service-account-keys", nil)).Items
	if len(items) != 1 || items[0].Id != mine.Id {
		t.Fatalf("alice sees %+v", items)
	}
	want(t, e.as("DELETE", "/api/v1/service-account-keys/"+bobKey.Key.ID, nil), 404)
	want(t, e.call("GET", "/api/v1/devices", nil, bobKey.Credential), 200) // still alive
	me := decode[gen.User](t, e.call("GET", "/api/v1/auth/me", nil, bobKey.Credential))
	if me.Username != "bob" {
		t.Fatalf("bob's key acts as %q", me.Username)
	}
}

func TestServiceKeyIsRefusedWhenItsOwnerIsDisabledOrTheCredentialIsForged(t *testing.T) {
	e := newDevEnv(t)
	k := e.mkKey("mcp", 0)
	want(t, e.call("GET", "/api/v1/devices", nil, k.Key), 200)

	parts := strings.Split(k.Key, ".")
	flipped := parts[2][:len(parts[2])-2] + map[bool]string{true: "AA", false: "BB"}[!strings.HasSuffix(parts[2], "AA")]
	for name, tok := range map[string]string{
		"bad signature": parts[0] + "." + parts[1] + "." + flipped,
		"no signature":  parts[0] + "." + parts[1] + ".",
		"hs256 header":  "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9." + parts[1] + "." + parts[2],
	} {
		if rec := e.call("GET", "/api/v1/devices", nil, tok); rec.Code != 401 {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}

	if _, err := e.db.Exec(`UPDATE users SET disabled = 1 WHERE id = ?`, e.userID); err != nil {
		t.Fatal(err)
	}
	want(t, e.call("GET", "/api/v1/devices", nil, k.Key), 401)
}

func TestServiceKeyRotationThroughTheAPI(t *testing.T) {
	e := newDevEnv(t)
	old := e.mkKey("mcp", 7*24*3600)
	want(t, e.call("GET", "/api/v1/devices", nil, old.Key), 200)

	rec := e.call("POST", "/api/v1/auth/service-key/rotate", nil, old.Key) // no body is fine
	want(t, rec, 200)
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("headers: %v", rec.Header())
	}
	next := decode[gen.ServiceAccountKeyCreated](t, rec)
	if next.Key == "" || next.Key == old.Key || next.Id == old.Id || next.Name != old.Name || next.Scope != old.Scope ||
		next.TtlSeconds != 7*24*3600 || next.Status != "active" {
		t.Fatalf("rotated key: %+v", next)
	}
	if next.PreviousKeyId == nil || *next.PreviousKeyId != old.Id || next.PreviousKeyValidUntil == nil ||
		time.Until(*next.PreviousKeyValidUntil) > time.Minute+5*time.Second || time.Until(*next.PreviousKeyValidUntil) < 30*time.Second {
		t.Fatalf("previous key fields: %+v", next)
	}
	want(t, e.call("GET", "/api/v1/devices", nil, next.Key), 200)

	// The old key is superseded: it still works for the grace window, and shows as rotated.
	want(t, e.call("GET", "/api/v1/devices", nil, old.Key), 200)
	items := decode[struct{ Items []gen.ServiceAccountKey }](t, e.as("GET", "/api/v1/service-account-keys", nil)).Items
	if len(items) != 2 {
		t.Fatalf("list: %+v", items)
	}
	byID := map[uuid.UUID]gen.ServiceAccountKey{items[0].Id: items[0], items[1].Id: items[1]}
	if byID[old.Id].Status != "rotated" || byID[old.Id].RotatedTo == nil || *byID[old.Id].RotatedTo != next.Id || byID[next.Id].Status != "active" {
		t.Fatalf("statuses: %+v", items)
	}

	// A retry with the old key (a lost response) returns the same new key and creates nothing.
	again := decode[gen.ServiceAccountKeyCreated](t, e.call("POST", "/api/v1/auth/service-key/rotate", nil, old.Key))
	if again.Id != next.Id || again.Key != next.Key {
		t.Fatalf("a retry made another key: %+v vs %+v", again, next)
	}
	if n := e.count("service_account_keys"); n != 2 {
		t.Fatalf("%d key rows", n)
	}

	// The new key can rotate itself with a chosen ttl.
	third := decode[gen.ServiceAccountKeyCreated](t, e.call("POST", "/api/v1/auth/service-key/rotate", map[string]any{"ttl_seconds": 3600}, next.Key))
	if third.TtlSeconds != 3600 {
		t.Fatalf("ttl override: %+v", third)
	}
	// The chain has moved on: the first key's retry no longer has a usable successor.
	rec = e.call("POST", "/api/v1/auth/service-key/rotate", nil, old.Key)
	want(t, rec, 409)
	if errCode(t, rec) != "conflict" {
		t.Fatalf("body: %s", rec.Body)
	}
	if rows := e.auditRows("service_key.rotate"); len(rows) != 3 {
		t.Fatalf("rotation audit rows: %+v", rows)
	}
}

func TestServiceKeyRotationRefusals(t *testing.T) {
	e := newDevEnv(t)
	k := e.mkKey("legacy", 0)
	body := map[string]any{"ttl_seconds": 5}

	want(t, e.call("POST", "/api/v1/auth/service-key/rotate", nil, ""), 401)                       // anonymous
	want(t, e.call("POST", "/api/v1/auth/service-key/rotate", nil, "garbage"), 401)                // junk
	want(t, e.call("POST", "/api/v1/auth/service-key/rotate", nil, e.token), 403)                  // a session is not a key
	want(t, e.call("POST", "/api/v1/auth/service-key/rotate", nil, e.legacyToken(time.Hour)), 403) // nor is an HS256 legacy token
	want(t, e.call("POST", "/api/v1/auth/service-key/rotate", body, k.Key), 400)                   // ttl below the minimum
	want(t, e.call("POST", "/api/v1/auth/service-key/rotate", `{"ttl_seconds":`, k.Key), 400)      // bad JSON
	if n := e.count("service_account_keys"); n != 1 {
		t.Fatalf("refused rotations created keys: %d", n)
	}

	// A revoked or expired key cannot rotate: the old key must be valid.
	want(t, e.as("DELETE", "/api/v1/service-account-keys/"+k.Id.String(), nil), 204)
	want(t, e.call("POST", "/api/v1/auth/service-key/rotate", nil, k.Key), 401)

	exp := e.mkKey("mcp", 3600)
	if _, err := e.db.Exec(`UPDATE service_account_keys SET expires_at = ? WHERE id = ?`, time.Now().Add(-time.Minute).UnixMilli(), exp.Id.String()); err != nil {
		t.Fatal(err)
	}
	want(t, e.call("POST", "/api/v1/auth/service-key/rotate", nil, exp.Key), 401)
	if n := e.count("service_account_keys"); n != 2 {
		t.Fatalf("refused rotations created keys: %d", n)
	}
}

// The rotate operation sits under /auth/, so it has the login endpoints' per-IP limit
// (REQ-AUTH-007): a stolen key cannot be rotated in a tight loop to flood the table.
func TestServiceKeyRotationIsRateLimitedLikeLogin(t *testing.T) {
	e := newDevEnv(t)
	k := e.mkKey("mcp", 0)
	var codes []int
	for range 8 {
		req := httptest.NewRequest("POST", "/api/v1/auth/service-key/rotate", http.NoBody)
		req.RemoteAddr = "192.0.2.200:4000"
		req.Header.Set("Authorization", "Bearer "+k.Key)
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		codes = append(codes, rec.Code)
	}
	if codes[0] != 200 || codes[7] != 429 {
		t.Fatalf("status sequence %v, want 200 first and 429 once the budget (burst 5) is spent", codes)
	}
}

func servicekeyCaller(userID, username string) servicekey.Caller {
	return servicekey.Caller{UserID: userID, Username: username}
}
