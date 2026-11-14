package commands

import (
	"context"
	"errors"
	"time"
)

// Errors of the repository and the engine. Handlers map them to HTTP statuses.
var (
	ErrNotFound     = errors.New("command not found")
	ErrActiveExists = errors.New("an active command of this type already exists for the device")
	ErrNoDispatcher = errors.New("no dispatcher registered for this command type")
	// ErrPrecondition means the request can never work as the device is configured (wake without
	// a MAC address, shutdown without an agent binding): HTTP 409.
	ErrPrecondition = errors.New("command precondition not met")
	// ErrInvalid means a malformed argument (unknown status filter, bad device id): HTTP 400.
	ErrInvalid = errors.New("invalid argument")
)

// PreconditionError carries the message shown to the user; it matches ErrPrecondition.
type PreconditionError struct{ Message string }

func (e *PreconditionError) Error() string { return e.Message }

// Is lets errors.Is(err, ErrPrecondition) match.
func (e *PreconditionError) Is(target error) bool { return target == ErrPrecondition }

// Change is a compare-and-set status update.
type Change struct {
	To        Status
	ErrorCode string
	Error     string
	Attempts  int       // new value of the attempts counter (0 = leave unchanged)
	At        time.Time // updated_at, and sent_at / acknowledged_at / completed_at as To implies
}

// ListFilter narrows a command listing; zero values mean "any".
type ListFilter struct {
	DeviceID string
	Status   Status
}

// ListAfter is the keyset position of a page (newest first: created_at DESC, id DESC).
type ListAfter struct {
	CreatedAt time.Time
	ID        string
}

// ActiveRef identifies an active command, for deriving power_transition.
type ActiveRef struct {
	DeviceID  string
	Type      Type
	CreatedAt time.Time
}

// Repository is the persistence of commands. The SQLite implementation relies on the
// commands_one_active unique index: Insert of a second active command of the same (device, type)
// fails with ErrActiveExists, whatever the callers do concurrently.
type Repository interface {
	Insert(ctx context.Context, c Command) error
	Get(ctx context.Context, id string) (Command, error)
	// Active returns the in-flight command of the type for the device; ErrNotFound if none.
	Active(ctx context.Context, deviceID string, t Type) (Command, error)
	// Update moves a command from `from` to ch.To only if it is still in `from`; the bool tells
	// whether this call made the change. The caller validates the transition (Transition).
	Update(ctx context.Context, id string, from Status, ch Change) (bool, error)
	List(ctx context.Context, f ListFilter, after *ListAfter, limit int) ([]Command, error)
	// ActiveForDevices lists the in-flight commands of the given devices.
	ActiveForDevices(ctx context.Context, deviceIDs []string) ([]ActiveRef, error)
	// Pending returns commands still in `pending` (dispatch never finished), oldest first.
	Pending(ctx context.Context) ([]Command, error)
	// Expire applies the deadline: sent/acknowledged commands whose deadline_at <= now become
	// timed_out, pending ones failed (error_code "interrupted"). It returns how many changed.
	Expire(ctx context.Context, now time.Time) (int64, error)
	// PruneTerminal deletes at most limit terminal commands last updated before cutoff.
	PruneTerminal(ctx context.Context, cutoff time.Time, limit int) (int64, error)
}
