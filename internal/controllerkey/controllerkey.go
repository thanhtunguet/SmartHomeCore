// Package controllerkey manages the Controller's Ed25519 signing key (ADR-001, REQ-SEC-005): the
// key pair that signs commands to Agents. The private key (its 32-byte seed) is stored sealed with
// the master key in controller_keys, bound to the column by the AAD "controller_keys.private_key";
// the public key is what operators put into an Agent's trusted_controllers.
//
// The key is generated from crypto/rand on first use and is independent of the JWT signing key
// and the master key. Exactly one row is active (retired_at IS NULL); the database enforces it
// with the controller_keys_one_active unique index. Rotation is not implemented yet.
package controllerkey

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/thanhtunguet/homecore/internal/agent/protocol"
	"github.com/thanhtunguet/homecore/internal/secrets"
	"github.com/thanhtunguet/homecore/internal/storage"
)

// AAD binds the sealed private key to its column.
const AAD = "controller_keys.private_key"

// Algorithm is the only signature algorithm (reported by the API).
const Algorithm = "ed25519"

// Key is the active Controller key. It holds the private key: never log it, never serialise it.
type Key struct {
	ID        string // protocol.KeyID of the public key
	CreatedAt time.Time
	public    ed25519.PublicKey
	private   ed25519.PrivateKey
}

// Public returns the public part, safe to show.
func (k *Key) Public() Public {
	return Public{KeyID: k.ID, Algorithm: Algorithm, PublicKey: protocol.EncodePublicKey(k.public)}
}

// Sign signs an agent command with this key (protocol.Sign).
func (k *Key) Sign(cmd protocol.Command) (protocol.SignedRequest, error) {
	return protocol.Sign(k.private, cmd)
}

// String never reveals the private key, even through %v / %+v.
func (k *Key) String() string { return "controllerkey.Key{" + k.ID + "}" }

// GoString keeps %#v from printing the private key as well.
func (k *Key) GoString() string { return k.String() }

// Public is the non-secret description of a key (GET /system/controller-key, `homecore key show`).
type Public struct {
	KeyID     string
	Algorithm string
	PublicKey string // standard base64 of the 32-byte public key
}

// ErrNoKey means no active key exists (ReadPublic on a database that never had a Controller run).
var ErrNoKey = errors.New("no controller key yet: start the Controller once, or run `homecore key show`")

// LoadOrCreate returns the active key, generating and storing one on first use. created tells
// whether this call generated it. Opening the sealed key fails (with a hint) when the master key
// changed; the key is then never silently replaced, because Agents trust the old one.
func LoadOrCreate(ctx context.Context, db *storage.DB, sealer *secrets.Sealer, now func() time.Time) (key *Key, created bool, err error) {
	if now == nil {
		now = time.Now
	}
	// A concurrent first start (two processes) may lose the insert race; the second pass then
	// finds the winner's row.
	for attempt := 0; attempt < 2; attempt++ {
		key, created, err = loadOrCreate(ctx, db, sealer, now)
		if err == nil || !isUniqueViolation(err) {
			break
		}
	}
	if err != nil {
		return nil, false, fmt.Errorf("controller key: %w", err)
	}
	return key, created, nil
}

func loadOrCreate(ctx context.Context, db *storage.DB, sealer *secrets.Sealer, now func() time.Time) (*Key, bool, error) {
	var key *Key
	var created bool
	err := db.WithTx(ctx, func(tx *sql.Tx) error {
		var (
			pub, sealed []byte
			createdAt   int64
		)
		err := tx.QueryRowContext(ctx, `SELECT public_key, private_key_sealed, created_at
			FROM controller_keys WHERE retired_at IS NULL`).Scan(&pub, &sealed, &createdAt)
		switch {
		case err == nil:
			k, err := open(sealer, pub, sealed, createdAt)
			key = k
			return err
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}

		publicKey, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		sealedSeed, err := sealer.Seal(priv.Seed(), AAD)
		if err != nil {
			return err
		}
		at := now().UTC().Truncate(time.Millisecond)
		keyID := protocol.KeyID(publicKey)
		if _, err := tx.ExecContext(ctx, `INSERT INTO controller_keys (key_id, public_key, private_key_sealed, created_at)
			VALUES (?, ?, ?, ?)`, keyID, []byte(publicKey), sealedSeed, storage.Millis(at)); err != nil {
			return err
		}
		key, created = &Key{ID: keyID, CreatedAt: at, public: publicKey, private: priv}, true
		return nil
	})
	return key, created, err
}

// open unseals a stored key and checks that the parts belong together.
func open(sealer *secrets.Sealer, pub, sealed []byte, createdAt int64) (*Key, error) {
	seed, err := sealer.Open(sealed, AAD)
	if err != nil {
		return nil, fmt.Errorf("%w; the master key changed? the stored key cannot be recovered, and Agents trust it, so it is not replaced automatically", err)
	}
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("stored private key has %d bytes, want %d", len(seed), ed25519.SeedSize)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	derived := priv.Public().(ed25519.PublicKey)
	if len(pub) != ed25519.PublicKeySize || !derived.Equal(ed25519.PublicKey(pub)) {
		return nil, errors.New("stored public key does not match the private key")
	}
	return &Key{ID: protocol.KeyID(derived), CreatedAt: storage.FromMillis(createdAt), public: derived, private: priv}, nil
}

// ReadPublic returns the active public key without needing the master key. ErrNoKey if none.
func ReadPublic(ctx context.Context, db *storage.DB) (Public, error) {
	var pub []byte
	err := db.QueryRowContext(ctx, `SELECT public_key FROM controller_keys WHERE retired_at IS NULL`).Scan(&pub)
	if errors.Is(err, sql.ErrNoRows) {
		return Public{}, ErrNoKey
	}
	if err != nil {
		return Public{}, fmt.Errorf("read controller key: %w", err)
	}
	if len(pub) != ed25519.PublicKeySize {
		return Public{}, errors.New("stored controller public key is corrupt")
	}
	p := ed25519.PublicKey(pub)
	return Public{KeyID: protocol.KeyID(p), Algorithm: Algorithm, PublicKey: protocol.EncodePublicKey(p)}, nil
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
