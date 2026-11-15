package alerts

import (
	"context"
	"time"
)

// Delivery is the outcome of one delivery attempt series to a channel, stored on the channel.
type Delivery struct {
	At    time.Time
	Error string // '' = delivered
}

// Repository is the storage the Service needs (implemented by SQLiteRepository).
type Repository interface {
	// Create inserts a channel. ErrNameTaken when the name is in use.
	Create(ctx context.Context, s Stored) error
	// Get returns one channel with its sealed secret. ErrNotFound.
	Get(ctx context.Context, id string) (Stored, error)
	// List returns every channel ordered by name (case-insensitive), then creation time.
	List(ctx context.Context) ([]Channel, error)
	// Update replaces the editable fields of a channel and its sealed secret. ErrNotFound,
	// ErrNameTaken.
	Update(ctx context.Context, s Stored) error
	// Delete removes a channel. ErrNotFound.
	Delete(ctx context.Context, id string) error
	// RecordDelivery stores the outcome of a delivery on the channel (not on updated_at). A channel
	// deleted meanwhile is ignored.
	RecordDelivery(ctx context.Context, id string, d Delivery) error
}
