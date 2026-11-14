package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/thanhtunguet/homecore/internal/storage"
)

// User is an account row. There are no roles (ADR-011).
type User struct {
	ID           string
	Username     string
	PasswordHash string
	Disabled     bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Errors returned by account operations.
var (
	ErrUsernameTaken = errors.New("username already exists")
	ErrUserNotFound  = errors.New("user not found")
)

// querier is the subset of *sql.DB and *sql.Tx the queries below need.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

const userColumns = `id, username, password_hash, disabled, created_at, updated_at`

func scanUser(row interface{ Scan(...any) error }) (User, error) {
	var (
		u        User
		disabled int
		c, up    int64
	)
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &disabled, &c, &up); err != nil {
		return User{}, err
	}
	u.Disabled = disabled != 0
	u.CreatedAt, u.UpdatedAt = storage.FromMillis(c), storage.FromMillis(up)
	return u, nil
}

func getUser(ctx context.Context, q querier, where string, arg any) (User, error) {
	u, err := scanUser(q.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE `+where, arg))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("query user: %w", err)
	}
	return u, nil
}

// Store is the SQLite persistence of users, refresh tokens and the auth settings.
type Store struct{ db *storage.DB }

// NewStore wraps db.
func NewStore(db *storage.DB) *Store { return &Store{db: db} }

// UserByID implements UserLookup.
func (s *Store) UserByID(ctx context.Context, id string) (User, error) {
	return getUser(ctx, s.db, "id = ?", id)
}

// UserByUsername finds a user, ignoring ASCII case.
func (s *Store) UserByUsername(ctx context.Context, username string) (User, error) {
	return getUser(ctx, s.db, "username = ?", username)
}

// ListUsers returns all users ordered by username.
func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userColumns+` FROM users ORDER BY username COLLATE NOCASE`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// CountUsers returns the number of accounts.
func (s *Store) CountUsers(ctx context.Context) (n int, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

// InsertUser stores u. It returns ErrUsernameTaken on a (case-insensitive) duplicate.
func (s *Store) InsertUser(ctx context.Context, u User) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO users (id, username, password_hash, disabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`, u.ID, u.Username, u.PasswordHash, boolInt(u.Disabled),
		storage.Millis(u.CreatedAt), storage.Millis(u.UpdatedAt))
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: users.username") {
		return ErrUsernameTaken
	}
	if err != nil {
		return fmt.Errorf("insert user: %w", err)
	}
	return nil
}

// UpdatePasswordHash replaces the hash without touching sessions (used for parameter upgrades).
func (s *Store) UpdatePasswordHash(ctx context.Context, id, hash string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`,
		hash, storage.Millis(now), id)
	return err
}

// ChangePassword sets a new hash and, in the same transaction, revokes every refresh token of the
// user and wipes any sealed successor copies. Access tokens already issued stay valid until they
// expire (at most access_token_ttl).
func (s *Store) ChangePassword(ctx context.Context, id, hash string, now time.Time) (revoked int64, err error) {
	err = s.db.WithTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`,
			hash, storage.Millis(now), id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrUserNotFound
		}
		res, err = tx.ExecContext(ctx, `UPDATE refresh_tokens SET revoked_at = ?, successor_sealed = NULL
			WHERE user_id = ? AND revoked_at IS NULL`, storage.Millis(now), id)
		if err != nil {
			return err
		}
		revoked, _ = res.RowsAffected()
		_, err = tx.ExecContext(ctx, `UPDATE refresh_tokens SET successor_sealed = NULL
			WHERE user_id = ? AND successor_sealed IS NOT NULL`, id)
		return err
	})
	return revoked, err
}

// setting reads a settings value; ok is false when the key does not exist.
func getSetting(ctx context.Context, q querier, key string) (val []byte, ok bool, err error) {
	err = q.QueryRowContext(ctx, `SELECT value FROM system_settings WHERE key = ?`, key).Scan(&val)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	return val, err == nil, err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
