package api

import (
	"bytes"
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/thanhtunguet/homecore/internal/audit"
	"github.com/thanhtunguet/homecore/internal/auth"
	"github.com/thanhtunguet/homecore/internal/secrets"
)

// REQ-API-026..029, ADR-017: the legacy GET endpoints, end to end through the real router.

// legacyToken issues what `homecore token create` does, with the sealer devEnv uses.
func (e *devEnv) legacyToken(ttl time.Duration) string {
	e.t.Helper()
	sealer, err := secrets.NewSealer(bytes.Repeat([]byte{4}, secrets.KeySize))
	if err != nil {
		e.t.Fatal(err)
	}
	lt, err := auth.IssueLegacyToken(context.Background(), e.db, sealer, "alice", "kitchen app", ttl, audit.NewWriter(e.db, nil), nil)
	if err != nil {
		e.t.Fatal(err)
	}
	return lt.Token
}

func legacyURL(op, device string) string { return "/api/" + op + "?device=" + url.QueryEscape(device) }

func TestLegacyTurnOnByNameAndByID(t *testing.T) {
	e := newDevEnv(t)
	d := e.mkDevice(map[string]any{"name": "Gaming PC", "monitor_port": 3333, "mac_address": "AA:BB:CC:DD:EE:FF", "wol_broadcast_address": "192.168.1.255"})

	// By name, spelled in another case: plain text `true`, one magic packet, never cached.
	rec := e.as("GET", legacyURL("turn-on", "gaming pc"), nil)
	want(t, rec, 200)
	if got := rec.Body.String(); got != "true" {
		t.Fatalf("body %q, want the text true", got)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("headers: content-type %q cache-control %q", ct, rec.Header().Get("Cache-Control"))
	}
	if got := e.packets(); len(got) != 1 || got[0] != "aa:bb:cc:dd:ee:ff 192.168.1.255:9" {
		t.Fatalf("packets: %v", got)
	}

	// By id, after the throttle window: idempotent, the active command is reused, no second packet.
	time.Sleep(1100 * time.Millisecond)
	want(t, e.as("GET", legacyURL("turn-on", d.Id.String()), nil), 200)
	if len(e.packets()) != 1 {
		t.Fatalf("a repeated turn-on sent another packet: %v", e.packets())
	}

	// The audit trail names the acting user and the door that was used.
	rows := e.auditRows("device.wake")
	if len(rows) != 2 || rows[0].actor != "alice" || rows[0].target != d.Id.String() || !strings.Contains(rows[0].details, `"via":"legacy"`) {
		t.Fatalf("audit: %+v", rows)
	}
}

func TestLegacyIsOnlineFollowsTheMonitor(t *testing.T) {
	e := newDevEnv(t)
	e.mkDevice(map[string]any{"name": "nas", "monitor_port": 4444})
	get := func() string {
		rec := e.as("GET", legacyURL("is-online", "NAS"), nil)
		want(t, rec, 200)
		return rec.Body.String()
	}
	if got := get(); got != "false" { // never probed: unknown counts as not online
		t.Fatalf("unknown device: %q", got)
	}
	e.setUp(4444, true)
	e.mon.CheckDue(context.Background())
	if got := get(); got != "true" {
		t.Fatalf("online device: %q", got)
	}
}

func TestLegacyErrorsUseTheEnvelope(t *testing.T) {
	e := newDevEnv(t)
	e.mkDevice(map[string]any{"name": "no-mac", "monitor_port": 1})

	for _, op := range []string{"turn-on", "turn-off", "is-online"} {
		rec := e.as("GET", legacyURL(op, "ghost"), nil)
		want(t, rec, 404)
		if errCode(t, rec) != "not_found" {
			t.Fatalf("%s unknown device: %s", op, rec.Body)
		}
		want(t, e.call("GET", legacyURL(op, "no-mac"), nil, ""), 401) // anonymous
		want(t, e.callRaw("GET", "/api/"+op, nil, e.token), 400)      // `device` is required
		want(t, e.as("GET", legacyURL(op, "   "), nil), 404)
	}
	want(t, e.as("GET", legacyURL("turn-on", "no-mac"), nil), 409) // no MAC address
	settle()
	want(t, e.as("GET", legacyURL("turn-off", "no-mac"), nil), 409) // no agent binding
	// POST is not a legacy method.
	want(t, e.callRaw("POST", legacyURL("turn-on", "no-mac"), nil, e.token), 405)
}

// The legacy endpoints and the /api/v1 ones share one wake/shutdown slot per device, however the
// device is spelled (REQ-API-018).
func TestLegacyThrottleIsSharedWithV1(t *testing.T) {
	e := newDevEnv(t)
	d := e.mkDevice(map[string]any{"name": "tv", "monitor_port": 5555, "mac_address": "AA:BB:CC:DD:EE:01", "wol_broadcast_address": "192.168.1.255"})

	want(t, e.as("GET", legacyURL("turn-on", "tv"), nil), 200)
	for _, p := range []struct{ method, path string }{
		{"GET", legacyURL("turn-on", "tv")},
		{"GET", legacyURL("turn-on", d.Id.String())},
		{"GET", legacyURL("turn-off", "TV")},
		{"POST", "/api/v1/devices/" + d.Id.String() + "/wake"},
	} {
		rec := e.as(p.method, p.path, nil)
		want(t, rec, 429)
		if errCode(t, rec) != "rate_limited" || rec.Header().Get("Retry-After") != "1" {
			t.Fatalf("%s %s: %s retry-after=%q", p.method, p.path, rec.Body, rec.Header().Get("Retry-After"))
		}
	}
	if len(e.packets()) != 1 {
		t.Fatalf("throttled requests sent packets: %v", e.packets())
	}
	// Reading the state is never throttled.
	want(t, e.as("GET", legacyURL("is-online", "tv"), nil), 200)
}

func TestLegacyTurnOffReachesTheAgent(t *testing.T) {
	se := newShutdownEnv(t, nil)
	rec := se.as("GET", legacyURL("turn-off", "GAMING PC"), nil)
	want(t, rec, 200)
	if rec.Body.String() != "true" {
		t.Fatalf("body %q", rec.Body)
	}
	se.waitPower(1)
	rows := se.auditRows("device.shutdown")
	if len(rows) != 1 || rows[0].actor != "alice" || !strings.Contains(rows[0].details, `"via":"legacy"`) || !strings.Contains(rows[0].details, `"name":"gaming pc"`) {
		t.Fatalf("audit: %+v", rows)
	}
}

// A legacy token is a 10-year bearer string for old apps: it works on the three legacy endpoints and
// nowhere else, and it stops working with its account.
func TestLegacyTokenIsScopedToTheLegacyEndpoints(t *testing.T) {
	e := newDevEnv(t)
	e.mkDevice(map[string]any{"name": "tv", "monitor_port": 5555, "mac_address": "AA:BB:CC:DD:EE:01", "wol_broadcast_address": "192.168.1.255"})
	tok := e.legacyToken(10 * 365 * 24 * time.Hour)

	want(t, e.call("GET", legacyURL("is-online", "tv"), nil, tok), 200)
	want(t, e.call("GET", legacyURL("turn-on", "tv"), nil, tok), 200)
	if len(e.packets()) != 1 {
		t.Fatalf("packets: %v", e.packets())
	}
	if rows := e.auditRows("device.wake"); len(rows) != 1 || rows[0].actor != "alice" {
		t.Fatalf("a legacy token must be audited as its owner: %+v", rows)
	}

	// Everything else: the token is genuine but refused (403, not 401). The v1 operations do not
	// declare this 403 in the spec (like the fail-closed policy 403), so the response is not checked.
	for _, p := range []struct{ method, path string }{
		{"GET", "/api/v1/devices"},
		{"GET", "/api/v1/auth/me"},
		{"POST", "/api/v1/devices/0193a0b2-7c4e-7f00-8000-000000000001/shutdown"},
		{"GET", "/api/v1/system/info"},
	} {
		rec := e.callRaw(p.method, p.path, nil, tok)
		want(t, rec, 403)
		if errCode(t, rec) != "forbidden" {
			t.Fatalf("%s %s: %s", p.method, p.path, rec.Body)
		}
	}

	// Disabling the account revokes the token at once (JWT checks the user on every request).
	if _, err := e.db.Exec(`UPDATE users SET disabled = 1 WHERE username = 'alice'`); err != nil {
		t.Fatal(err)
	}
	want(t, e.call("GET", legacyURL("is-online", "tv"), nil, tok), 401)
}

func TestLegacyTokenIssuingRules(t *testing.T) {
	e := newDevEnv(t)
	sealer, err := secrets.NewSealer(bytes.Repeat([]byte{4}, secrets.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := auth.IssueLegacyToken(ctx, e.db, sealer, "nobody", "x", time.Hour, nil, nil); err == nil {
		t.Fatal("a token was issued for an unknown user")
	}
	for _, ttl := range []time.Duration{0, -time.Hour, auth.MaxLegacyTokenTTL + time.Hour} {
		if _, err := auth.IssueLegacyToken(ctx, e.db, sealer, "alice", "x", ttl, nil, nil); err == nil {
			t.Fatalf("ttl %v accepted", ttl)
		}
	}
	// The audit entry has the label and token id but not the token.
	lt, err := auth.IssueLegacyToken(ctx, e.db, sealer, "alice", "bedroom", time.Hour, audit.NewWriter(e.db, nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	rows := e.auditRows("auth.legacy_token_issue")
	if len(rows) != 1 || !strings.Contains(rows[0].details, `"label":"bedroom"`) || !strings.Contains(rows[0].details, lt.ID) || strings.Contains(rows[0].details, lt.Token) {
		t.Fatalf("audit: %+v", rows)
	}
	// An expired token is refused like any expired JWT.
	if time.Until(lt.Expires) > time.Hour+time.Minute {
		t.Fatalf("expiry %v", lt.Expires)
	}
}
