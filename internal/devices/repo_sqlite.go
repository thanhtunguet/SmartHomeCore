package devices

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

// deviceSelect lists the columns scanDevice expects, joined with the optional binding.
const deviceSelect = `SELECT d.id, d.name, d.description, d.hostname, d.ip_address, d.mac_address,
	d.wol_broadcast_address, d.wol_port, d.device_type, d.os, d.monitor_port,
	d.monitor_interval_seconds, d.monitor_timeout_ms, d.enabled, d.status, d.last_seen_at,
	d.last_checked_at, d.last_latency_ms, d.created_at, d.updated_at,
	a.agent_id, a.port, a.scheme, a.status, a.version, a.last_ok_at
	FROM devices d LEFT JOIN agent_bindings a ON a.device_id = d.id`

type rowScanner interface{ Scan(dest ...any) error }

func scanDevice(r rowScanner) (Device, error) {
	var (
		d                              Device
		devType, osName, status        string
		enabled                        int
		intervalS, timeoutMs           int64
		lastSeen, lastChecked, latency sql.NullInt64
		created, updated               int64
		agentID, agentScheme           sql.NullString
		agentStatus, agentVersion      sql.NullString
		agentPort, agentOKAt           sql.NullInt64
	)
	if err := r.Scan(&d.ID, &d.Name, &d.Description, &d.Hostname, &d.IPAddress, &d.MACAddress,
		&d.WOLBroadcastAddress, &d.WOLPort, &devType, &osName, &d.MonitorPort, &intervalS, &timeoutMs,
		&enabled, &status, &lastSeen, &lastChecked, &latency, &created, &updated,
		&agentID, &agentPort, &agentScheme, &agentStatus, &agentVersion, &agentOKAt); err != nil {
		return Device{}, err
	}
	d.Type, d.OS, d.Status = Type(devType), OS(osName), Status(status)
	d.Enabled = enabled == 1
	d.MonitorInterval = time.Duration(intervalS) * time.Second
	d.MonitorTimeout = time.Duration(timeoutMs) * time.Millisecond
	d.LastSeenAt = nullTime(lastSeen)
	d.LastCheckedAt = nullTime(lastChecked)
	if latency.Valid {
		v := int(latency.Int64)
		d.LastLatencyMs = &v
	}
	d.CreatedAt, d.UpdatedAt = storage.FromMillis(created), storage.FromMillis(updated)
	if agentID.Valid {
		d.Agent = &AgentBinding{
			AgentID: agentID.String, Port: int(agentPort.Int64), Scheme: agentScheme.String,
			Status: AgentStatus(agentStatus.String), Version: agentVersion.String, LastOKAt: nullTime(agentOKAt),
		}
	}
	return d, nil
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

// mapConstraint turns the driver's unique-constraint text into the domain errors.
func mapConstraint(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "UNIQUE constraint failed: devices.name"):
		return ErrNameTaken
	case strings.Contains(msg, "UNIQUE constraint failed: agent_bindings.agent_id"):
		return ErrAgentTaken
	}
	return err
}

// Create implements Repository.
func (r *SQLiteRepository) Create(ctx context.Context, d Device) error {
	err := r.db.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO devices
			(id, name, description, hostname, ip_address, mac_address, wol_broadcast_address, wol_port,
			 device_type, os, monitor_port, monitor_interval_seconds, monitor_timeout_ms, enabled,
			 status, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			d.ID, d.Name, d.Description, d.Hostname, d.IPAddress, d.MACAddress, d.WOLBroadcastAddress,
			d.WOLPort, string(d.Type), string(d.OS), d.MonitorPort, int64(d.MonitorInterval/time.Second),
			d.MonitorTimeout.Milliseconds(), boolInt(d.Enabled), string(d.Status),
			storage.Millis(d.CreatedAt), storage.Millis(d.UpdatedAt))
		if err != nil {
			return err
		}
		if d.Agent != nil {
			return upsertBinding(ctx, tx, d.ID, *d.Agent, d.UpdatedAt)
		}
		return nil
	})
	if err = mapConstraint(err); err != nil {
		if errors.Is(err, ErrNameTaken) || errors.Is(err, ErrAgentTaken) {
			return err
		}
		return fmt.Errorf("create device: %w", err)
	}
	return nil
}

func upsertBinding(ctx context.Context, tx *sql.Tx, deviceID string, a AgentBinding, now time.Time) error {
	// Re-binding to another agent or address forgets what was known about the old one; an update
	// that changes nothing (the UI sends the whole binding on every PATCH) keeps it.
	_, err := tx.ExecContext(ctx, `INSERT INTO agent_bindings (device_id, agent_id, port, scheme, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (device_id) DO UPDATE SET
			status = CASE WHEN agent_id = excluded.agent_id AND port = excluded.port AND scheme = excluded.scheme
				THEN status ELSE 'unknown' END,
			version = CASE WHEN agent_id = excluded.agent_id AND port = excluded.port AND scheme = excluded.scheme
				THEN version ELSE '' END,
			last_ok_at = CASE WHEN agent_id = excluded.agent_id AND port = excluded.port AND scheme = excluded.scheme
				THEN last_ok_at ELSE NULL END,
			agent_id = excluded.agent_id, port = excluded.port, scheme = excluded.scheme,
			updated_at = excluded.updated_at`,
		deviceID, a.AgentID, a.Port, a.Scheme, storage.Millis(now), storage.Millis(now))
	return err
}

// Get implements Repository.
func (r *SQLiteRepository) Get(ctx context.Context, id string) (Device, error) {
	d, err := scanDevice(r.db.QueryRowContext(ctx, deviceSelect+` WHERE d.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, ErrNotFound
	}
	if err != nil {
		return Device{}, fmt.Errorf("get device: %w", err)
	}
	return d, nil
}

// Update implements Repository.
func (r *SQLiteRepository) Update(ctx context.Context, d Device, syncAgent bool) error {
	err := r.db.WithTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE devices SET name = ?, description = ?, hostname = ?,
			ip_address = ?, mac_address = ?, wol_broadcast_address = ?, wol_port = ?, device_type = ?, os = ?,
			monitor_port = ?, monitor_interval_seconds = ?, monitor_timeout_ms = ?, enabled = ?, updated_at = ?
			WHERE id = ?`,
			d.Name, d.Description, d.Hostname, d.IPAddress, d.MACAddress, d.WOLBroadcastAddress, d.WOLPort,
			string(d.Type), string(d.OS), d.MonitorPort, int64(d.MonitorInterval/time.Second),
			d.MonitorTimeout.Milliseconds(), boolInt(d.Enabled), storage.Millis(d.UpdatedAt), d.ID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		if syncAgent && d.Agent != nil {
			return upsertBinding(ctx, tx, d.ID, *d.Agent, d.UpdatedAt)
		}
		return nil
	})
	if err = mapConstraint(err); err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrNameTaken) || errors.Is(err, ErrAgentTaken) {
			return err
		}
		return fmt.Errorf("update device: %w", err)
	}
	return nil
}

// Delete implements Repository.
func (r *SQLiteRepository) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM devices WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete device: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RemoveAgent implements Repository.
func (r *SQLiteRepository) RemoveAgent(ctx context.Context, id string) error {
	return r.db.WithTx(ctx, func(tx *sql.Tx) error {
		var one int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM devices WHERE id = ?`, id).Scan(&one); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return fmt.Errorf("remove agent: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM agent_bindings WHERE device_id = ?`, id); err != nil {
			return fmt.Errorf("remove agent: %w", err)
		}
		return nil
	})
}

// List implements Repository. Keyset order: name (ASCII case-insensitive, the column collation),
// then id. Names are unique under that collation, so the id only makes the order total.
func (r *SQLiteRepository) List(ctx context.Context, after *ListAfter, limit int) ([]Device, error) {
	q := deviceSelect
	var args []any
	if after != nil {
		q += ` WHERE d.name > ? COLLATE NOCASE OR (d.name = ? COLLATE NOCASE AND d.id > ?)`
		args = append(args, after.Name, after.Name, after.ID)
	}
	q += ` ORDER BY d.name COLLATE NOCASE, d.id LIMIT ?`
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, fmt.Errorf("list devices: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// History implements Repository.
func (r *SQLiteRepository) History(ctx context.Context, deviceID string, after *HistoryAfter, limit int) ([]StatusChange, error) {
	q := `SELECT id, status, previous_status, reason, changed_at FROM device_status_history WHERE device_id = ?`
	args := []any{deviceID}
	if after != nil {
		ms := storage.Millis(after.ChangedAt)
		q += ` AND (changed_at < ? OR (changed_at = ? AND id < ?))`
		args = append(args, ms, ms, after.ID)
	}
	q += ` ORDER BY changed_at DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("device history: %w", err)
	}
	defer rows.Close()
	out := []StatusChange{}
	for rows.Next() {
		var (
			c       StatusChange
			status  string
			prev    sql.NullString
			changed int64
		)
		if err := rows.Scan(&c.ID, &status, &prev, &c.Reason, &changed); err != nil {
			return nil, fmt.Errorf("device history: %w", err)
		}
		c.Status, c.PreviousStatus, c.ChangedAt = Status(status), Status(prev.String), storage.FromMillis(changed)
		out = append(out, c)
	}
	return out, rows.Err()
}

// activeCommand is the SQL condition for a non-terminal command (see commands_one_active).
const activeCommand = `c.status IN ('pending', 'sent', 'acknowledged')`

// MonitorTargets implements Repository.
func (r *SQLiteRepository) MonitorTargets(ctx context.Context) ([]MonitorTarget, error) {
	// Two simple queries beat a clever one: enabled devices, then the devices with an active
	// command, merged in Go.
	active := map[string]bool{}
	ar, err := r.db.QueryContext(ctx, `SELECT DISTINCT c.device_id FROM commands c WHERE `+activeCommand)
	if err != nil {
		return nil, fmt.Errorf("monitor targets: %w", err)
	}
	for ar.Next() {
		var id string
		if err := ar.Scan(&id); err != nil {
			_ = ar.Close()
			return nil, fmt.Errorf("monitor targets: %w", err)
		}
		active[id] = true
	}
	if err := ar.Err(); err != nil {
		_ = ar.Close()
		return nil, fmt.Errorf("monitor targets: %w", err)
	}
	if err := ar.Close(); err != nil {
		return nil, err
	}
	dr, err := r.db.QueryContext(ctx, deviceSelect+` WHERE d.enabled = 1 OR EXISTS
		(SELECT 1 FROM commands c WHERE c.device_id = d.id AND `+activeCommand+`) ORDER BY d.id`)
	if err != nil {
		return nil, fmt.Errorf("monitor targets: %w", err)
	}
	defer dr.Close()
	var out []MonitorTarget
	for dr.Next() {
		d, err := scanDevice(dr)
		if err != nil {
			return nil, fmt.Errorf("monitor targets: %w", err)
		}
		out = append(out, MonitorTarget{Device: d, Urgent: active[d.ID]})
	}
	return out, dr.Err()
}

// RecordObservation implements Repository.
func (r *SQLiteRepository) RecordObservation(ctx context.Context, o Observation) error {
	at := storage.Millis(o.At)
	return r.db.WithTx(ctx, func(tx *sql.Tx) error {
		var res sql.Result
		var err error
		if o.Reachable {
			res, err = tx.ExecContext(ctx, `UPDATE devices SET status = ?, last_checked_at = ?, last_seen_at = ?,
				last_latency_ms = ? WHERE id = ?`, string(o.Status), at, at, o.Latency.Milliseconds(), o.DeviceID)
		} else {
			res, err = tx.ExecContext(ctx, `UPDATE devices SET status = ?, last_checked_at = ? WHERE id = ?`,
				string(o.Status), at, o.DeviceID)
		}
		if err != nil {
			return fmt.Errorf("record observation: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		if o.Changed {
			var prev any
			if o.Previous != "" {
				prev = string(o.Previous)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO device_status_history
				(device_id, status, previous_status, reason, changed_at) VALUES (?, ?, ?, ?, ?)`,
				o.DeviceID, string(o.Status), prev, o.Reason, at); err != nil {
				return fmt.Errorf("record status change: %w", err)
			}
		}
		return nil
	})
}

// PruneHistory implements Repository.
func (r *SQLiteRepository) PruneHistory(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM device_status_history WHERE id IN
		(SELECT id FROM device_status_history WHERE changed_at < ? LIMIT ?)`, storage.Millis(cutoff), limit)
	if err != nil {
		return 0, fmt.Errorf("prune status history: %w", err)
	}
	return res.RowsAffected()
}

// RecordAgentContact implements Repository. It is a no-op when the binding changed in the
// meantime (another agent_id), so a late result cannot be attributed to a new agent.
func (r *SQLiteRepository) RecordAgentContact(ctx context.Context, deviceID, agentID string, c AgentContact) error {
	status := AgentUnreachable
	if c.OK {
		status = AgentReady
	}
	at := storage.Millis(c.At)
	var err error
	if c.OK {
		_, err = r.db.ExecContext(ctx, `UPDATE agent_bindings SET status = ?, last_ok_at = ?,
			version = CASE WHEN ? = '' THEN version ELSE ? END WHERE device_id = ? AND agent_id = ?`,
			string(status), at, c.Version, c.Version, deviceID, agentID)
	} else {
		_, err = r.db.ExecContext(ctx, `UPDATE agent_bindings SET status = ? WHERE device_id = ? AND agent_id = ?`,
			string(status), deviceID, agentID)
	}
	if err != nil {
		return fmt.Errorf("record agent contact: %w", err)
	}
	return nil
}

// BoundDevices implements Repository.
func (r *SQLiteRepository) BoundDevices(ctx context.Context) ([]Device, error) {
	rows, err := r.db.QueryContext(ctx, deviceSelect+` WHERE a.device_id IS NOT NULL ORDER BY d.id`)
	if err != nil {
		return nil, fmt.Errorf("bound devices: %w", err)
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, fmt.Errorf("bound devices: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
