// Package audit writes the append-only audit_log table: security-relevant actions with the
// acting user, the target and the client address. It is shared by authentication (login,
// logout, token reuse, account changes) and later by shutdown (Phase 3) and key rotation.
//
// Entries must never contain secrets: no passwords, tokens or keys in Details.
package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/thanhtunguet/homecore/internal/storage"
)

// Result values for Entry.Result. Callers may use an error code instead for other failures.
const (
	ResultOK     = "ok"
	ResultFailed = "failed"
)

// Entry is one audit record.
type Entry struct {
	ActorUserID string // empty when the actor is unknown or not a user
	Actor       string // username snapshot, or "cli" for the local command line
	Action      string // e.g. "auth.login", "device.shutdown"
	TargetType  string // e.g. "user", "device"
	TargetID    string
	Result      string         // ResultOK, ResultFailed or an error code
	RemoteAddr  string         // client IP; empty for the CLI
	Details     map[string]any // JSON-encoded; never secrets
}

// Writer inserts audit entries.
type Writer struct {
	db  *storage.DB
	now func() time.Time
}

// NewWriter returns a Writer. now may be nil (time.Now).
func NewWriter(db *storage.DB, now func() time.Time) *Writer {
	if now == nil {
		now = time.Now
	}
	return &Writer{db: db, now: now}
}

// Write appends e. The error is returned so that callers whose action must not happen without a
// record (shutdown) can refuse; best-effort callers (login) log it and carry on.
func (w *Writer) Write(ctx context.Context, e Entry) error {
	return w.write(ctx, w.db, e)
}

// WriteTx appends e inside an open transaction, so the record commits atomically with the action.
func (w *Writer) WriteTx(ctx context.Context, tx *sql.Tx, e Entry) error {
	return w.write(ctx, tx, e)
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (w *Writer) write(ctx context.Context, q execer, e Entry) error {
	if e.Action == "" {
		return fmt.Errorf("audit: action required")
	}
	details := []byte("{}")
	if len(e.Details) > 0 {
		b, err := json.Marshal(e.Details)
		if err != nil {
			return fmt.Errorf("audit: encode details: %w", err)
		}
		details = b
	}
	var actorID any
	if e.ActorUserID != "" {
		actorID = e.ActorUserID
	}
	_, err := q.ExecContext(ctx, `INSERT INTO audit_log
		(at, actor_user_id, actor, action, target_type, target_id, result, remote_addr, details)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		storage.Millis(w.now()), actorID, e.Actor, e.Action, e.TargetType, e.TargetID,
		e.Result, e.RemoteAddr, string(details))
	if err != nil {
		return fmt.Errorf("audit: write: %w", err)
	}
	return nil
}
