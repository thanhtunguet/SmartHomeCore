// Package servicekey implements service account keys (ADR-018): long-lived, individually
// revocable credentials for the legacy endpoints and for AI-agent (MCP) integrations.
//
// The Controller generates an Ed25519 key pair for every key and keeps the private key (its
// 32-byte seed) sealed with the master key in service_account_keys. The credential given to the
// client is a JWT (alg EdDSA, kid = key id) signed with that private key. It is shown once by the
// API and never stored, but it is fully determined by the row, so a rotation that is repeated
// inside the grace window can return the same successor (see Rotate).
//
// Authentication looks the key up by `kid` on every request, verifies the signature with the
// stored public key and checks the row (not revoked, not expired, not past its rotation grace) and
// the owning user (exists, not disabled). Revoking a key therefore takes effect immediately and
// individually, which the HS256 legacy tokens of ADR-017 cannot do.
//
// A key belongs to a user; actions made with it are attributed to that user (ADR-011, no roles).
// What a key may call is its scope, enforced by the API middleware from the OpenAPI spec.
package servicekey

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/thanhtunguet/homecore/internal/audit"
	"github.com/thanhtunguet/homecore/internal/auth"
	"github.com/thanhtunguet/homecore/internal/secrets"
	"github.com/thanhtunguet/homecore/internal/storage"
)

// Scopes a key can have. They are matched against the `x-token-scopes` of each operation.
const (
	ScopeLegacy = auth.ScopeLegacy // /api/turn-on, /api/turn-off, /api/is-online
	ScopeMCP    = "mcp"            // device control and read-only monitoring for AI agents
)

// Limits and defaults.
const (
	DefaultTTL    = 365 * 24 * time.Hour // 1 year
	MinTTL        = time.Minute
	MaxTTL        = auth.MaxLegacyTokenTTL // 20 years
	MaxNameLength = 64

	// RotationGrace is how long a key keeps working after it was rotated. It lets an agent whose
	// rotate response was lost retry with the old key and get the same new key, and lets requests
	// already in flight with the old key finish. Short on purpose: it is the window in which a
	// stolen old key is still useful.
	RotationGrace = time.Minute

	lastUsedInterval = time.Minute
)

// Algorithm is the JWT algorithm of a service account key credential.
const Algorithm = "EdDSA"

// Statuses reported by the API.
const (
	StatusActive  = "active"
	StatusRotated = "rotated" // replaced by a rotation; dead once the grace window has passed
	StatusExpired = "expired"
	StatusRevoked = "revoked"
)

// Errors returned by the service.
var (
	ErrNotFound = errors.New("service account key not found")
	// ErrInactive means the key to rotate is revoked, expired or past its rotation grace.
	ErrInactive = errors.New("service account key is not active")
	// ErrRotatedAway means the key was rotated and its successor is no longer usable.
	ErrRotatedAway = errors.New("service account key was already rotated and its successor is gone")
)

// ValidationError is a rejected input (name, scope, ttl).
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

// Key is the stored description of a key. It never contains key material.
type Key struct {
	ID           string
	UserID       string
	Name         string
	Scope        string
	TTL          time.Duration
	CreatedAt    time.Time
	ExpiresAt    time.Time
	RevokedAt    *time.Time
	SupersededAt *time.Time
	RotatedTo    string
	LastUsedAt   *time.Time
}

// Status is the state of the key at now.
func (k Key) Status(now time.Time) string {
	switch {
	case k.RevokedAt != nil:
		return StatusRevoked
	case k.SupersededAt != nil:
		return StatusRotated
	case !now.Before(k.ExpiresAt):
		return StatusExpired
	}
	return StatusActive
}

// Usable reports whether the key authenticates requests at now.
func (k Key) Usable(now time.Time) bool {
	if k.RevokedAt != nil || !now.Before(k.ExpiresAt) {
		return false
	}
	return k.SupersededAt == nil || now.Before(k.SupersededAt.Add(RotationGrace))
}

// Issued is a key together with its credential, the only time the credential is returned.
type Issued struct {
	Key        Key
	Credential string // the secret: show once, never log or audit it
}

// Rotation is the result of a rotation.
type Rotation struct {
	Issued
	PreviousID         string
	PreviousValidUntil time.Time // when the old key stops working (end of the grace window)
	Replayed           bool      // the old key had already been rotated; this is the same successor again
}

// Caller identifies who acts, for the audit trail.
type Caller struct {
	UserID     string
	Username   string
	RemoteAddr string
	// ViaKeyID is set when the caller authenticated with a service account key (self-rotation).
	ViaKeyID string
}

// Config configures a Service.
type Config struct {
	DB     *storage.DB
	Sealer *secrets.Sealer
	Users  auth.UserLookup // default: the users table
	Audit  *audit.Writer   // nil: no audit entries
	Now    func() time.Time
}

// Service creates, lists, revokes and rotates keys and authenticates them.
type Service struct {
	db     *storage.DB
	sealer *secrets.Sealer
	users  auth.UserLookup
	audit  *audit.Writer
	now    func() time.Time
}

var _ auth.Authenticator = (*Service)(nil)

// New returns a Service.
func New(c Config) (*Service, error) {
	if c.DB == nil || c.Sealer == nil {
		return nil, errors.New("servicekey: DB and Sealer are required")
	}
	if c.Users == nil {
		c.Users = auth.NewStore(c.DB)
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return &Service{db: c.DB, sealer: c.Sealer, users: c.Users, audit: c.Audit, now: c.Now}, nil
}

func (s *Service) clock() time.Time { return s.now().UTC().Truncate(time.Millisecond) }

// ValidateScope reports whether scope is a scope a key can have.
func ValidateScope(scope string) error {
	if scope != ScopeLegacy && scope != ScopeMCP {
		return &ValidationError{Msg: fmt.Sprintf("scope must be %q or %q", ScopeLegacy, ScopeMCP)}
	}
	return nil
}

func validateName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > MaxNameLength ||
		strings.ContainsFunc(name, func(r rune) bool { return r < ' ' || r == 0x7f }) {
		return "", &ValidationError{Msg: fmt.Sprintf("name must be 1 to %d characters without control characters", MaxNameLength)}
	}
	return name, nil
}

func validateTTL(ttl time.Duration) error {
	if ttl < MinTTL || ttl > MaxTTL {
		return &ValidationError{Msg: fmt.Sprintf("ttl_seconds must be between %d and %d", int64(MinTTL.Seconds()), int64(MaxTTL.Seconds()))}
	}
	return nil
}

// Create generates a key pair and a key for userID. ttl 0 means DefaultTTL.
func (s *Service) Create(ctx context.Context, caller Caller, name, scope string, ttl time.Duration) (Issued, error) {
	name, err := validateName(name)
	if err != nil {
		return Issued{}, err
	}
	if err := ValidateScope(scope); err != nil {
		return Issued{}, err
	}
	if ttl == 0 {
		ttl = DefaultTTL
	}
	if err := validateTTL(ttl); err != nil {
		return Issued{}, err
	}
	var out Issued
	err = s.db.WithTx(ctx, func(tx *sql.Tx) error {
		k, cred, err := s.insert(ctx, tx, caller.UserID, name, scope, ttl, s.clock())
		if err != nil {
			return err
		}
		out = Issued{Key: k, Credential: cred}
		return s.auditTx(ctx, tx, caller, "service_key.create", k.ID, map[string]any{
			"key_id": k.ID, "name": k.Name, "scope": k.Scope, "expires_at": k.ExpiresAt.Format(time.RFC3339)})
	})
	return out, err
}

// insert generates and stores a new key and returns it with its credential.
func (s *Service) insert(ctx context.Context, tx *sql.Tx, userID, name, scope string, ttl time.Duration, now time.Time) (Key, string, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Key{}, "", err
	}
	id := uuid.Must(uuid.NewV7()).String()
	sealed, err := s.sealer.Seal(priv.Seed(), aad(id))
	if err != nil {
		return Key{}, "", err
	}
	k := Key{ID: id, UserID: userID, Name: name, Scope: scope, TTL: ttl, CreatedAt: now, ExpiresAt: now.Add(ttl)}
	if _, err := tx.ExecContext(ctx, `INSERT INTO service_account_keys
		(id, user_id, name, scope, public_key, private_key_sealed, ttl_seconds, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		k.ID, k.UserID, k.Name, k.Scope, []byte(pub), sealed, int64(ttl/time.Second),
		storage.Millis(k.CreatedAt), storage.Millis(k.ExpiresAt)); err != nil {
		return Key{}, "", fmt.Errorf("insert service account key: %w", err)
	}
	return k, sign(k, priv), nil
}

// List returns the keys of userID, newest first.
func (s *Service) List(ctx context.Context, userID string) ([]Key, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+keyColumns+` FROM service_account_keys
		WHERE user_id = ? ORDER BY created_at DESC, id DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list service account keys: %w", err)
	}
	defer rows.Close()
	var out []Key
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// Revoke ends a key of the caller at once. It is idempotent; a key the caller does not own is
// ErrNotFound, as if it did not exist.
func (s *Service) Revoke(ctx context.Context, caller Caller, id string) error {
	return s.db.WithTx(ctx, func(tx *sql.Tx) error {
		k, err := getKey(ctx, tx, id, caller.UserID)
		if err != nil {
			return err
		}
		if k.RevokedAt != nil {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE service_account_keys SET revoked_at = ? WHERE id = ?`,
			storage.Millis(s.clock()), id); err != nil {
			return err
		}
		return s.auditTx(ctx, tx, caller, "service_key.revoke", id, map[string]any{"key_id": id, "name": k.Name})
	})
}

// Rotate replaces the key keyID (the one that authenticated the request) with a new key of the
// same owner, name and scope, and a new key pair. ttl 0 keeps the old key's TTL.
//
// The old key is validated again inside the transaction (not revoked, not expired). It is not
// revoked but superseded: it keeps working for RotationGrace, so a client that lost the response
// can retry. A retry with the old key inside that window is answered with the same successor
// (Replayed) instead of creating a second one; after the window the old key is dead.
func (s *Service) Rotate(ctx context.Context, caller Caller, keyID string, ttl time.Duration) (Rotation, error) {
	if ttl != 0 {
		if err := validateTTL(ttl); err != nil {
			return Rotation{}, err
		}
	}
	var out Rotation
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		now := s.clock()
		old, err := getKey(ctx, tx, keyID, caller.UserID)
		if err != nil {
			return err
		}
		if !old.Usable(now) {
			return ErrInactive
		}

		if old.SupersededAt != nil { // a retry: hand out the successor again
			next, err := getKey(ctx, tx, old.RotatedTo, caller.UserID)
			if err != nil || !next.Usable(now) || next.SupersededAt != nil {
				return ErrRotatedAway
			}
			cred, err := s.credentialOf(ctx, tx, next)
			if err != nil {
				return err
			}
			out = Rotation{Issued: Issued{Key: next, Credential: cred}, PreviousID: old.ID,
				PreviousValidUntil: old.SupersededAt.Add(RotationGrace), Replayed: true}
			return s.auditTx(ctx, tx, caller, "service_key.rotate", old.ID, map[string]any{
				"key_id": old.ID, "new_key_id": next.ID, "replayed": true})
		}

		if ttl == 0 {
			ttl = old.TTL
		}
		next, cred, err := s.insert(ctx, tx, old.UserID, old.Name, old.Scope, ttl, now)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE service_account_keys SET superseded_at = ?, rotated_to = ? WHERE id = ?`,
			storage.Millis(now), next.ID, old.ID); err != nil {
			return err
		}
		validUntil := now.Add(RotationGrace)
		if old.ExpiresAt.Before(validUntil) {
			validUntil = old.ExpiresAt
		}
		out = Rotation{Issued: Issued{Key: next, Credential: cred}, PreviousID: old.ID, PreviousValidUntil: validUntil}
		return s.auditTx(ctx, tx, caller, "service_key.rotate", old.ID, map[string]any{
			"key_id": old.ID, "new_key_id": next.ID, "name": next.Name, "scope": next.Scope,
			"expires_at": next.ExpiresAt.Format(time.RFC3339)})
	})
	return out, err
}

// credentialOf rebuilds the credential of a stored key from its sealed private key.
func (s *Service) credentialOf(ctx context.Context, q queryer, k Key) (string, error) {
	var sealed []byte
	if err := q.QueryRowContext(ctx, `SELECT private_key_sealed FROM service_account_keys WHERE id = ?`, k.ID).Scan(&sealed); err != nil {
		return "", err
	}
	seed, err := s.sealer.Open(sealed, aad(k.ID))
	if err != nil {
		return "", fmt.Errorf("service account key %s: %w", k.ID, err)
	}
	if len(seed) != ed25519.SeedSize {
		return "", fmt.Errorf("service account key %s: stored private key has %d bytes", k.ID, len(seed))
	}
	return sign(k, ed25519.NewKeyFromSeed(seed)), nil
}

// Authenticate implements auth.Authenticator for an EdDSA service account key credential.
// Every failure is the same auth.ErrUnauthenticated.
func (s *Service) Authenticate(ctx context.Context, token string) (auth.Principal, error) {
	h, rest, ok := strings.Cut(token, ".")
	if !ok || len(token) > 2048 {
		return auth.Principal{}, auth.ErrUnauthenticated
	}
	p, sigPart, ok := strings.Cut(rest, ".")
	if !ok || strings.Contains(sigPart, ".") {
		return auth.Principal{}, auth.ErrUnauthenticated
	}
	kid, ok := parseHeader(h)
	if !ok {
		return auth.Principal{}, auth.ErrUnauthenticated
	}
	if _, err := uuid.Parse(kid); err != nil {
		return auth.Principal{}, auth.ErrUnauthenticated
	}

	var pub []byte
	row := s.db.QueryRowContext(ctx, `SELECT public_key FROM service_account_keys WHERE id = ?`, kid)
	if err := row.Scan(&pub); err != nil || len(pub) != ed25519.PublicKeySize {
		return auth.Principal{}, auth.ErrUnauthenticated
	}
	sig, err := b64url.DecodeString(sigPart)
	if err != nil || !ed25519.Verify(ed25519.PublicKey(pub), []byte(h+"."+p), sig) {
		return auth.Principal{}, auth.ErrUnauthenticated
	}
	rawClaims, err := b64url.DecodeString(p)
	if err != nil {
		return auth.Principal{}, auth.ErrUnauthenticated
	}
	var c claims
	if json.Unmarshal(rawClaims, &c) != nil || c.ID != kid {
		return auth.Principal{}, auth.ErrUnauthenticated
	}

	// The signature proves the server issued this token; the row decides whether it still counts.
	k, err := getKey(ctx, s.db, kid, "")
	now := s.clock()
	if err != nil || c.Subject != k.UserID || c.Scope != k.Scope || !k.Usable(now) {
		return auth.Principal{}, auth.ErrUnauthenticated
	}
	u, err := s.users.UserByID(ctx, k.UserID)
	if err != nil || u.Disabled {
		return auth.Principal{}, auth.ErrUnauthenticated
	}
	// Best effort and at most once a minute, so a busy client does not write on every request.
	_, _ = s.db.ExecContext(ctx, `UPDATE service_account_keys SET last_used_at = ?
		WHERE id = ? AND (last_used_at IS NULL OR last_used_at < ?)`,
		storage.Millis(now), kid, storage.Millis(now.Add(-lastUsedInterval)))
	return auth.Principal{UserID: u.ID, Username: u.Username, Scope: k.Scope, KeyID: k.ID}, nil
}

// Prune deletes keys that ended (expired, revoked or rotated) before cutoff, at most limit rows.
// It is a retention.Target.
func (s *Service) Prune(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	c := storage.Millis(cutoff)
	res, err := s.db.ExecContext(ctx, `DELETE FROM service_account_keys WHERE id IN (
		SELECT id FROM service_account_keys
		WHERE expires_at < ? OR revoked_at < ? OR superseded_at < ? LIMIT ?)`, c, c, c, limit)
	if err != nil {
		return 0, fmt.Errorf("prune service account keys: %w", err)
	}
	return res.RowsAffected()
}

func (s *Service) auditTx(ctx context.Context, tx *sql.Tx, c Caller, action, target string, details map[string]any) error {
	if s.audit == nil {
		return nil
	}
	if c.ViaKeyID != "" {
		details["via_key_id"] = c.ViaKeyID
	}
	return s.audit.WriteTx(ctx, tx, audit.Entry{ActorUserID: c.UserID, Actor: c.Username, Action: action,
		TargetType: "service_account_key", TargetID: target, Result: audit.ResultOK, RemoteAddr: c.RemoteAddr, Details: details})
}

// aad binds a sealed private key to its column and its row.
func aad(id string) string { return "service_account_keys.private_key:" + id }
