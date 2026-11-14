package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thanhtunguet/homecore/internal/audit"
	"github.com/thanhtunguet/homecore/internal/secrets"
	"github.com/thanhtunguet/homecore/internal/storage"
)

const testPassword = "correct horse battery staple"

type env struct {
	svc    *Service
	db     *storage.DB
	clk    *fakeClock
	sealer *secrets.Sealer
	user   User
}

func openDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return db
}

func newSealer(t *testing.T) *secrets.Sealer {
	t.Helper()
	s, err := secrets.NewSealer(bytes.Repeat([]byte{7}, secrets.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// newEnv builds a Service on a real SQLite file with a fake clock and one user "alice".
func newEnv(t *testing.T) *env {
	t.Helper()
	db := openDB(t)
	clk := newFakeClock()
	sealer := newSealer(t)
	svc, err := NewService(context.Background(), Config{
		DB: db, Sealer: sealer, Audit: audit.NewWriter(db, clk.Now),
		AccessTTL: 15 * time.Minute, RefreshTTL: 30 * 24 * time.Hour,
		Params: fastParams, Now: clk.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	u, err := svc.Accounts().Create(context.Background(), "alice", testPassword, "test")
	if err != nil {
		t.Fatal(err)
	}
	return &env{svc: svc, db: db, clk: clk, sealer: sealer, user: u}
}

func (e *env) login(t *testing.T) TokenPair {
	t.Helper()
	p, err := e.svc.Login(context.Background(), "alice", testPassword, "10.0.0.1")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	return p
}

func (e *env) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := e.db.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestLoginIssuesVerifiablePair(t *testing.T) {
	e := newEnv(t)
	pair := e.login(t)
	if pair.ExpiresIn != 900 || pair.AccessToken == "" || pair.RefreshToken == "" {
		t.Fatalf("unexpected pair %+v", pair)
	}
	p, err := e.svc.Authenticate(context.Background(), pair.AccessToken)
	if err != nil || p.Username != "alice" || p.UserID != e.user.ID {
		t.Fatalf("access token does not authenticate: %+v %v", p, err)
	}
	// Username lookup is case-insensitive.
	if _, err := e.svc.Login(context.Background(), "ALICE", testPassword, ""); err != nil {
		t.Fatalf("case-insensitive login: %v", err)
	}
}

func TestLoginFailuresAreIndistinguishable(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.db.ExecContext(ctx, `INSERT INTO users (id, username, password_hash, disabled, created_at, updated_at)
		SELECT 'dis-id', 'dora', password_hash, 1, 0, 0 FROM users WHERE username = 'alice'`); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string][2]string{
		"wrong password": {"alice", "wrong wrong wrong"},
		"unknown user":   {"nobody", testPassword},
		"disabled user":  {"dora", testPassword}, // right password, but disabled
		"over-long":      {"alice", strings.Repeat("a", 300)},
	} {
		_, err := e.svc.Login(ctx, c[0], c[1], "10.0.0.9")
		if err != ErrInvalidCredentials {
			t.Errorf("%s: %v, want ErrInvalidCredentials", name, err)
		}
	}
}

// The unknown-user path must perform a full verification (against the dummy hash) like a wrong
// password does, which is what keeps the two indistinguishable by timing. Asserted through the
// work done rather than wall-clock time, which is too noisy for a test.
func TestUnknownUserStillVerifiesAHash(t *testing.T) {
	e := newEnv(t)
	if _, _, _, err := decodeHash(e.svc.dummyHash); err != nil {
		t.Fatalf("dummy hash is not a valid argon2id hash: %v", err)
	}
	// Hold every hashing slot: a login for an unknown user must block on the hasher exactly like
	// a real one, proving it goes through Verify.
	for range MaxConcurrent {
		e.svc.hasher.sem <- struct{}{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := e.svc.Login(ctx, "nobody", "whatever whatever", ""); err == nil || err == ErrInvalidCredentials {
		t.Fatalf("unknown user returned without running a verification: %v", err)
	}
}

func TestLoginRehashesOutdatedParameters(t *testing.T) {
	e := newEnv(t)
	stronger := fastParams
	stronger.Iterations = 2
	e.svc.hasher = NewHasher(stronger)
	e.login(t)
	u, _ := e.svc.store.UserByID(context.Background(), e.user.ID)
	if !strings.Contains(u.PasswordHash, "m=64,t=2,p=1") {
		t.Fatalf("hash not upgraded on login: %q", u.PasswordHash)
	}
	e.login(t) // still works with the new hash
}

func TestSigningKeyIsGeneratedOnceSealedAndDistinctFromMasterKey(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	var raw []byte
	if err := e.db.QueryRowContext(ctx, `SELECT value FROM system_settings WHERE key = ?`, SettingJWTKey).Scan(&raw); err != nil {
		t.Fatalf("signing key not stored: %v", err)
	}
	key, err := e.sealer.Open(raw, "system_settings."+SettingJWTKey)
	if err != nil || len(key) != SigningKeySize {
		t.Fatalf("stored value is not a sealed %d-byte key: %v", SigningKeySize, err)
	}
	if bytes.Equal(key, bytes.Repeat([]byte{7}, secrets.KeySize)) {
		t.Fatal("JWT key equals the master key")
	}
	if bytes.Contains(raw, key) {
		t.Fatal("signing key stored in plaintext")
	}
	if _, err := e.sealer.Open(raw, "system_settings.other"); err == nil {
		t.Fatal("sealed value must be bound to its column")
	}

	// A second start reuses the key: tokens issued before the restart stay valid.
	pair := e.login(t)
	svc2, err := NewService(ctx, Config{DB: e.db, Sealer: e.sealer, AccessTTL: 15 * time.Minute,
		RefreshTTL: time.Hour, Params: fastParams, Now: e.clk.Now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc2.Authenticate(ctx, pair.AccessToken); err != nil {
		t.Fatalf("token from before the restart rejected: %v", err)
	}
	if n := e.count(t, `SELECT count(*) FROM system_settings WHERE key = ?`, SettingJWTKey); n != 1 {
		t.Fatalf("%d signing key rows", n)
	}

	// A different master key cannot open it: clear error, not a silent new key.
	other, _ := secrets.NewSealer(bytes.Repeat([]byte{9}, secrets.KeySize))
	if _, err := NewService(ctx, Config{DB: e.db, Sealer: other, AccessTTL: time.Minute, RefreshTTL: time.Hour, Params: fastParams}); err == nil ||
		!strings.Contains(err.Error(), "master key") {
		t.Fatalf("expected a master-key hint, got %v", err)
	}
}

func TestRefreshRotation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	first := e.login(t)

	e.clk.Advance(5 * time.Minute)
	second, err := e.svc.Refresh(ctx, first.RefreshToken, "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if second.RefreshToken == first.RefreshToken || second.AccessToken == first.AccessToken {
		t.Fatal("rotation must issue new tokens")
	}
	if _, err := e.svc.Authenticate(ctx, second.AccessToken); err != nil {
		t.Fatalf("new access token invalid: %v", err)
	}
	// Chain continues.
	e.clk.Advance(5 * time.Minute)
	third, err := e.svc.Refresh(ctx, second.RefreshToken, "")
	if err != nil || third.RefreshToken == second.RefreshToken {
		t.Fatalf("second rotation: %v", err)
	}
	// One family, three rows.
	if n := e.count(t, `SELECT count(DISTINCT family_id) FROM refresh_tokens`); n != 1 {
		t.Fatalf("%d families", n)
	}
	if n := e.count(t, `SELECT count(*) FROM refresh_tokens`); n != 3 {
		t.Fatalf("%d rows", n)
	}
}

func TestRefreshTokensAreStoredOnlyAsHashes(t *testing.T) {
	e := newEnv(t)
	pair := e.login(t)
	next, err := e.svc.Refresh(context.Background(), pair.RefreshToken, "")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := e.db.Query(`SELECT token_hash, successor_sealed FROM refresh_tokens`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var hash, sealed []byte
		if err := rows.Scan(&hash, &sealed); err != nil {
			t.Fatal(err)
		}
		for _, tok := range []string{pair.RefreshToken, next.RefreshToken, pair.AccessToken, next.AccessToken} {
			if bytes.Contains(hash, []byte(tok)) || bytes.Contains(sealed, []byte(tok)) {
				t.Fatal("a plaintext token is stored")
			}
		}
		if len(hash) != 32 {
			t.Fatalf("token_hash is %d bytes, want a SHA-256", len(hash))
		}
	}
	var n int
	e.db.QueryRow(`SELECT count(*) FROM refresh_tokens WHERE token_hash = ?`, hashToken(pair.RefreshToken)).Scan(&n)
	if n != 1 {
		t.Fatal("lookup by SHA-256 of the token must find the row")
	}
}

func TestRefreshReuseOutsideGraceRevokesFamily(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	first := e.login(t)
	second, err := e.svc.Refresh(ctx, first.RefreshToken, "")
	if err != nil {
		t.Fatal(err)
	}

	e.clk.Advance(DefaultRefreshGrace + time.Second)
	if _, err := e.svc.Refresh(ctx, first.RefreshToken, "203.0.113.5"); err != ErrInvalidRefresh {
		t.Fatalf("replayed token: %v, want ErrInvalidRefresh", err)
	}
	// The legitimate holder of the successor is signed out too.
	if _, err := e.svc.Refresh(ctx, second.RefreshToken, ""); err != ErrInvalidRefresh {
		t.Fatalf("successor after family revocation: %v, want ErrInvalidRefresh", err)
	}
	if n := e.count(t, `SELECT count(*) FROM refresh_tokens WHERE revoked_at IS NULL`); n != 0 {
		t.Fatalf("%d tokens of the family still active", n)
	}
	if n := e.count(t, `SELECT count(*) FROM refresh_tokens WHERE successor_sealed IS NOT NULL`); n != 0 {
		t.Fatal("sealed successor copy kept after revocation")
	}
	// The reuse is audited, with the replaying address.
	if n := e.count(t, `SELECT count(*) FROM audit_log WHERE action = 'auth.refresh_reuse' AND remote_addr = '203.0.113.5' AND result = 'revoked'`); n != 1 {
		t.Fatalf("reuse audit rows: %d", n)
	}
	// A fresh login starts a new, working family.
	e.login(t)
}

func TestRefreshReuseInsideGraceReturnsSamePair(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	first := e.login(t)

	e.clk.Advance(time.Minute)
	a, err := e.svc.Refresh(ctx, first.RefreshToken, "")
	if err != nil {
		t.Fatal(err)
	}
	e.clk.Advance(3 * time.Second) // the other tab, still inside the window
	b, err := e.svc.Refresh(ctx, first.RefreshToken, "")
	if err != nil {
		t.Fatalf("inside grace window: %v", err)
	}
	if a.RefreshToken != b.RefreshToken || a.AccessToken != b.AccessToken {
		t.Fatal("grace-window request must return the already issued pair")
	}
	if b.ExpiresIn != 897 {
		t.Fatalf("expires_in %d, want the remaining 897 s", b.ExpiresIn)
	}
	if _, err := e.svc.Authenticate(ctx, b.AccessToken); err != nil {
		t.Fatal(err)
	}
	// No third row, family untouched, and the successor still rotates normally.
	if n := e.count(t, `SELECT count(*) FROM refresh_tokens`); n != 2 {
		t.Fatalf("%d rows, want 2 (no extra token minted)", n)
	}
	if n := e.count(t, `SELECT count(*) FROM refresh_tokens WHERE revoked_at IS NOT NULL`); n != 0 {
		t.Fatal("family revoked inside the grace window")
	}
	c, err := e.svc.Refresh(ctx, b.RefreshToken, "")
	if err != nil || c.RefreshToken == b.RefreshToken {
		t.Fatalf("successor does not rotate: %v", err)
	}
	// The window is measured from the rotation, not from the last request: the old token (rotated
	// a few seconds ago) still resolves to the first successor.
	if again, err := e.svc.Refresh(ctx, first.RefreshToken, ""); err != nil || again.RefreshToken != a.RefreshToken {
		t.Fatalf("old token inside its window: %v", err)
	}
}

func TestGraceWindowBoundary(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	first := e.login(t)
	if _, err := e.svc.Refresh(ctx, first.RefreshToken, ""); err != nil {
		t.Fatal(err)
	}
	e.clk.Advance(DefaultRefreshGrace - time.Millisecond)
	if _, err := e.svc.Refresh(ctx, first.RefreshToken, ""); err != nil {
		t.Fatalf("just inside the window: %v", err)
	}
	e.clk.Advance(time.Millisecond)
	if _, err := e.svc.Refresh(ctx, first.RefreshToken, ""); err != ErrInvalidRefresh {
		t.Fatalf("at the window edge: %v, want revocation", err)
	}
}

func TestGraceWindowCanBeDisabled(t *testing.T) {
	db := openDB(t)
	clk := newFakeClock()
	svc, err := NewService(context.Background(), Config{DB: db, Sealer: newSealer(t), AccessTTL: time.Minute,
		RefreshTTL: time.Hour, Params: fastParams, Now: clk.Now, Grace: -1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Accounts().Create(context.Background(), "alice", testPassword, "t"); err != nil {
		t.Fatal(err)
	}
	p, _ := svc.Login(context.Background(), "alice", testPassword, "")
	if _, err := svc.Refresh(context.Background(), p.RefreshToken, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Refresh(context.Background(), p.RefreshToken, ""); err != ErrInvalidRefresh {
		t.Fatalf("no grace configured: %v, want revocation", err)
	}
}

// Within the window a sealed copy exists; afterwards no plaintext refresh token is recoverable.
func TestSealedSuccessorIsWipedAfterTheWindow(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	first := e.login(t)
	next, _ := e.svc.Refresh(ctx, first.RefreshToken, "")

	var sealed []byte
	e.db.QueryRow(`SELECT successor_sealed FROM refresh_tokens WHERE token_hash = ?`, hashToken(first.RefreshToken)).Scan(&sealed)
	if len(sealed) == 0 {
		t.Fatal("no sealed successor inside the window")
	}
	if bytes.Contains(sealed, []byte(next.RefreshToken)) {
		t.Fatal("successor stored unsealed")
	}
	plain, err := e.sealer.Open(sealed, successorAAD)
	if err != nil || !bytes.Contains(plain, []byte(next.RefreshToken)) {
		t.Fatalf("sealed copy does not contain the issued pair: %v", err)
	}

	// Inside the window the maintenance job keeps it, after the window it wipes it.
	e.clk.Advance(DefaultRefreshGrace - time.Second)
	if err := e.svc.Maintain(ctx); err != nil {
		t.Fatal(err)
	}
	if n := e.count(t, `SELECT count(*) FROM refresh_tokens WHERE successor_sealed IS NOT NULL`); n != 1 {
		t.Fatalf("copy wiped too early (%d)", n)
	}
	e.clk.Advance(2 * time.Second)
	if err := e.svc.Maintain(ctx); err != nil {
		t.Fatal(err)
	}
	if n := e.count(t, `SELECT count(*) FROM refresh_tokens WHERE successor_sealed IS NOT NULL`); n != 0 {
		t.Fatalf("copy still stored after the window (%d)", n)
	}
	// And the wiped token counts as plain reuse.
	if _, err := e.svc.Refresh(ctx, first.RefreshToken, ""); err != ErrInvalidRefresh {
		t.Fatalf("reuse after wipe: %v", err)
	}
}

func TestSealedSuccessorIsWipedInlineOnNextRotation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p0 := e.login(t)
	p1, _ := e.svc.Refresh(ctx, p0.RefreshToken, "")
	e.clk.Advance(time.Minute)
	if _, err := e.svc.Refresh(ctx, p1.RefreshToken, ""); err != nil {
		t.Fatal(err)
	}
	if n := e.count(t, `SELECT count(*) FROM refresh_tokens WHERE successor_sealed IS NOT NULL`); n != 1 {
		t.Fatalf("%d sealed copies, want only the newest", n)
	}
}

func TestRefreshExpiryAndRevokedTokens(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.login(t)

	e.clk.Advance(30*24*time.Hour - time.Second)
	n, err := e.svc.Refresh(ctx, p.RefreshToken, "")
	if err != nil {
		t.Fatalf("just before expiry: %v", err)
	}
	e.clk.Advance(30*24*time.Hour + time.Second) // refresh lifetime is counted from the rotation
	if _, err := e.svc.Refresh(ctx, n.RefreshToken, ""); err != ErrInvalidRefresh {
		t.Fatalf("expired token: %v", err)
	}
	// Expired is not "reuse": nothing is audited as such.
	if c := e.count(t, `SELECT count(*) FROM audit_log WHERE action = 'auth.refresh_reuse'`); c != 0 {
		t.Fatal("expiry misreported as reuse")
	}
	if _, err := e.svc.Refresh(ctx, "no-such-token", ""); err != ErrInvalidRefresh {
		t.Fatalf("unknown token: %v", err)
	}
}

func TestRefreshRejectsDisabledUserAndRevokesFamily(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.login(t)
	if _, err := e.db.ExecContext(ctx, `UPDATE users SET disabled = 1 WHERE id = ?`, e.user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Refresh(ctx, p.RefreshToken, ""); err != ErrInvalidRefresh {
		t.Fatalf("disabled user refresh: %v", err)
	}
	if n := e.count(t, `SELECT count(*) FROM refresh_tokens WHERE revoked_at IS NULL`); n != 0 {
		t.Fatal("family left active for a disabled user")
	}
	if _, err := e.svc.Authenticate(ctx, p.AccessToken); err != ErrUnauthenticated {
		t.Fatalf("access token of a disabled user: %v", err)
	}
}

func TestLogoutRevokesFamily(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.login(t)
	next, _ := e.svc.Refresh(ctx, p.RefreshToken, "")
	principal := Principal{UserID: e.user.ID, Username: "alice"}

	if err := e.svc.Logout(ctx, principal, next.RefreshToken, "10.0.0.1"); err != nil {
		t.Fatal(err)
	}
	for name, tok := range map[string]string{"current": next.RefreshToken, "previous (inside grace)": p.RefreshToken} {
		if _, err := e.svc.Refresh(ctx, tok, ""); err != ErrInvalidRefresh {
			t.Errorf("%s refresh token after logout: %v", name, err)
		}
	}
	if n := e.count(t, `SELECT count(*) FROM audit_log WHERE action = 'auth.logout' AND result = 'ok'`); n != 1 {
		t.Fatalf("logout audit rows: %d", n)
	}
	// Idempotent, and unknown tokens are not an error.
	if err := e.svc.Logout(ctx, principal, next.RefreshToken, ""); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Logout(ctx, principal, "nonsense", ""); err != nil {
		t.Fatal(err)
	}
}

func TestLogoutIgnoresAnotherUsersToken(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.svc.Accounts().Create(ctx, "bob", "another long password", "t"); err != nil {
		t.Fatal(err)
	}
	alice := e.login(t)
	bob := Principal{UserID: "someone-else", Username: "bob"}
	if err := e.svc.Logout(ctx, bob, alice.RefreshToken, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Refresh(ctx, alice.RefreshToken, ""); err != nil {
		t.Fatalf("alice's session was revoked by another user: %v", err)
	}
}

func TestSessionsAreIndependentFamilies(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	phone, laptop := e.login(t), e.login(t)
	if err := e.svc.Logout(ctx, Principal{UserID: e.user.ID}, phone.RefreshToken, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Refresh(ctx, laptop.RefreshToken, ""); err != nil {
		t.Fatalf("logging out one device ended another: %v", err)
	}
}

func TestPasswordChangeRevokesRefreshTokens(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.login(t)
	e.login(t)

	n, err := e.svc.Accounts().SetPassword(ctx, "alice", "a brand new password", "cli")
	if err != nil || n != 2 {
		t.Fatalf("SetPassword revoked=%d err=%v", n, err)
	}
	if _, err := e.svc.Refresh(ctx, p.RefreshToken, ""); err != ErrInvalidRefresh {
		t.Fatalf("old session survived a password change: %v", err)
	}
	if _, err := e.svc.Login(ctx, "alice", testPassword, ""); err != ErrInvalidCredentials {
		t.Fatalf("old password still works: %v", err)
	}
	if _, err := e.svc.Login(ctx, "alice", "a brand new password", ""); err != nil {
		t.Fatalf("new password rejected: %v", err)
	}
	if _, err := e.svc.Accounts().SetPassword(ctx, "alice", "short", "cli"); err != ErrPasswordTooShort {
		t.Fatalf("short password accepted: %v", err)
	}
	if _, err := e.svc.Accounts().SetPassword(ctx, "ghost", "a brand new password", "cli"); err != ErrUserNotFound {
		t.Fatalf("unknown user: %v", err)
	}
}

func TestCreateUserValidation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.svc.Accounts()
	if _, err := a.Create(ctx, "Alice", "another long password", "t"); err != ErrUsernameTaken {
		t.Fatalf("duplicate ignoring case: %v", err)
	}
	for _, bad := range []string{"", "has space", "ünï", strings.Repeat("a", 65), "semi;colon", "new\nline"} {
		if _, err := a.Create(ctx, bad, "another long password", "t"); err == nil {
			t.Errorf("username %q accepted", bad)
		}
	}
	if _, err := a.Create(ctx, "bob", "short", "t"); err != ErrPasswordTooShort {
		t.Errorf("short password: %v", err)
	}
	var stored string
	e.db.QueryRow(`SELECT password_hash FROM users WHERE username = 'alice'`).Scan(&stored)
	if !strings.HasPrefix(stored, "$argon2id$") || strings.Contains(stored, testPassword) {
		t.Fatalf("password not stored as an argon2id hash: %q", stored)
	}
}

func TestAuditRows(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.svc.Login(ctx, "alice", "wrong wrong wrong", "10.0.0.7")
	e.svc.Login(ctx, testPassword, "x", "10.0.0.8") // a password typed into the username field
	e.login(t)

	type row struct{ actor, action, result, remote, details string }
	rows, err := e.db.Query(`SELECT actor, action, result, remote_addr, details FROM audit_log WHERE action = 'auth.login' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []row
	for rows.Next() {
		var r row
		rows.Scan(&r.actor, &r.action, &r.result, &r.remote, &r.details)
		got = append(got, r)
	}
	want := []row{
		{"alice", "auth.login", "failed", "10.0.0.7", `{"reason":"bad_password"}`},
		{"", "auth.login", "failed", "10.0.0.8", `{"reason":"unknown_user"}`},
		{"alice", "auth.login", "ok", "10.0.0.1", `{}`},
	}
	if len(got) != len(want) {
		t.Fatalf("audit rows %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	// Nothing in the audit log may contain a password or token.
	var all string
	dump, _ := e.db.Query(`SELECT actor || action || target_id || result || remote_addr || details FROM audit_log`)
	defer dump.Close()
	for dump.Next() {
		var s string
		dump.Scan(&s)
		all += s
	}
	for _, secret := range []string{testPassword, "wrong wrong wrong"} {
		if strings.Contains(all, secret) {
			t.Fatalf("audit log contains %q", secret)
		}
	}
	// Account creation by the CLI is audited too, without the password.
	if n := e.count(t, `SELECT count(*) FROM audit_log WHERE action = 'user.create' AND actor = 'test'`); n != 1 {
		t.Fatalf("user.create audit rows: %d", n)
	}
	var d string
	e.db.QueryRow(`SELECT details FROM audit_log WHERE action = 'user.create'`).Scan(&d)
	var m map[string]any
	if json.Unmarshal([]byte(d), &m) != nil || m["username"] != "alice" || len(m) != 1 {
		t.Fatalf("user.create details %q", d)
	}
}

func TestConcurrentRefreshWithSameTokenYieldsOneSuccessor(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.login(t)

	const n = 8
	results := make(chan TokenPair, n)
	errs := make(chan error, n)
	start := make(chan struct{})
	for range n {
		go func() {
			<-start
			tp, err := e.svc.Refresh(ctx, p.RefreshToken, "")
			results <- tp
			errs <- err
		}()
	}
	close(start)
	var first TokenPair
	for i := range n {
		tp, err := <-results, <-errs
		if err != nil {
			t.Fatalf("concurrent refresh %d: %v", i, err)
		}
		if i == 0 {
			first = tp
		} else if tp.RefreshToken != first.RefreshToken {
			t.Fatal("concurrent refreshes got different successors")
		}
	}
	if c := e.count(t, `SELECT count(*) FROM refresh_tokens`); c != 2 {
		t.Fatalf("%d rows, want 2", c)
	}
}

func TestMaintainPurgesOldRows(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.login(t)
	e.svc.Logout(ctx, Principal{UserID: e.user.ID}, p.RefreshToken, "")
	live := e.login(t)

	e.clk.Advance(2 * 24 * time.Hour)
	e.svc.Maintain(ctx)
	if c := e.count(t, `SELECT count(*) FROM refresh_tokens`); c != 1 {
		t.Fatalf("revoked row not purged after a day (%d rows)", c)
	}
	if _, err := e.svc.Refresh(ctx, live.RefreshToken, ""); err != nil {
		t.Fatalf("live token lost: %v", err)
	}
}
