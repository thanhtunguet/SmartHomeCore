package servicekey

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/thanhtunguet/homecore/internal/storage"
)

var b64url = base64.RawURLEncoding

// claims are the JWT claims of a credential. They are a function of the stored row, so signing
// the same key twice yields the same token (Ed25519 signatures are deterministic).
type claims struct {
	Subject  string `json:"sub"` // owning user id
	IssuedAt int64  `json:"iat"` // unix seconds
	Expires  int64  `json:"exp"` // unix seconds; informational, the row is the authority
	ID       string `json:"jti"` // key id, equal to the header's kid
	Scope    string `json:"scp"`
}

// sign builds the credential of k with its private key.
func sign(k Key, priv ed25519.PrivateKey) string {
	hdr, _ := json.Marshal(struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
		Kid string `json:"kid"`
	}{Algorithm, "JWT", k.ID})
	payload, _ := json.Marshal(claims{
		Subject: k.UserID, IssuedAt: k.CreatedAt.Unix(), Expires: k.ExpiresAt.Unix(), ID: k.ID, Scope: k.Scope,
	})
	input := b64url.EncodeToString(hdr) + "." + b64url.EncodeToString(payload)
	return input + "." + b64url.EncodeToString(ed25519.Sign(priv, []byte(input)))
}

// parseHeader returns the kid of a strictly valid header: alg EdDSA, typ JWT (or absent), a kid,
// and no other member (no jku, jwk, crit and the like).
func parseHeader(h string) (kid string, ok bool) {
	raw, err := b64url.DecodeString(h)
	if err != nil {
		return "", false
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return "", false
	}
	for name, v := range m {
		s, isString := v.(string)
		switch name {
		case "alg":
			if !isString || s != Algorithm {
				return "", false
			}
		case "typ":
			if !isString || !strings.EqualFold(s, "JWT") {
				return "", false
			}
		case "kid":
			if !isString || s == "" {
				return "", false
			}
			kid = s
		default:
			return "", false
		}
	}
	_, hasAlg := m["alg"]
	return kid, hasAlg && kid != ""
}

// queryer is the part of *sql.DB and *sql.Tx the lookups need.
type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

const keyColumns = `id, user_id, name, scope, ttl_seconds, created_at, expires_at, revoked_at, superseded_at, rotated_to, last_used_at`

func scanKey(row interface{ Scan(...any) error }) (Key, error) {
	var (
		k                  Key
		ttl, created, exp  int64
		revoked, super, lu sql.NullInt64
		rotatedTo          sql.NullString
	)
	if err := row.Scan(&k.ID, &k.UserID, &k.Name, &k.Scope, &ttl, &created, &exp, &revoked, &super, &rotatedTo, &lu); err != nil {
		return Key{}, err
	}
	k.TTL = time.Duration(ttl) * time.Second
	k.CreatedAt, k.ExpiresAt = storage.FromMillis(created), storage.FromMillis(exp)
	k.RevokedAt, k.SupersededAt, k.LastUsedAt = optTime(revoked), optTime(super), optTime(lu)
	k.RotatedTo = rotatedTo.String
	return k, nil
}

func optTime(n sql.NullInt64) *time.Time {
	if !n.Valid {
		return nil
	}
	t := storage.FromMillis(n.Int64)
	return &t
}

// getKey loads a key; owner "" skips the ownership check (authentication knows the key, not the user).
func getKey(ctx context.Context, q queryer, id, owner string) (Key, error) {
	query, args := `SELECT `+keyColumns+` FROM service_account_keys WHERE id = ?`, []any{id}
	if owner != "" {
		query += ` AND user_id = ?`
		args = append(args, owner)
	}
	k, err := scanKey(q.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return Key{}, ErrNotFound
	}
	if err != nil {
		return Key{}, fmt.Errorf("query service account key: %w", err)
	}
	return k, nil
}
