package devices

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Errors returned by the Service and Repository. Handlers map them to HTTP statuses.
var (
	ErrNotFound   = errors.New("device not found")
	ErrNameTaken  = errors.New("a device with this name already exists")
	ErrAgentTaken = errors.New("this agent_id is already bound to another device")
)

// ValidationError lists every problem found in a request; handlers answer 400 with its text.
type ValidationError struct{ Problems []string }

func (e *ValidationError) Error() string { return strings.Join(e.Problems, "; ") }

// ListAfter is the keyset position after which a device page starts: the sort key of the last
// item of the previous page (name compared case-insensitively, then id).
type ListAfter struct {
	Name string
	ID   string
}

// HistoryAfter is the keyset position of a status history page (newest first).
type HistoryAfter struct {
	ChangedAt time.Time
	ID        int64
}

// Repository is the persistence the Service and the monitor need. The SQLite implementation is
// SQLiteRepository; tests use it against a real temp database.
type Repository interface {
	// Create inserts the device and its binding in one transaction. ErrNameTaken, ErrAgentTaken.
	Create(ctx context.Context, d Device) error
	// Get returns the device with its binding. ErrNotFound.
	Get(ctx context.Context, id string) (Device, error)
	// Update writes the configuration columns of d (never the observed state). When syncAgent is
	// true the binding is upserted from d.Agent. ErrNotFound, ErrNameTaken, ErrAgentTaken.
	Update(ctx context.Context, d Device, syncAgent bool) error
	// Delete removes the device; its binding, commands and status history go with it (ON DELETE
	// CASCADE). ErrNotFound.
	Delete(ctx context.Context, id string) error
	// RemoveAgent deletes the binding of an existing device; a device without one is not an
	// error. ErrNotFound only when the device does not exist.
	RemoveAgent(ctx context.Context, id string) error
	// List returns up to limit devices ordered by name (case-insensitive), then id, after the
	// given position (nil = from the start).
	List(ctx context.Context, after *ListAfter, limit int) ([]Device, error)
	// History returns up to limit status changes, newest first, after the position.
	History(ctx context.Context, deviceID string, after *HistoryAfter, limit int) ([]StatusChange, error)

	// MonitorTargets returns the devices the monitor must consider: every enabled device and any
	// device with an active command.
	MonitorTargets(ctx context.Context) ([]MonitorTarget, error)
	// RecordObservation stores the outcome of a probe: status, last_checked_at and, on success,
	// last_seen_at and last_latency_ms; with o.Changed it appends a history row in the same
	// transaction. ErrNotFound if the device is gone.
	RecordObservation(ctx context.Context, o Observation) error
	// PruneHistory deletes at most limit status history rows older than cutoff.
	PruneHistory(ctx context.Context, cutoff time.Time, limit int) (int64, error)
}
