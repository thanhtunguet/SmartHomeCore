package activity

import (
	"context"
	"fmt"
	"strings"

	"github.com/thanhtunguet/homecore/internal/storage"
)

// SQLiteRepository reads audit_log.
type SQLiteRepository struct{ db *storage.DB }

var _ Repository = (*SQLiteRepository)(nil)

// NewSQLiteRepository wraps db.
func NewSQLiteRepository(db *storage.DB) *SQLiteRepository { return &SQLiteRepository{db: db} }

// categoryWhere is the SQL form of CategoryOf (alias a = audit_log).
var categoryWhere = map[Category]string{
	CategoryAuth:    `a.action LIKE 'auth.%'`,
	CategoryUser:    `a.action LIKE 'user.%'`,
	CategoryDDNS:    `a.action LIKE 'ddns.%'`,
	CategoryCommand: `a.action IN ('device.wake', 'device.shutdown')`,
	CategoryDevice:  `a.action LIKE 'device.%' AND a.action NOT IN ('device.wake', 'device.shutdown')`,
	CategorySystem: `a.action NOT LIKE 'auth.%' AND a.action NOT LIKE 'user.%' AND a.action NOT LIKE 'ddns.%'
		AND a.action NOT LIKE 'device.%'`,
}

// List implements Repository. Order: at DESC, id DESC (index audit_log_at serves the sort).
func (r *SQLiteRepository) List(ctx context.Context, f Filter, after *After, limit int) ([]Raw, error) {
	q := `SELECT a.id, a.at, a.actor, a.action, a.target_type, a.target_id, a.result, a.details, COALESCE(d.name, '')
		FROM audit_log a LEFT JOIN devices d ON a.target_type = 'device' AND d.id = a.target_id`
	var (
		where []string
		args  []any
	)
	if f.DeviceID != "" {
		where = append(where, `a.target_type = 'device' AND a.target_id = ?`)
		args = append(args, f.DeviceID)
	}
	if f.Category != "" {
		where = append(where, "("+categoryWhere[f.Category]+")")
	}
	if after != nil {
		ms := storage.Millis(after.At)
		where = append(where, `(a.at < ? OR (a.at = ? AND a.id < ?))`)
		args = append(args, ms, ms, after.ID)
	}
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += ` ORDER BY a.at DESC, a.id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list activity: %w", err)
	}
	defer rows.Close()
	out := []Raw{}
	for rows.Next() {
		var (
			x  Raw
			at int64
		)
		if err := rows.Scan(&x.ID, &at, &x.Actor, &x.Action, &x.TargetType, &x.TargetID, &x.Result, &x.Details, &x.DeviceName); err != nil {
			return nil, fmt.Errorf("list activity: %w", err)
		}
		x.At = storage.FromMillis(at)
		out = append(out, x)
	}
	return out, rows.Err()
}
