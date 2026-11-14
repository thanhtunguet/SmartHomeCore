package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/thanhtunguet/homecore/internal/audit"
	"github.com/thanhtunguet/homecore/internal/secrets"
	"github.com/thanhtunguet/homecore/internal/storage"
)

const (
	// SettingJWTKey is the system_settings key of the sealed HS256 signing key.
	SettingJWTKey = "auth.jwt_signing_key"

	// DefaultRefreshGrace is how long after a rotation the same (already used) refresh token still
	// yields the already issued successor pair instead of revoking the family.
	DefaultRefreshGrace = 10 * time.Second

	refreshTokenBytes = 32
	successorAAD      = "refresh_tokens.successor"
)

// Errors returned by Service. Handlers map both to 401 with a generic message.
var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrInvalidRefresh     = errors.New("invalid or expired refresh token")
)

// TokenPair is what login and refresh return.
type TokenPair struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int // access token lifetime in seconds
}

// Config configures a Service.
type Config struct {
	DB         *storage.DB
	Sealer     *secrets.Sealer
	Audit      *audit.Writer // may be nil (no audit rows)
	Log        *slog.Logger
	AccessTTL  time.Duration
	RefreshTTL time.Duration
	Grace      time.Duration // 0 = DefaultRefreshGrace; negative disables the grace window
	Params     Params        // zero = DefaultParams
	Now        func() time.Time
}

// Service implements login, refresh-token rotation, logout and access-token authentication.
type Service struct {
	cfg      Config
	store    *Store
	accounts *Accounts
	hasher   *Hasher
	jwt      *JWT
	// dummyHash is verified when the user does not exist, so an unknown username costs the same
	// as a wrong password.
	dummyHash string
}

// NewService loads (or on first run creates) the sealed JWT signing key and builds the Service.
func NewService(ctx context.Context, cfg Config) (*Service, error) {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	if cfg.Params == (Params{}) {
		cfg.Params = DefaultParams
	}
	if cfg.Grace == 0 {
		cfg.Grace = DefaultRefreshGrace
	}
	if cfg.Grace < 0 {
		cfg.Grace = 0
	}
	s := &Service{cfg: cfg, store: NewStore(cfg.DB), hasher: NewHasher(cfg.Params)}
	s.accounts = NewAccounts(s.store, s.hasher, cfg.Audit, cfg.Log, cfg.Now)

	key, err := LoadOrCreateSigningKey(ctx, cfg.DB, cfg.Sealer, cfg.Now)
	if err != nil {
		return nil, err
	}
	s.jwt, err = NewJWT(key, cfg.AccessTTL, s.store, cfg.Now)
	if err != nil {
		return nil, err
	}
	pw := make([]byte, 24)
	if _, err := rand.Read(pw); err != nil {
		return nil, err
	}
	if s.dummyHash, err = s.hasher.Hash(ctx, base64.RawStdEncoding.EncodeToString(pw)); err != nil {
		return nil, err
	}
	return s, nil
}

// Authenticator is the access-token verifier (the *JWT).
func (s *Service) Authenticator() *JWT { return s.jwt }

// Accounts gives access to account management.
func (s *Service) Accounts() *Accounts { return s.accounts }

// Authenticate implements Authenticator.
func (s *Service) Authenticate(ctx context.Context, bearer string) (Principal, error) {
	return s.jwt.Authenticate(ctx, bearer)
}

// CurrentUser returns the account behind a principal.
func (s *Service) CurrentUser(ctx context.Context, p Principal) (User, error) {
	return s.store.UserByID(ctx, p.UserID)
}

// LoadOrCreateSigningKey returns the HS256 key stored sealed in system_settings, generating it on
// first run. It is an independent random value: not derived from the master key and unrelated to
// the Controller's Ed25519 key (security invariant, REQ-SEC-005).
func LoadOrCreateSigningKey(ctx context.Context, db *storage.DB, sealer *secrets.Sealer, now func() time.Time) ([]byte, error) {
	if now == nil {
		now = time.Now
	}
	aad := "system_settings." + SettingJWTKey
	var key []byte
	err := db.WithTx(ctx, func(tx *sql.Tx) error {
		sealed, ok, err := getSetting(ctx, tx, SettingJWTKey)
		if err != nil {
			return err
		}
		if ok {
			if key, err = sealer.Open(sealed, aad); err != nil {
				return fmt.Errorf("%w; the master key changed? deleting the %q row of system_settings creates a new key and signs everyone out", err, SettingJWTKey)
			}
			if len(key) != SigningKeySize {
				return fmt.Errorf("stored jwt signing key has %d bytes, want %d", len(key), SigningKeySize)
			}
			return nil
		}
		key = make([]byte, SigningKeySize)
		if _, err := rand.Read(key); err != nil {
			return err
		}
		if sealed, err = sealer.Seal(key, aad); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO system_settings (key, value, updated_at) VALUES (?, ?, ?)`,
			SettingJWTKey, sealed, storage.Millis(now()))
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("jwt signing key: %w", err)
	}
	return key, nil
}

// --- login ---------------------------------------------------------------------------------

// Login verifies the credentials and starts a new refresh-token family.
//
// Unknown users, disabled users and wrong passwords all return ErrInvalidCredentials after one
// argon2 verification each (a dummy hash stands in for an unknown user), so neither the response
// nor its timing tells them apart. The reason is recorded in the audit log only.
func (s *Service) Login(ctx context.Context, username, password, remoteAddr string) (TokenPair, error) {
	u, err := s.store.UserByUsername(ctx, username)
	found := err == nil
	if err != nil && !errors.Is(err, ErrUserNotFound) {
		return TokenPair{}, err
	}
	hash := s.dummyHash
	if found {
		hash = u.PasswordHash
	}
	ok, needsRehash, err := s.hasher.Verify(ctx, password, hash)
	if err != nil {
		return TokenPair{}, err
	}
	if !found || !ok || u.Disabled {
		reason := "bad_password"
		switch {
		case !found:
			reason = "unknown_user"
		case u.Disabled:
			reason = "disabled"
		}
		// The submitted username is recorded only when it names an existing account: people do
		// type passwords into the username field, and the log must never hold one.
		e := audit.Entry{Action: "auth.login", Result: audit.ResultFailed, RemoteAddr: remoteAddr,
			Details: map[string]any{"reason": reason}}
		if found {
			e.ActorUserID, e.Actor, e.TargetType, e.TargetID = u.ID, u.Username, "user", u.ID
		}
		s.record(ctx, e)
		return TokenPair{}, ErrInvalidCredentials
	}

	if needsRehash {
		if nh, err := s.hasher.Hash(ctx, password); err == nil {
			if err := s.store.UpdatePasswordHash(ctx, u.ID, nh, s.cfg.Now()); err != nil {
				s.cfg.Log.Warn("password hash upgrade failed", "user", u.Username, "err", err)
			}
		}
	}

	var pair TokenPair
	err = s.cfg.DB.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		pair, _, err = s.issue(ctx, tx, u, uuid.Must(uuid.NewV7()).String())
		return err
	})
	if err != nil {
		return TokenPair{}, err
	}
	s.record(ctx, audit.Entry{ActorUserID: u.ID, Actor: u.Username, Action: "auth.login", TargetType: "user",
		TargetID: u.ID, Result: audit.ResultOK, RemoteAddr: remoteAddr})
	return pair, nil
}

// issue mints an access token and a new refresh token in family and stores the latter's hash.
func (s *Service) issue(ctx context.Context, q querier, u User, family string) (TokenPair, time.Time, error) {
	access, exp, err := s.jwt.Issue(u)
	if err != nil {
		return TokenPair{}, time.Time{}, err
	}
	refresh, err := newRefreshToken()
	if err != nil {
		return TokenPair{}, time.Time{}, err
	}
	now := s.cfg.Now()
	if _, err := q.ExecContext(ctx, `INSERT INTO refresh_tokens (id, user_id, family_id, token_hash, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`, uuid.Must(uuid.NewV7()).String(), u.ID, family, hashToken(refresh),
		storage.Millis(now.Add(s.cfg.RefreshTTL)), storage.Millis(now)); err != nil {
		return TokenPair{}, time.Time{}, fmt.Errorf("store refresh token: %w", err)
	}
	return TokenPair{AccessToken: access, RefreshToken: refresh, ExpiresIn: int(s.jwt.TTL().Seconds())}, exp, nil
}

func newRefreshToken() (string, error) {
	b := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashToken(tok string) []byte {
	h := sha256.Sum256([]byte(tok))
	return h[:]
}

// --- refresh -------------------------------------------------------------------------------

type refreshRow struct {
	id, userID, familyID string
	expiresAt            int64
	usedAt, revokedAt    sql.NullInt64
	successor            []byte
}

// successorCopy is what is sealed on a used row: the pair handed out in exchange for it.
type successorCopy struct {
	Access        string `json:"a"`
	Refresh       string `json:"r"`
	AccessExpires int64  `json:"e"` // unix ms
}

// Refresh rotates a refresh token. Every outcome besides success is ErrInvalidRefresh.
//
//   - unknown, revoked or expired token: ErrInvalidRefresh.
//   - unused token: marked used; a new access token and a new refresh token (same family) are
//     issued, and a sealed copy of that pair is kept on the used row for the grace window.
//   - used token presented within the grace window: the same pair is returned again.
//   - used token presented later: someone replayed a rotated token; the whole family is revoked.
func (s *Service) Refresh(ctx context.Context, token, remoteAddr string) (TokenPair, error) {
	hash := hashToken(token)
	var (
		pair    TokenPair
		outcome error
		audited *audit.Entry
	)
	err := s.cfg.DB.WithTx(ctx, func(tx *sql.Tx) error {
		now := s.cfg.Now()
		nowMs := storage.Millis(now)
		var row refreshRow
		err := tx.QueryRowContext(ctx, `SELECT id, user_id, family_id, expires_at, used_at, revoked_at, successor_sealed
			FROM refresh_tokens WHERE token_hash = ?`, hash).
			Scan(&row.id, &row.userID, &row.familyID, &row.expiresAt, &row.usedAt, &row.revokedAt, &row.successor)
		if errors.Is(err, sql.ErrNoRows) {
			outcome = ErrInvalidRefresh
			return nil
		}
		if err != nil {
			return fmt.Errorf("lookup refresh token: %w", err)
		}
		if row.revokedAt.Valid || row.expiresAt <= nowMs {
			outcome = ErrInvalidRefresh
			return nil
		}
		u, err := getUser(ctx, tx, "id = ?", row.userID)
		if errors.Is(err, ErrUserNotFound) || (err == nil && u.Disabled) {
			outcome = ErrInvalidRefresh
			return revokeFamily(ctx, tx, row.familyID, nowMs)
		}
		if err != nil {
			return err
		}

		if row.usedAt.Valid {
			within := nowMs-row.usedAt.Int64 < s.cfg.Grace.Milliseconds()
			if within && len(row.successor) > 0 {
				if p, ok := s.openSuccessor(row.successor, now); ok {
					pair = p
					return nil
				}
			}
			outcome = ErrInvalidRefresh
			audited = &audit.Entry{ActorUserID: u.ID, Actor: u.Username, Action: "auth.refresh_reuse",
				TargetType: "user", TargetID: u.ID, Result: "revoked", RemoteAddr: remoteAddr,
				Details: map[string]any{"family_id": row.familyID, "seconds_since_rotation": (nowMs - row.usedAt.Int64) / 1000}}
			return revokeFamily(ctx, tx, row.familyID, nowMs)
		}

		next, exp, err := s.issue(ctx, tx, u, row.familyID)
		if err != nil {
			return err
		}
		blob, err := json.Marshal(successorCopy{Access: next.AccessToken, Refresh: next.RefreshToken, AccessExpires: storage.Millis(exp)})
		if err != nil {
			return err
		}
		sealed, err := s.cfg.Sealer.Seal(blob, successorAAD)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE refresh_tokens SET used_at = ?, successor_sealed = ? WHERE id = ?`,
			nowMs, sealed, row.id); err != nil {
			return err
		}
		// Anything older than the window in this family has no use any more.
		if _, err := tx.ExecContext(ctx, `UPDATE refresh_tokens SET successor_sealed = NULL
			WHERE family_id = ? AND successor_sealed IS NOT NULL AND id <> ? AND used_at <= ?`,
			row.familyID, row.id, nowMs-s.cfg.Grace.Milliseconds()); err != nil {
			return err
		}
		pair = next
		return nil
	})
	if err != nil {
		return TokenPair{}, err
	}
	if audited != nil {
		s.record(ctx, *audited)
	}
	if outcome != nil {
		return TokenPair{}, outcome
	}
	return pair, nil
}

func (s *Service) openSuccessor(sealed []byte, now time.Time) (TokenPair, bool) {
	b, err := s.cfg.Sealer.Open(sealed, successorAAD)
	if err != nil {
		return TokenPair{}, false
	}
	var c successorCopy
	if json.Unmarshal(b, &c) != nil || c.Access == "" || c.Refresh == "" {
		return TokenPair{}, false
	}
	left := time.UnixMilli(c.AccessExpires).Sub(now)
	if left <= 0 {
		return TokenPair{}, false
	}
	return TokenPair{AccessToken: c.Access, RefreshToken: c.Refresh, ExpiresIn: max(1, int(left.Seconds()))}, true
}

func revokeFamily(ctx context.Context, q querier, family string, nowMs int64) error {
	_, err := q.ExecContext(ctx, `UPDATE refresh_tokens SET revoked_at = COALESCE(revoked_at, ?), successor_sealed = NULL
		WHERE family_id = ?`, nowMs, family)
	return err
}

// --- logout --------------------------------------------------------------------------------

// Logout revokes the family of the given refresh token, provided it belongs to the caller. It is
// idempotent: an unknown, expired or already revoked token is not an error, and a token of
// another user is silently ignored. The caller's current access token stays valid until it
// expires (at most access_token_ttl).
func (s *Service) Logout(ctx context.Context, p Principal, refreshToken, remoteAddr string) error {
	var revoked bool
	err := s.cfg.DB.WithTx(ctx, func(tx *sql.Tx) error {
		var user, family string
		err := tx.QueryRowContext(ctx, `SELECT user_id, family_id FROM refresh_tokens WHERE token_hash = ?`,
			hashToken(refreshToken)).Scan(&user, &family)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && user != p.UserID) {
			return nil
		}
		if err != nil {
			return err
		}
		revoked = true
		return revokeFamily(ctx, tx, family, storage.Millis(s.cfg.Now()))
	})
	if err != nil {
		return err
	}
	res := audit.ResultOK
	if !revoked {
		res = "no_session"
	}
	s.record(ctx, audit.Entry{ActorUserID: p.UserID, Actor: p.Username, Action: "auth.logout", TargetType: "user",
		TargetID: p.UserID, Result: res, RemoteAddr: remoteAddr})
	return nil
}

// --- maintenance ---------------------------------------------------------------------------

const (
	// retainDead is how long revoked or expired refresh rows are kept before deletion.
	retainDead = 24 * time.Hour
	// retainUsed is how long rotated (used) rows are kept so a replayed old token is still
	// recognised as reuse and revokes its family. Older replays just fail as unknown tokens.
	retainUsed = 7 * 24 * time.Hour
)

// Maintain wipes sealed successor copies whose grace window has passed and deletes refresh tokens
// that expired or were revoked more than a day ago, or were rotated more than a week ago. It is
// idempotent and cheap.
func (s *Service) Maintain(ctx context.Context) error {
	nowMs := storage.Millis(s.cfg.Now())
	if _, err := s.cfg.DB.ExecContext(ctx, `UPDATE refresh_tokens SET successor_sealed = NULL
		WHERE successor_sealed IS NOT NULL AND (used_at IS NULL OR used_at <= ? OR revoked_at IS NOT NULL)`,
		nowMs-s.cfg.Grace.Milliseconds()); err != nil {
		return fmt.Errorf("wipe successor copies: %w", err)
	}
	cut := nowMs - retainDead.Milliseconds()
	if _, err := s.cfg.DB.ExecContext(ctx, `DELETE FROM refresh_tokens
		WHERE expires_at <= ? OR (revoked_at IS NOT NULL AND revoked_at <= ?) OR (used_at IS NOT NULL AND used_at <= ?)`,
		cut, cut, nowMs-retainUsed.Milliseconds()); err != nil {
		return fmt.Errorf("purge refresh tokens: %w", err)
	}
	return nil
}

// Run performs Maintain at start and then every minute until ctx is cancelled.
func (s *Service) Run(ctx context.Context) error {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		if err := s.Maintain(ctx); err != nil && ctx.Err() == nil {
			s.cfg.Log.Warn("auth maintenance failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

func (s *Service) record(ctx context.Context, e audit.Entry) {
	if s.cfg.Audit == nil {
		return
	}
	if err := s.cfg.Audit.Write(ctx, e); err != nil {
		s.cfg.Log.Error("audit write failed", "action", e.Action, "err", err)
	}
}
