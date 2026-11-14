package commands

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

const commandColumns = `id, device_id, type, status, requested_by, error_code, error, attempts, deadline_at,
	sent_at, acknowledged_at, completed_at, created_at, updated_at`

// active is the SQL condition behind the commands_one_active index.
const active = `status IN ('pending', 'sent', 'acknowledged')`

type rowScanner interface{ Scan(dest ...any) error }

func scanCommand(r rowScanner) (Command, error) {
	var (
		c                         Command
		typ, status               string
		requestedBy               sql.NullString
		deadline, sent, ack, done sql.NullInt64
		created, updated          int64
	)
	if err := r.Scan(&c.ID, &c.DeviceID, &typ, &status, &requestedBy, &c.ErrorCode, &c.Error, &c.Attempts,
		&deadline, &sent, &ack, &done, &created, &updated); err != nil {
		return Command{}, err
	}
	c.Type, c.Status, c.RequestedBy = Type(typ), Status(status), requestedBy.String
	c.DeadlineAt, c.SentAt, c.AcknowledgedAt, c.CompletedAt = nullTime(deadline), nullTime(sent), nullTime(ack), nullTime(done)
	c.CreatedAt, c.UpdatedAt = storage.FromMillis(created), storage.FromMillis(updated)
	return c, nil
}

func nullTime(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := storage.FromMillis(v.Int64)
	return &t
}

func millisPtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return storage.Millis(*t)
}

// Insert implements Repository.
func (r *SQLiteRepository) Insert(ctx context.Context, c Command) error {
	var requestedBy any
	if c.RequestedBy != "" {
		requestedBy = c.RequestedBy
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO commands (`+commandColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.DeviceID, string(c.Type), string(c.Status), requestedBy, c.ErrorCode, c.Error, c.Attempts,
		millisPtr(c.DeadlineAt), millisPtr(c.SentAt), millisPtr(c.AcknowledgedAt), millisPtr(c.CompletedAt),
		storage.Millis(c.CreatedAt), storage.Millis(c.UpdatedAt))
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: commands.device_id, commands.type") {
		return ErrActiveExists
	}
	if err != nil {
		return fmt.Errorf("insert command: %w", err)
	}
	return nil
}

// Get implements Repository.
func (r *SQLiteRepository) Get(ctx context.Context, id string) (Command, error) {
	c, err := scanCommand(r.db.QueryRowContext(ctx, `SELECT `+commandColumns+` FROM commands WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Command{}, ErrNotFound
	}
	if err != nil {
		return Command{}, fmt.Errorf("get command: %w", err)
	}
	return c, nil
}

// Active implements Repository.
func (r *SQLiteRepository) Active(ctx context.Context, deviceID string, t Type) (Command, error) {
	c, err := scanCommand(r.db.QueryRowContext(ctx, `SELECT `+commandColumns+` FROM commands
		WHERE device_id = ? AND type = ? AND `+active, deviceID, string(t)))
	if errors.Is(err, sql.ErrNoRows) {
		return Command{}, ErrNotFound
	}
	if err != nil {
		return Command{}, fmt.Errorf("active command: %w", err)
	}
	return c, nil
}

// Update implements Repository.
func (r *SQLiteRepository) Update(ctx context.Context, id string, from Status, ch Change) (bool, error) {
	at := storage.Millis(ch.At)
	var sent, ack, done any
	switch {
	case ch.To == StatusSent:
		sent = at
	case ch.To == StatusAcknowledged:
		ack = at
	case ch.To.Terminal():
		done = at
	}
	res, err := r.db.ExecContext(ctx, `UPDATE commands SET status = ?, error_code = ?, error = ?,
		attempts = CASE WHEN ? > 0 THEN ? ELSE attempts END,
		sent_at = COALESCE(?, sent_at), acknowledged_at = COALESCE(?, acknowledged_at),
		completed_at = COALESCE(?, completed_at), updated_at = ?
		WHERE id = ? AND status = ?`,
		string(ch.To), ch.ErrorCode, ch.Error, ch.Attempts, ch.Attempts, sent, ack, done, at, id, string(from))
	if err != nil {
		return false, fmt.Errorf("update command: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// List implements Repository.
func (r *SQLiteRepository) List(ctx context.Context, f ListFilter, after *ListAfter, limit int) ([]Command, error) {
	q := `SELECT ` + commandColumns + ` FROM commands WHERE 1 = 1`
	var args []any
	if f.DeviceID != "" {
		q += ` AND device_id = ?`
		args = append(args, f.DeviceID)
	}
	if f.Status != "" {
		q += ` AND status = ?`
		args = append(args, string(f.Status))
	}
	if after != nil {
		ms := storage.Millis(after.CreatedAt)
		q += ` AND (created_at < ? OR (created_at = ? AND id < ?))`
		args = append(args, ms, ms, after.ID)
	}
	q += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit)
	return r.query(ctx, q, args...)
}

func (r *SQLiteRepository) query(ctx context.Context, q string, args ...any) ([]Command, error) {
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query commands: %w", err)
	}
	defer rows.Close()
	out := []Command{}
	for rows.Next() {
		c, err := scanCommand(rows)
		if err != nil {
			return nil, fmt.Errorf("query commands: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ActiveForDevices implements Repository.
func (r *SQLiteRepository) ActiveForDevices(ctx context.Context, deviceIDs []string) ([]ActiveRef, error) {
	if len(deviceIDs) == 0 {
		return nil, nil
	}
	args := make([]any, len(deviceIDs))
	for i, id := range deviceIDs {
		args[i] = id
	}
	rows, err := r.db.QueryContext(ctx, `SELECT device_id, type, created_at FROM commands
		WHERE `+active+` AND device_id IN (?`+strings.Repeat(", ?", len(deviceIDs)-1)+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("active commands: %w", err)
	}
	defer rows.Close()
	var out []ActiveRef
	for rows.Next() {
		var (
			ref     ActiveRef
			typ     string
			created int64
		)
		if err := rows.Scan(&ref.DeviceID, &typ, &created); err != nil {
			return nil, fmt.Errorf("active commands: %w", err)
		}
		ref.Type, ref.CreatedAt = Type(typ), storage.FromMillis(created)
		out = append(out, ref)
	}
	return out, rows.Err()
}

// Pending implements Repository.
func (r *SQLiteRepository) Pending(ctx context.Context) ([]Command, error) {
	return r.query(ctx, `SELECT `+commandColumns+` FROM commands WHERE status = 'pending' ORDER BY created_at, id`)
}

// Expire implements Repository.
func (r *SQLiteRepository) Expire(ctx context.Context, now time.Time) (int64, error) {
	ms := storage.Millis(now)
	var total int64
	err := r.db.WithTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE commands SET status = 'timed_out', completed_at = ?, updated_at = ?
			WHERE status IN ('sent', 'acknowledged') AND deadline_at IS NOT NULL AND deadline_at <= ?`, ms, ms, ms)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		total += n
		res, err = tx.ExecContext(ctx, `UPDATE commands SET status = 'failed', error_code = 'interrupted',
			error = 'the Controller stopped before the command was dispatched', completed_at = ?, updated_at = ?
			WHERE status = 'pending' AND deadline_at IS NOT NULL AND deadline_at <= ?`, ms, ms, ms)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		total += n
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("expire commands: %w", err)
	}
	return total, nil
}

// PruneTerminal implements Repository.
func (r *SQLiteRepository) PruneTerminal(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM commands WHERE id IN (SELECT id FROM commands
		WHERE status IN ('completed', 'failed', 'timed_out') AND updated_at < ? LIMIT ?)`,
		storage.Millis(cutoff), limit)
	if err != nil {
		return 0, fmt.Errorf("prune commands: %w", err)
	}
	return res.RowsAffected()
}
