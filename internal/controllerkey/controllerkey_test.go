package controllerkey

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thanhtunguet/homecore/internal/agent/protocol"
	"github.com/thanhtunguet/homecore/internal/auth"
	"github.com/thanhtunguet/homecore/internal/secrets"
	"github.com/thanhtunguet/homecore/internal/storage"
)

func newDB(t *testing.T) *storage.DB {
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
	return db
}

func newSealer(t *testing.T, fill byte) *secrets.Sealer {
	t.Helper()
	s, err := secrets.NewSealer(bytes.Repeat([]byte{fill}, secrets.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// REQ-SEC-005 / REQ-AG-002: generated once, sealed at rest, stable across loads, usable to sign.
func TestKeyIsCreatedOnceSealedAndStable(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	sealer := newSealer(t, 1)

	k1, created, err := LoadOrCreate(ctx, db, sealer, nil)
	if err != nil || !created {
		t.Fatalf("first: created=%v err=%v", created, err)
	}
	k2, created, err := LoadOrCreate(ctx, db, sealer, nil)
	if err != nil || created || k2.ID != k1.ID || !bytes.Equal(k1.private, k2.private) {
		t.Fatalf("second: created=%v err=%v id=%s/%s", created, err, k1.ID, k2.ID)
	}

	// key_id is the protocol's: first 8 bytes of SHA-256(public key), 16 hex chars.
	if len(k1.ID) != 16 || k1.ID != protocol.KeyID(k1.public) {
		t.Fatalf("key id %q", k1.ID)
	}
	pub := k1.Public()
	if pub.Algorithm != "ed25519" || pub.KeyID != k1.ID {
		t.Fatalf("public: %+v", pub)
	}
	if _, err := protocol.ParsePublicKey(pub.PublicKey); err != nil {
		t.Fatalf("public key is not what an agent accepts: %v", err)
	}

	// At rest: one active row; the seed is not stored in clear and opens only with the right AAD.
	var rows int
	var storedPub, sealed []byte
	if err := db.QueryRow(`SELECT count(*), public_key, private_key_sealed FROM controller_keys WHERE retired_at IS NULL`).Scan(&rows, &storedPub, &sealed); err != nil || rows != 1 {
		t.Fatalf("active rows: %d %v", rows, err)
	}
	seed := k1.private.Seed()
	if bytes.Contains(sealed, seed) || bytes.Contains(sealed, k1.private) {
		t.Fatal("the private key is stored in clear")
	}
	if got, err := sealer.Open(sealed, AAD); err != nil || !bytes.Equal(got, seed) {
		t.Fatalf("sealed value does not open with the documented AAD: %v", err)
	}
	if _, err := sealer.Open(sealed, "system_settings.jwt_signing_key"); err == nil {
		t.Fatal("sealed key opens with another column's AAD")
	}

	// A signature made with the loaded key verifies against the published public key.
	cmd, err := protocol.NewCommand("0193a0b2-7c4e-7f00-8000-0000000000aa", "0193a0b2-7c4e-7f00-8000-000000000001", protocol.ActionShutdown, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	req, err := k2.Sign(cmd)
	if err != nil || req.KeyID != k1.ID {
		t.Fatalf("sign: %+v %v", req, err)
	}
	v, err := protocol.NewVerifier(protocol.VerifierConfig{
		AgentID: cmd.AgentID, TrustedKeys: []ed25519.PublicKey{storedPub}, AllowedActions: []protocol.Action{protocol.ActionShutdown},
		ClockSkew: time.Minute, StartedAt: time.Now().Add(-time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, verr := v.Verify(req.KeyID, req.Signature, req.Body); verr != nil {
		t.Fatalf("an agent trusting the stored public key rejects the Controller's signature: %v", verr)
	}
}

// Security invariant: the Controller key, the JWT signing key and the master key are distinct.
func TestKeyIsDistinctFromJWTAndMasterKey(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	master := bytes.Repeat([]byte{9}, secrets.KeySize)
	sealer, _ := secrets.NewSealer(master)
	jwt, err := auth.LoadOrCreateSigningKey(ctx, db, sealer, nil)
	if err != nil {
		t.Fatal(err)
	}
	k, _, err := LoadOrCreate(ctx, db, sealer, nil)
	if err != nil {
		t.Fatal(err)
	}
	seed := k.private.Seed()
	if bytes.Equal(seed, master) || bytes.Equal(seed, jwt) || bytes.Equal(k.public, master) || bytes.Equal(k.public, jwt) {
		t.Fatal("the Controller key shares material with the master key or the JWT key")
	}
}

func TestWrongMasterKeyIsAnErrorNotAReplacement(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	k, _, err := LoadOrCreate(ctx, db, newSealer(t, 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = LoadOrCreate(ctx, db, newSealer(t, 2), nil)
	if err == nil || !strings.Contains(err.Error(), "master key") {
		t.Fatalf("expected a master key hint, got %v", err)
	}
	var id string
	var n int
	_ = db.QueryRow(`SELECT count(*), max(key_id) FROM controller_keys`).Scan(&n, &id)
	if n != 1 || id != k.ID {
		t.Fatalf("the stored key was replaced: %d rows, %s", n, id)
	}
}

func TestTamperedRowIsRejected(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	sealer := newSealer(t, 1)
	if _, _, err := LoadOrCreate(ctx, db, sealer, nil); err != nil {
		t.Fatal(err)
	}
	other, _, _ := ed25519.GenerateKey(nil)
	if _, err := db.Exec(`UPDATE controller_keys SET public_key = ?`, []byte(other)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadOrCreate(ctx, db, sealer, nil); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("public key swapped in the database: %v", err)
	}
}

// Two processes starting at once (Controller and `homecore key show`) end up with one key.
func TestConcurrentFirstUseYieldsOneKey(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	sealer := newSealer(t, 1)
	var wg sync.WaitGroup
	ids := make([]string, 6)
	errs := make([]error, 6)
	for i := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			k, _, err := LoadOrCreate(ctx, db, sealer, nil)
			if err == nil {
				ids[i] = k.ID
			}
			errs[i] = err
		}()
	}
	wg.Wait()
	for i := range ids {
		if errs[i] != nil || ids[i] != ids[0] {
			t.Fatalf("goroutine %d: id=%q err=%v (ids %v)", i, ids[i], errs[i], ids)
		}
	}
	var n int
	_ = db.QueryRow(`SELECT count(*) FROM controller_keys`).Scan(&n)
	if n != 1 {
		t.Fatalf("%d key rows", n)
	}
}

// The database itself refuses a second active key (controller_keys_one_active).
func TestDatabaseAllowsOnlyOneActiveKey(t *testing.T) {
	db := newDB(t)
	_, _, _ = LoadOrCreate(context.Background(), db, newSealer(t, 1), nil)
	_, err := db.Exec(`INSERT INTO controller_keys (key_id, public_key, private_key_sealed, created_at) VALUES ('x', x'00', x'00', 1)`)
	if err == nil || !strings.Contains(err.Error(), "UNIQUE") {
		t.Fatalf("second active key accepted: %v", err)
	}
}

func TestReadPublicWithoutMasterKey(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	if _, err := ReadPublic(ctx, db); err != ErrNoKey {
		t.Fatalf("empty db: %v", err)
	}
	k, _, _ := LoadOrCreate(ctx, db, newSealer(t, 1), nil)
	p, err := ReadPublic(ctx, db)
	if err != nil || p != k.Public() {
		t.Fatalf("read public: %+v %v", p, err)
	}
}

// Printing a Key by accident must not leak the private key.
func TestKeyNeverFormatsItsPrivatePart(t *testing.T) {
	k, _, err := LoadOrCreate(context.Background(), newDB(t), newSealer(t, 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	seedHex := fmt.Sprintf("%x", k.private.Seed())
	for _, s := range []string{fmt.Sprint(k), fmt.Sprintf("%v", k), fmt.Sprintf("%+v", k), fmt.Sprintf("%#v", k), fmt.Sprintf("%s", k)} {
		if strings.Contains(s, seedHex) || strings.Contains(s, fmt.Sprint([]byte(k.private))) {
			t.Fatalf("formatted key leaks the private key: %s", s)
		}
	}
}
