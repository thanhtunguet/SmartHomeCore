package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/thanhtunguet/homecore/internal/audit"
	"github.com/thanhtunguet/homecore/internal/secrets"
	"github.com/thanhtunguet/homecore/internal/storage"
)

// MaxLegacyTokenTTL bounds `homecore token create --ttl`. The tokens cannot be refreshed, so they
// are long-lived by design (ADR-017); the cap only keeps the expiry inside a sane range.
const MaxLegacyTokenTTL = 20 * 365 * 24 * time.Hour

// LegacyToken is a freshly issued long-lived client token.
type LegacyToken struct {
	Token   string // the secret; show once, never log or audit it
	ID      string // the jti, the only part that is safe to record
	User    User
	Expires time.Time
}

// IssueLegacyToken creates a long-lived token for the legacy device endpoints, owned by an existing
// user. It works directly on the database, like `homecore user`, so the Controller need not be
// running. The token carries ScopeLegacy: it is rejected on every other operation. It stays valid
// until it expires, the account is disabled or deleted, or the JWT signing key is rotated.
//
// The audit entry records the user, the label and the token id, never the token.
func IssueLegacyToken(ctx context.Context, db *storage.DB, sealer *secrets.Sealer, username, label string,
	ttl time.Duration, aud *audit.Writer, now func() time.Time) (LegacyToken, error) {
	if ttl <= 0 || ttl > MaxLegacyTokenTTL {
		return LegacyToken{}, fmt.Errorf("ttl must be between 1s and %s", MaxLegacyTokenTTL)
	}
	store := NewStore(db)
	u, err := store.UserByUsername(ctx, username)
	if err != nil {
		return LegacyToken{}, err
	}
	if u.Disabled {
		return LegacyToken{}, errors.New("user is disabled")
	}
	key, err := LoadOrCreateSigningKey(ctx, db, sealer, now)
	if err != nil {
		return LegacyToken{}, err
	}
	j, err := NewJWT(key, ttl, store, now)
	if err != nil {
		return LegacyToken{}, err
	}
	tok, exp, err := j.IssueScoped(u, ttl, ScopeLegacy)
	if err != nil {
		return LegacyToken{}, err
	}
	claims, err := j.Parse(tok)
	if err != nil {
		return LegacyToken{}, err
	}
	if aud != nil {
		_ = aud.Write(ctx, audit.Entry{Actor: "cli", Action: "auth.legacy_token_issue", TargetType: "user", TargetID: u.ID,
			Result: audit.ResultOK, Details: map[string]any{"username": u.Username, "label": label, "token_id": claims.ID, "expires_at": exp.UTC().Format(time.RFC3339)}})
	}
	return LegacyToken{Token: tok, ID: claims.ID, User: u, Expires: exp}, nil
}
