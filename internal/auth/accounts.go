package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/thanhtunguet/homecore/internal/audit"
)

// MaxUsernameLength matches maxLength in the OpenAPI LoginRequest.
const MaxUsernameLength = 64

// ValidateUsername accepts 1..64 ASCII letters, digits and . _ - @. ASCII only keeps the
// database's NOCASE uniqueness exact (it folds ASCII case only) and makes look-alike account
// names impossible.
func ValidateUsername(name string) error {
	if name == "" || len(name) > MaxUsernameLength {
		return fmt.Errorf("username must be 1 to %d characters", MaxUsernameLength)
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-', r == '@':
		default:
			return errors.New("username may only contain letters, digits and . _ - @")
		}
	}
	return nil
}

// Accounts creates and maintains user accounts. It is what `homecore user ...` uses, directly on
// the database, without a running Controller.
type Accounts struct {
	store  *Store
	hasher *Hasher
	audit  *audit.Writer
	log    *slog.Logger
	now    func() time.Time
}

// NewAccounts builds Accounts. now may be nil. log may be nil.
func NewAccounts(store *Store, hasher *Hasher, aud *audit.Writer, log *slog.Logger, now func() time.Time) *Accounts {
	if now == nil {
		now = time.Now
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Accounts{store: store, hasher: hasher, audit: aud, log: log, now: now}
}

// Store exposes the underlying store (read access for listing).
func (a *Accounts) Store() *Store { return a.store }

// Create adds an account. actor is recorded in the audit log ("cli" for the command line).
func (a *Accounts) Create(ctx context.Context, username, password, actor string) (User, error) {
	if err := ValidateUsername(username); err != nil {
		return User{}, err
	}
	if err := ValidateNewPassword(password); err != nil {
		return User{}, err
	}
	hash, err := a.hasher.Hash(ctx, password)
	if err != nil {
		return User{}, err
	}
	now := a.now()
	u := User{ID: uuid.Must(uuid.NewV7()).String(), Username: username, PasswordHash: hash, CreatedAt: now, UpdatedAt: now}
	if err := a.store.InsertUser(ctx, u); err != nil {
		return User{}, err
	}
	a.record(ctx, audit.Entry{Actor: actor, Action: "user.create", TargetType: "user", TargetID: u.ID,
		Result: audit.ResultOK, Details: map[string]any{"username": u.Username}})
	return u, nil
}

// SetPassword changes a password and revokes the user's refresh tokens. It returns the number of
// refresh tokens revoked.
func (a *Accounts) SetPassword(ctx context.Context, username, password, actor string) (revoked int64, err error) {
	if err := ValidateNewPassword(password); err != nil {
		return 0, err
	}
	u, err := a.store.UserByUsername(ctx, username)
	if err != nil {
		return 0, err
	}
	hash, err := a.hasher.Hash(ctx, password)
	if err != nil {
		return 0, err
	}
	revoked, err = a.store.ChangePassword(ctx, u.ID, hash, a.now())
	if err != nil {
		return 0, err
	}
	a.record(ctx, audit.Entry{Actor: actor, Action: "user.password_change", TargetType: "user", TargetID: u.ID,
		Result: audit.ResultOK, Details: map[string]any{"username": u.Username, "refresh_tokens_revoked": revoked}})
	return revoked, nil
}

// List returns all accounts.
func (a *Accounts) List(ctx context.Context) ([]User, error) { return a.store.ListUsers(ctx) }

// record writes an audit entry; a failure is logged, never fatal (the account change already
// happened and the operator is at the console).
func (a *Accounts) record(ctx context.Context, e audit.Entry) {
	if a.audit == nil {
		return
	}
	if err := a.audit.Write(ctx, e); err != nil {
		a.log.Error("audit write failed", "action", e.Action, "err", err)
	}
}
