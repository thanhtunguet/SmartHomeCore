package systemsettings

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/thanhtunguet/homecore/internal/storage"
)

const openAPIKey = "openapi_enabled"

// SQLiteRepository stores system settings in system_settings.
type SQLiteRepository struct{ db *storage.DB }

func NewSQLiteRepository(db *storage.DB) *SQLiteRepository { return &SQLiteRepository{db: db} }

func (r *SQLiteRepository) OpenAPIEnabled(ctx context.Context) (bool, error) {
	var value []byte
	err := r.db.QueryRowContext(ctx, `SELECT value FROM system_settings WHERE key = ?`, openAPIKey).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if len(value) != 1 || (value[0] != '0' && value[0] != '1') {
		return false, errors.New("invalid OpenAPI setting value")
	}
	return value[0] == '1', nil
}

func (r *SQLiteRepository) SetOpenAPIEnabled(ctx context.Context, enabled bool, now time.Time) error {
	value := []byte{'0'}
	if enabled {
		value[0] = '1'
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO system_settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		openAPIKey, value, storage.Millis(now))
	return err
}
