package servicekey

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/thanhtunguet/homecore/internal/audit"
	"github.com/thanhtunguet/homecore/internal/auth"
	"github.com/thanhtunguet/homecore/internal/secrets"
	"github.com/thanhtunguet/homecore/internal/storage"
)

// REQ-AUTH-021..027, ADR-018.

type env struct {
	t      *testing.T
	db     *storage.DB
	sealer *secrets.Sealer
	svc    *Service
	user   string // an enabled account
	mu     sync.Mutex
	now    time.Time
}

func (e *env) clock() time.Time { e.mu.Lock(); defer e.mu.Unlock(); return e.now }
func (e *env) advance(d time.Duration) {
	e.mu.Lock()
	e.now = e.now.Add(d)
	e.mu.Unlock()
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "keys.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	sealer, err := secrets.NewSealer(bytes.Repeat([]byte{7}, secrets.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, db: db, sealer: sealer, now: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)}
	e.user = e.addUser("alice")
	svc, err := New(Config{DB: db, Sealer: sealer, Audit: audit.NewWriter(db, e.clock), Now: e.clock})
	if err != nil {
		t.Fatal(err)
	}
	e.svc = svc
	return e
}

func (e *env) addUser(name string) string {
	e.t.Helper()
	id := uuid.Must(uuid.NewV7()).String()
	if _, err := e.db.Exec(`INSERT INTO users (id, username, password_hash, disabled, created_at, updated_at) VALUES (?, ?, 'x', 0, 1, 1)`, id, name); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) caller() Caller { return Caller{UserID: e.user, Username: "alice"} }

func (e *env) create(name, scope string, ttl time.Duration) Issued {
	e.t.Helper()
	i, err := e.svc.Create(context.Background(), e.caller(), name, scope, ttl)
	if err != nil {
		e.t.Fatal(err)
	}
	return i
}

func (e *env) auth(cred string) (auth.Principal, error) {
	return e.svc.Authenticate(context.Background(), cred)
}

func (e *env) mustAuth(cred string) auth.Principal {
	e.t.Helper()
	p, err := e.auth(cred)
	if err != nil {
		e.t.Fatalf("credential rejected: %v", err)
	}
	return p
}

func (e *env) mustReject(cred string) {
	e.t.Helper()
	if _, err := e.auth(cred); !errors.Is(err, auth.ErrUnauthenticated) {
		e.t.Fatalf("credential accepted, err=%v", err)
	}
}

func TestCreateDefaultsToOneYearAndAuthenticates(t *testing.T) {
	e := newEnv(t)
	i := e.create("claude mcp", ScopeMCP, 0)
	if i.Key.TTL != 365*24*time.Hour || !i.Key.ExpiresAt.Equal(e.clock().Add(365*24*time.Hour)) {
		t.Fatalf("default ttl: %v expires %v", i.Key.TTL, i.Key.ExpiresAt)
	}
	if i.Key.Status(e.clock()) != StatusActive || i.Key.Scope != ScopeMCP || i.Key.UserID != e.user {
		t.Fatalf("key: %+v", i.Key)
	}
	if got := strings.Count(i.Credential, "."); got != 2 || auth.TokenAlgorithm(i.Credential) != Algorithm {
		t.Fatalf("credential is not an EdDSA JWT: %q alg=%q", i.Credential, auth.TokenAlgorithm(i.Credential))
	}
	p := e.mustAuth(i.Credential)
	if p.UserID != e.user || p.Username != "alice" || p.Scope != ScopeMCP || p.KeyID != i.Key.ID {
		t.Fatalf("principal: %+v", p)
	}
}

func TestCreateValidation(t *testing.T) {
	e := newEnv(t)
	cases := []struct {
		name, scope string
		ttl         time.Duration
	}{
		{"", ScopeMCP, 0},
		{"   ", ScopeMCP, 0},
		{strings.Repeat("x", 65), ScopeMCP, 0},
		{"bad\nname", ScopeMCP, 0},
		{"ok", "admin", 0},
		{"ok", "", 0},
		{"ok", ScopeMCP, time.Second},
		{"ok", ScopeMCP, MaxTTL + time.Second},
		{"ok", ScopeMCP, -time.Hour},
	}
	for _, c := range cases {
		_, err := e.svc.Create(context.Background(), e.caller(), c.name, c.scope, c.ttl)
		var verr *ValidationError
		if !errors.As(err, &verr) {
			t.Errorf("Create(%q, %q, %v): %v, want a validation error", c.name, c.scope, c.ttl, err)
		}
	}
	if keys, _ := e.svc.List(context.Background(), e.user); len(keys) != 0 {
		t.Fatalf("rejected input stored %d keys", len(keys))
	}
	// The bounds themselves are fine.
	e.create("min", ScopeLegacy, MinTTL)
	e.create("max", ScopeLegacy, MaxTTL)
}

func TestExpiry(t *testing.T) {
	e := newEnv(t)
	i := e.create("short", ScopeMCP, time.Hour)
	e.advance(time.Hour - time.Second)
	e.mustAuth(i.Credential)
	e.advance(time.Second)
	e.mustReject(i.Credential)
	if got := i.Key.Status(e.clock()); got != StatusExpired {
		t.Fatalf("status %s", got)
	}
}

// The private key is sealed with the master key and bound to its row; the credential is not stored.
func TestPrivateKeyIsSealedAtRestAndBoundToItsRow(t *testing.T) {
	e := newEnv(t)
	a, b := e.create("a", ScopeMCP, 0), e.create("b", ScopeMCP, 0)

	var sealed, pub []byte
	if err := e.db.QueryRow(`SELECT private_key_sealed, public_key FROM service_account_keys WHERE id = ?`, a.Key.ID).Scan(&sealed, &pub); err != nil {
		t.Fatal(err)
	}
	seed, err := e.sealer.Open(sealed, aad(a.Key.ID))
	if err != nil || len(seed) != ed25519.SeedSize {
		t.Fatalf("sealed key does not open: %v (%d bytes)", err, len(seed))
	}
	if bytes.Contains(sealed, seed) {
		t.Fatal("the seed is stored in the clear")
	}
	if !ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey).Equal(ed25519.PublicKey(pub)) {
		t.Fatal("stored public key does not match the sealed private key")
	}
	if _, err := e.sealer.Open(sealed, aad(b.Key.ID)); err == nil {
		t.Fatal("a sealed key opened under another row's id")
	}
	// Different keys have different key pairs.
	var pubB []byte
	_ = e.db.QueryRow(`SELECT public_key FROM service_account_keys WHERE id = ?`, b.Key.ID).Scan(&pubB)
	if bytes.Equal(pub, pubB) {
		t.Fatal("two keys share a key pair")
	}
	// The credential itself is nowhere in the database.
	rows, err := e.db.Query(`SELECT quote(id) || quote(name) || quote(public_key) || quote(private_key_sealed) FROM service_account_keys`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var all string
		_ = rows.Scan(&all)
		for _, c := range []string{a.Credential, b.Credential} {
			if strings.Contains(all, c) || strings.Contains(all, strings.Split(c, ".")[2]) {
				t.Fatal("a credential (or its signature) is stored")
			}
		}
	}
}

func TestAuthenticateRejectsForgeries(t *testing.T) {
	e := newEnv(t)
	i := e.create("k", ScopeMCP, 0)
	e.mustAuth(i.Credential)
	h, p, s := split(i.Credential)

	// Tampered payload or signature, truncated, junk.
	forgedPayload := b64url.EncodeToString([]byte(`{"sub":"` + e.user + `","iat":1,"exp":99999999999,"jti":"` + i.Key.ID + `","scp":"mcp"}`))
	for name, tok := range map[string]string{
		"payload swapped":   h + "." + forgedPayload + "." + s,
		"signature flipped": h + "." + p + "." + flip(s),
		"no signature":      h + "." + p + ".",
		"two parts":         h + "." + p,
		"four parts":        i.Credential + ".x",
		"empty":             "",
		"junk":              "not a token",
		"too long":          h + "." + strings.Repeat("A", 3000) + "." + s,
	} {
		if _, err := e.auth(tok); !errors.Is(err, auth.ErrUnauthenticated) {
			t.Errorf("%s accepted (err=%v)", name, err)
		}
	}

	// Signed correctly, but with a key the Controller does not have under this kid.
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	e.mustReject(sign(i.Key, other))

	// Claims that disagree with the row are refused even when the signature is the real one.
	_, priv := e.privateKey(i.Key.ID)
	for name, mutate := range map[string]func(*Key){
		"other scope": func(k *Key) { k.Scope = ScopeLegacy },
		"other user":  func(k *Key) { k.UserID = e.addUser("mallory") },
	} {
		k := i.Key
		mutate(&k)
		if _, err := e.auth(sign(k, priv)); !errors.Is(err, auth.ErrUnauthenticated) {
			t.Errorf("%s accepted (err=%v)", name, err)
		}
	}

	// An unknown kid (a key pair of the right shape) is refused too.
	ghost := i.Key
	ghost.ID = uuid.Must(uuid.NewV7()).String()
	e.mustReject(sign(ghost, priv))
}

func TestHeaderIsStrict(t *testing.T) {
	kid := uuid.Must(uuid.NewV7()).String()
	for hdr, ok := range map[string]bool{
		`{"alg":"EdDSA","typ":"JWT","kid":"` + kid + `"}`:                  true,
		`{"alg":"EdDSA","kid":"` + kid + `"}`:                              true,
		`{"alg":"EdDSA","typ":"jwt","kid":"` + kid + `"}`:                  true,
		`{"alg":"none","typ":"JWT","kid":"` + kid + `"}`:                   false,
		`{"alg":"HS256","typ":"JWT","kid":"` + kid + `"}`:                  false,
		`{"alg":"EdDSA","typ":"JWT"}`:                                      false,
		`{"alg":"EdDSA","typ":"JWT","kid":""}`:                             false,
		`{"alg":"EdDSA","typ":"JWT","kid":1}`:                              false,
		`{"typ":"JWT","kid":"` + kid + `"}`:                                false,
		`{"alg":"EdDSA","typ":"JWT","kid":"` + kid + `","jku":"http://x"}`: false,
		`{"alg":"EdDSA","typ":"JWT","kid":"` + kid + `","crit":["x"]}`:     false,
		`{"alg":"EdDSA","typ":"at+jwt","kid":"` + kid + `"}`:               false,
		`[]`: false,
	} {
		_, got := parseHeader(b64url.EncodeToString([]byte(hdr)))
		if got != ok {
			t.Errorf("parseHeader(%s) = %v, want %v", hdr, got, ok)
		}
	}
	if _, ok := parseHeader("***"); ok {
		t.Error("bad base64 accepted")
	}
}

func TestRevokeIsImmediateIdempotentAndOwnerOnly(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	i := e.create("k", ScopeMCP, 0)
	e.mustAuth(i.Credential)

	mallory := Caller{UserID: e.addUser("mallory"), Username: "mallory"}
	if err := e.svc.Revoke(ctx, mallory, i.Key.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a stranger revoked the key: %v", err)
	}
	e.mustAuth(i.Credential)

	if err := e.svc.Revoke(ctx, e.caller(), i.Key.ID); err != nil {
		t.Fatal(err)
	}
	e.mustReject(i.Credential)
	if err := e.svc.Revoke(ctx, e.caller(), i.Key.ID); err != nil {
		t.Fatalf("second revoke: %v", err)
	}
	if err := e.svc.Revoke(ctx, e.caller(), uuid.Must(uuid.NewV7()).String()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
	keys, _ := e.svc.List(ctx, e.user)
	if len(keys) != 1 || keys[0].Status(e.clock()) != StatusRevoked {
		t.Fatalf("list: %+v", keys)
	}
	// Revoking wrote exactly one audit row, without the credential.
	rows := e.auditRows("service_key.revoke")
	if len(rows) != 1 || strings.Contains(rows[0], i.Credential) {
		t.Fatalf("audit: %v", rows)
	}
}

func TestDisabledOrDeletedOwnerLosesTheKeyAtOnce(t *testing.T) {
	e := newEnv(t)
	i := e.create("k", ScopeMCP, 0)
	e.mustAuth(i.Credential)
	if _, err := e.db.Exec(`UPDATE users SET disabled = 1 WHERE id = ?`, e.user); err != nil {
		t.Fatal(err)
	}
	e.mustReject(i.Credential)
	if _, err := e.db.Exec(`UPDATE users SET disabled = 0 WHERE id = ?`, e.user); err != nil {
		t.Fatal(err)
	}
	e.mustAuth(i.Credential)
	if _, err := e.db.Exec(`DELETE FROM users WHERE id = ?`, e.user); err != nil {
		t.Fatal(err)
	}
	e.mustReject(i.Credential)
	var n int
	_ = e.db.QueryRow(`SELECT count(*) FROM service_account_keys`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d key rows survived their owner", n)
	}
}

func TestListIsPerOwnerNewestFirst(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.create("first", ScopeMCP, 0)
	e.advance(time.Second)
	b := e.create("second", ScopeLegacy, time.Hour)
	bob := Caller{UserID: e.addUser("bob"), Username: "bob"}
	if _, err := e.svc.Create(ctx, bob, "bobs", ScopeMCP, 0); err != nil {
		t.Fatal(err)
	}
	keys, err := e.svc.List(ctx, e.user)
	if err != nil || len(keys) != 2 || keys[0].ID != b.Key.ID || keys[1].ID != a.Key.ID {
		t.Fatalf("list: %+v %v", keys, err)
	}
	if keys[0].TTL != time.Hour || keys[0].Scope != ScopeLegacy {
		t.Fatalf("fields: %+v", keys[0])
	}
}

func TestLastUsedIsRefreshedAtMostOncePerMinute(t *testing.T) {
	e := newEnv(t)
	i := e.create("k", ScopeMCP, 0)
	lastUsed := func() *time.Time {
		keys, _ := e.svc.List(context.Background(), e.user)
		return keys[0].LastUsedAt
	}
	if lastUsed() != nil {
		t.Fatal("a new key was already used")
	}
	e.mustAuth(i.Credential)
	first := lastUsed()
	if first == nil || !first.Equal(e.clock()) {
		t.Fatalf("last_used_at %v, want %v", first, e.clock())
	}
	e.advance(30 * time.Second)
	e.mustAuth(i.Credential)
	if got := lastUsed(); !got.Equal(*first) {
		t.Fatalf("written again after 30 s: %v", got)
	}
	e.advance(31 * time.Second)
	e.mustAuth(i.Credential)
	if got := lastUsed(); !got.Equal(e.clock()) {
		t.Fatalf("not refreshed after a minute: %v", got)
	}
}

func TestPruneRemovesOnlyKeysThatEndedBeforeTheCutoff(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	expired := e.create("expired", ScopeMCP, time.Hour)
	revoked := e.create("revoked", ScopeMCP, 0)
	if err := e.svc.Revoke(ctx, e.caller(), revoked.Key.ID); err != nil {
		t.Fatal(err)
	}
	live := e.create("live", ScopeMCP, 0)
	e.advance(48 * time.Hour)

	n, err := e.svc.Prune(ctx, e.clock().Add(-24*time.Hour), 100)
	if err != nil || n != 2 { // `expired` ended 47 h ago, `revoked` 48 h ago
		t.Fatalf("pruned %d, %v", n, err)
	}
	keys, _ := e.svc.List(ctx, e.user)
	if len(keys) != 1 || keys[0].ID != live.Key.ID {
		t.Fatalf("left: %+v", keys)
	}
	_ = expired
}

func (e *env) privateKey(id string) (ed25519.PublicKey, ed25519.PrivateKey) {
	e.t.Helper()
	var sealed []byte
	if err := e.db.QueryRow(`SELECT private_key_sealed FROM service_account_keys WHERE id = ?`, id).Scan(&sealed); err != nil {
		e.t.Fatal(err)
	}
	seed, err := e.sealer.Open(sealed, aad(id))
	if err != nil {
		e.t.Fatal(err)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	return priv.Public().(ed25519.PublicKey), priv
}

func (e *env) auditRows(action string) []string {
	e.t.Helper()
	rows, err := e.db.Query(`SELECT actor || ' ' || target_id || ' ' || details FROM audit_log WHERE action = ? ORDER BY id`, action)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		out = append(out, s)
	}
	return out
}

func split(tok string) (h, p, s string) {
	parts := strings.Split(tok, ".")
	return parts[0], parts[1], parts[2]
}

// flip changes one base64url character so the decoded bytes differ.
func flip(s string) string {
	b := []byte(s)
	if b[0] == 'A' {
		b[0] = 'B'
	} else {
		b[0] = 'A'
	}
	return string(b)
}
