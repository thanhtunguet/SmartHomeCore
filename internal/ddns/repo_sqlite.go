package ddns

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/thanhtunguet/homecore/internal/storage"
)

// SQLiteRepository implements Repository on the Controller database.
type SQLiteRepository struct{ db *storage.DB }

var _ Repository = (*SQLiteRepository)(nil)

// NewSQLiteRepository wraps db.
func NewSQLiteRepository(db *storage.DB) *SQLiteRepository { return &SQLiteRepository{db: db} }

const selectConfig = `SELECT id, name, zone_id, cf_record_id, record_name, record_type, proxied, ttl,
	interval_seconds, enabled, api_token_sealed, last_ip, detected_ip, dns_value, last_checked_at,
	last_synced_at, last_error, last_error_at, created_at, updated_at FROM ddns_configs`

type rowScanner interface{ Scan(dest ...any) error }

func scanStored(r rowScanner) (Stored, error) {
	var (
		s                      Stored
		c                      = &s.Config
		typ                    string
		proxied, enabled       int
		intervalS              int64
		checked, synced, errAt sql.NullInt64
		created, updated       int64
	)
	if err := r.Scan(&c.ID, &c.Name, &c.ZoneID, &c.RecordID, &c.RecordName, &typ, &proxied, &c.TTL,
		&intervalS, &enabled, &s.TokenSealed, &c.LastIP, &c.DetectedIP, &c.DNSValue, &checked,
		&synced, &c.LastError, &errAt, &created, &updated); err != nil {
		return Stored{}, err
	}
	c.RecordType = RecordType(typ)
	c.Proxied, c.Enabled = proxied == 1, enabled == 1
	c.Interval = time.Duration(intervalS) * time.Second
	c.HasToken = len(s.TokenSealed) > 0
	c.LastCheckedAt, c.LastSyncedAt, c.LastErrorAt = nullTime(checked), nullTime(synced), nullTime(errAt)
	c.CreatedAt, c.UpdatedAt = storage.FromMillis(created), storage.FromMillis(updated)
	return s, nil
}

func nullTime(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := storage.FromMillis(v.Int64)
	return &t
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// mapConstraint turns the driver's unique-constraint text into ErrDuplicate.
func mapConstraint(err error) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: ddns_configs.") {
		return ErrDuplicate
	}
	return err
}

// Create implements Repository.
func (r *SQLiteRepository) Create(ctx context.Context, s Stored) error {
	c := s.Config
	_, err := r.db.ExecContext(ctx, `INSERT INTO ddns_configs
		(id, name, zone_id, cf_record_id, record_name, record_type, proxied, ttl, interval_seconds, enabled,
		 api_token_sealed, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.Name, c.ZoneID, c.RecordID, c.RecordName, string(c.RecordType), boolInt(c.Proxied), c.TTL,
		int64(c.Interval/time.Second), boolInt(c.Enabled), s.TokenSealed,
		storage.Millis(c.CreatedAt), storage.Millis(c.UpdatedAt))
	if err = mapConstraint(err); err != nil {
		if errors.Is(err, ErrDuplicate) {
			return err
		}
		return fmt.Errorf("create ddns config: %w", err)
	}
	return nil
}

// Get implements Repository.
func (r *SQLiteRepository) Get(ctx context.Context, id string) (Stored, error) {
	s, err := scanStored(r.db.QueryRowContext(ctx, selectConfig+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Stored{}, ErrNotFound
	}
	if err != nil {
		return Stored{}, fmt.Errorf("get ddns config: %w", err)
	}
	return s, nil
}

// List implements Repository.
func (r *SQLiteRepository) List(ctx context.Context) ([]Config, error) {
	rows, err := r.db.QueryContext(ctx, selectConfig+` ORDER BY name COLLATE NOCASE, created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list ddns configs: %w", err)
	}
	defer rows.Close()
	out := []Config{}
	for rows.Next() {
		s, err := scanStored(rows)
		if err != nil {
			return nil, fmt.Errorf("list ddns configs: %w", err)
		}
		out = append(out, s.Config)
	}
	return out, rows.Err()
}

// Update implements Repository.
func (r *SQLiteRepository) Update(ctx context.Context, s Stored, resetState bool) error {
	c := s.Config
	q := `UPDATE ddns_configs SET name = ?, zone_id = ?, cf_record_id = ?, record_name = ?, record_type = ?,
		proxied = ?, ttl = ?, interval_seconds = ?, enabled = ?, api_token_sealed = ?, updated_at = ?`
	if resetState {
		q += `, last_ip = '', detected_ip = '', dns_value = '', last_checked_at = NULL, last_synced_at = NULL,
			last_error = '', last_error_at = NULL`
	}
	res, err := r.db.ExecContext(ctx, q+` WHERE id = ?`,
		c.Name, c.ZoneID, c.RecordID, c.RecordName, string(c.RecordType), boolInt(c.Proxied), c.TTL,
		int64(c.Interval/time.Second), boolInt(c.Enabled), s.TokenSealed, storage.Millis(c.UpdatedAt), c.ID)
	if err = mapConstraint(err); err != nil {
		if errors.Is(err, ErrDuplicate) {
			return err
		}
		return fmt.Errorf("update ddns config: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete implements Repository.
func (r *SQLiteRepository) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM ddns_configs WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete ddns config: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SaveOutcome implements Repository.
func (r *SQLiteRepository) SaveOutcome(ctx context.Context, id string, version time.Time, o Outcome) (bool, error) {
	at := storage.Millis(o.At)
	applied := false
	err := r.db.WithTx(ctx, func(tx *sql.Tx) error {
		var (
			res sql.Result
			err error
		)
		if o.Result == ResultFailed {
			// Discovery or the read may not have got that far: keep the previous values then.
			res, err = tx.ExecContext(ctx, `UPDATE ddns_configs SET
				detected_ip = CASE WHEN ?1 = '' THEN detected_ip ELSE ?1 END,
				dns_value = CASE WHEN ?2 = '' THEN dns_value ELSE ?2 END,
				last_checked_at = ?3, last_error = ?4, last_error_at = ?3
				WHERE id = ?5 AND updated_at = ?6`,
				o.IP, o.DNSValue, at, o.Error, id, storage.Millis(version))
		} else {
			res, err = tx.ExecContext(ctx, `UPDATE ddns_configs SET
				last_ip = ?1, detected_ip = ?1, dns_value = ?2, last_checked_at = ?3, last_synced_at = ?3,
				last_error = '', last_error_at = NULL
				WHERE id = ?4 AND updated_at = ?5`,
				o.IP, o.DNSValue, at, id, storage.Millis(version))
		}
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil
		}
		applied = true
		_, err = tx.ExecContext(ctx, `INSERT INTO ddns_sync_history (config_id, result, ip, error, at)
			VALUES (?, ?, ?, ?, ?)`, id, string(o.Result), o.IP, o.Error, at)
		return err
	})
	if err != nil {
		return false, fmt.Errorf("save ddns outcome: %w", err)
	}
	return applied, nil
}

// History implements Repository.
func (r *SQLiteRepository) History(ctx context.Context, id string, limit int) ([]Outcome, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT result, ip, error, at FROM ddns_sync_history
		WHERE config_id = ? ORDER BY at DESC, id DESC LIMIT ?`, id, limit)
	if err != nil {
		return nil, fmt.Errorf("ddns history: %w", err)
	}
	defer rows.Close()
	var out []Outcome
	for rows.Next() {
		var (
			o  Outcome
			at int64
			re string
		)
		if err := rows.Scan(&re, &o.IP, &o.Error, &at); err != nil {
			return nil, err
		}
		o.Result, o.At = Result(re), storage.FromMillis(at)
		out = append(out, o)
	}
	return out, rows.Err()
}
