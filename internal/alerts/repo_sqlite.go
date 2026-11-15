package alerts

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/thanhtunguet/homecore/internal/storage"
)

// SQLiteRepository implements Repository on the Controller database.
type SQLiteRepository struct{ db *storage.DB }

var _ Repository = (*SQLiteRepository)(nil)

// NewSQLiteRepository wraps db.
func NewSQLiteRepository(db *storage.DB) *SQLiteRepository { return &SQLiteRepository{db: db} }

const selectChannel = `SELECT id, name, type, enabled, events, settings, secret_sealed, last_sent_at,
	last_error, last_error_at, created_at, updated_at FROM alert_channels`

type rowScanner interface{ Scan(dest ...any) error }

func scanStored(r rowScanner) (Stored, error) {
	var (
		s                Stored
		c                = &s.Channel
		enabled          int
		events, settings string
		sent, errAt      sql.NullInt64
		created, updated int64
	)
	if err := r.Scan(&c.ID, &c.Name, &c.Type, &enabled, &events, &settings, &s.SecretSealed, &sent,
		&c.LastError, &errAt, &created, &updated); err != nil {
		return Stored{}, err
	}
	c.Enabled = enabled == 1
	if err := json.Unmarshal([]byte(events), &c.Events); err != nil {
		return Stored{}, fmt.Errorf("alert channel %s: events: %w", c.ID, err)
	}
	if err := json.Unmarshal([]byte(settings), &c.Settings); err != nil {
		return Stored{}, fmt.Errorf("alert channel %s: settings: %w", c.ID, err)
	}
	if c.Settings == nil {
		c.Settings = map[string]string{}
	}
	c.HasSecret = len(s.SecretSealed) > 0
	if sent.Valid {
		t := storage.FromMillis(sent.Int64)
		c.LastSentAt = &t
	}
	if errAt.Valid {
		t := storage.FromMillis(errAt.Int64)
		c.LastErrorAt = &t
	}
	c.CreatedAt, c.UpdatedAt = storage.FromMillis(created), storage.FromMillis(updated)
	return s, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// mapConstraint turns the driver's unique-constraint text into ErrNameTaken.
func mapConstraint(err error) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return ErrNameTaken
	}
	return err
}

func encode(c Channel) (events, settings []byte, err error) {
	if events, err = json.Marshal(c.Events); err != nil {
		return nil, nil, err
	}
	settings, err = json.Marshal(c.Settings)
	return events, settings, err
}

// Create implements Repository.
func (r *SQLiteRepository) Create(ctx context.Context, s Stored) error {
	c := s.Channel
	events, settings, err := encode(c)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO alert_channels
		(id, name, type, enabled, events, settings, secret_sealed, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.Name, c.Type, boolInt(c.Enabled), string(events), string(settings), s.SecretSealed,
		storage.Millis(c.CreatedAt), storage.Millis(c.UpdatedAt))
	if err = mapConstraint(err); err != nil {
		if errors.Is(err, ErrNameTaken) {
			return err
		}
		return fmt.Errorf("create alert channel: %w", err)
	}
	return nil
}

// Get implements Repository.
func (r *SQLiteRepository) Get(ctx context.Context, id string) (Stored, error) {
	s, err := scanStored(r.db.QueryRowContext(ctx, selectChannel+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Stored{}, ErrNotFound
	}
	if err != nil {
		return Stored{}, fmt.Errorf("get alert channel: %w", err)
	}
	return s, nil
}

// List implements Repository.
func (r *SQLiteRepository) List(ctx context.Context) ([]Channel, error) {
	rows, err := r.db.QueryContext(ctx, selectChannel+` ORDER BY name COLLATE NOCASE, created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list alert channels: %w", err)
	}
	defer rows.Close()
	out := []Channel{}
	for rows.Next() {
		s, err := scanStored(rows)
		if err != nil {
			return nil, fmt.Errorf("list alert channels: %w", err)
		}
		out = append(out, s.Channel)
	}
	return out, rows.Err()
}

// Update implements Repository.
func (r *SQLiteRepository) Update(ctx context.Context, s Stored) error {
	c := s.Channel
	events, settings, err := encode(c)
	if err != nil {
		return err
	}
	res, err := r.db.ExecContext(ctx, `UPDATE alert_channels SET name = ?, enabled = ?, events = ?,
		settings = ?, secret_sealed = ?, updated_at = ? WHERE id = ?`,
		c.Name, boolInt(c.Enabled), string(events), string(settings), s.SecretSealed,
		storage.Millis(c.UpdatedAt), c.ID)
	if err = mapConstraint(err); err != nil {
		if errors.Is(err, ErrNameTaken) {
			return err
		}
		return fmt.Errorf("update alert channel: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete implements Repository.
func (r *SQLiteRepository) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM alert_channels WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete alert channel: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RecordDelivery implements Repository.
func (r *SQLiteRepository) RecordDelivery(ctx context.Context, id string, d Delivery) error {
	at := storage.Millis(d.At)
	var err error
	if d.Error == "" {
		_, err = r.db.ExecContext(ctx, `UPDATE alert_channels SET last_sent_at = ?, last_error = '',
			last_error_at = NULL WHERE id = ?`, at, id)
	} else {
		_, err = r.db.ExecContext(ctx, `UPDATE alert_channels SET last_error = ?, last_error_at = ?
			WHERE id = ?`, d.Error, at, id)
	}
	if err != nil {
		return fmt.Errorf("record alert delivery: %w", err)
	}
	return nil
}
