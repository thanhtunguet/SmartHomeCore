package portforward

import (
	"context"
	"database/sql"
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

const selectRule = `SELECT id, name, protocol, listen_address, listen_port, target_host, target_port,
	enabled, created_at, updated_at FROM port_forward_rules`

type rowScanner interface{ Scan(dest ...any) error }

func scanRule(s rowScanner) (Rule, error) {
	var (
		r                Rule
		proto            string
		enabled          int
		created, updated int64
	)
	if err := s.Scan(&r.ID, &r.Name, &proto, &r.ListenAddress, &r.ListenPort, &r.TargetHost, &r.TargetPort,
		&enabled, &created, &updated); err != nil {
		return Rule{}, err
	}
	r.Protocol, r.Enabled = Protocol(proto), enabled == 1
	r.CreatedAt, r.UpdatedAt = storage.FromMillis(created), storage.FromMillis(updated)
	return r, nil
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

// Create implements Repository.
func (r *SQLiteRepository) Create(ctx context.Context, p Rule) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO port_forward_rules
		(id, name, protocol, listen_address, listen_port, target_host, target_port, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Name, string(p.Protocol), p.ListenAddress, p.ListenPort, p.TargetHost, p.TargetPort,
		boolInt(p.Enabled), storage.Millis(p.CreatedAt), storage.Millis(p.UpdatedAt))
	if err = mapConstraint(err); err != nil {
		if errors.Is(err, ErrNameTaken) {
			return err
		}
		return fmt.Errorf("create port forwarding rule: %w", err)
	}
	return nil
}

// Get implements Repository.
func (r *SQLiteRepository) Get(ctx context.Context, id string) (Rule, error) {
	p, err := scanRule(r.db.QueryRowContext(ctx, selectRule+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Rule{}, ErrNotFound
	}
	if err != nil {
		return Rule{}, fmt.Errorf("get port forwarding rule: %w", err)
	}
	return p, nil
}

// List implements Repository.
func (r *SQLiteRepository) List(ctx context.Context) ([]Rule, error) {
	rows, err := r.db.QueryContext(ctx, selectRule+` ORDER BY name COLLATE NOCASE, created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list port forwarding rules: %w", err)
	}
	defer rows.Close()
	out := []Rule{}
	for rows.Next() {
		p, err := scanRule(rows)
		if err != nil {
			return nil, fmt.Errorf("list port forwarding rules: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Update implements Repository.
func (r *SQLiteRepository) Update(ctx context.Context, p Rule) error {
	res, err := r.db.ExecContext(ctx, `UPDATE port_forward_rules SET name = ?, protocol = ?, listen_address = ?,
		listen_port = ?, target_host = ?, target_port = ?, enabled = ?, updated_at = ? WHERE id = ?`,
		p.Name, string(p.Protocol), p.ListenAddress, p.ListenPort, p.TargetHost, p.TargetPort,
		boolInt(p.Enabled), storage.Millis(p.UpdatedAt), p.ID)
	if err = mapConstraint(err); err != nil {
		if errors.Is(err, ErrNameTaken) {
			return err
		}
		return fmt.Errorf("update port forwarding rule: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete implements Repository.
func (r *SQLiteRepository) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM port_forward_rules WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete port forwarding rule: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
