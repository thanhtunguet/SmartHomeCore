package ddns

import (
	"context"
	"time"
)

// Config is one managed Cloudflare record as the API shows it. The token is never part of it.
type Config struct {
	ID         string
	Name       string
	ZoneID     string
	RecordID   string // Cloudflare record id (column cf_record_id); '' only for rows that predate it
	RecordName string // lower case, no trailing dot
	RecordType RecordType
	Proxied    bool
	TTL        int // 1 = automatic; always 1 while Proxied
	Interval   time.Duration
	Enabled    bool
	HasToken   bool

	// Observed state, written by syncs.
	LastIP        string // what the Controller last pushed or confirmed
	DetectedIP    string // public address of the most recent successful discovery
	DNSValue      string // content of the Cloudflare record as last read
	LastCheckedAt *time.Time
	LastSyncedAt  *time.Time // last success (updated or unchanged)
	LastError     string
	LastErrorAt   *time.Time

	CreatedAt time.Time
	UpdatedAt time.Time // changes only when the configuration is edited, never by a sync
}

// Stored is a Config together with its sealed token, for the service only.
type Stored struct {
	Config      Config
	TokenSealed []byte
}

// Result is the outcome of one sync.
type Result string

const (
	ResultUpdated   Result = "updated"
	ResultUnchanged Result = "unchanged"
	ResultFailed    Result = "failed"
)

// Outcome is what one sync attempt learned, persisted by Repository.SaveOutcome.
type Outcome struct {
	At       time.Time
	Result   Result
	IP       string // detected public address; '' when discovery failed
	DNSValue string // record content as read or written; '' when it could not be read
	Error    string // failed only; never contains the token
}

// Repository is the storage the DDNS service needs.
type Repository interface {
	// Create inserts a row. ErrDuplicate when another configuration manages the same record.
	Create(ctx context.Context, s Stored) error
	// Get returns one configuration with its sealed token. ErrNotFound.
	Get(ctx context.Context, id string) (Stored, error)
	// List returns all configurations ordered by name (case-insensitive), then creation time.
	List(ctx context.Context) ([]Config, error)
	// Update writes the editable fields and the token. With resetState (the target changed) the
	// observed state of the old record is cleared too. ErrNotFound, ErrDuplicate.
	Update(ctx context.Context, s Stored, resetState bool) error
	// Delete removes the configuration; its history goes with it (ON DELETE CASCADE). ErrNotFound.
	Delete(ctx context.Context, id string) error
	// SaveOutcome stores the observed state and appends a ddns_sync_history row in one
	// transaction, but only if the configuration still has the given UpdatedAt: a sync that raced
	// with an edit describes a record the user may no longer have chosen, so it is dropped
	// (applied false). A deleted configuration is also not applied.
	SaveOutcome(ctx context.Context, id string, version time.Time, o Outcome) (applied bool, err error)
	// History returns the newest sync attempts of a configuration (tests, diagnostics).
	History(ctx context.Context, id string, limit int) ([]Outcome, error)
}
