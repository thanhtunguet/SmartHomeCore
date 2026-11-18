package portforward

import "context"

// Repository is the storage the Service needs (implemented by SQLiteRepository).
type Repository interface {
	// Create inserts a rule. ErrNameTaken when the name is in use.
	Create(ctx context.Context, r Rule) error
	// Get returns one rule. ErrNotFound.
	Get(ctx context.Context, id string) (Rule, error)
	// List returns every rule ordered by name (case-insensitive), then creation time.
	List(ctx context.Context) ([]Rule, error)
	// Update replaces the editable fields of a rule. ErrNotFound, ErrNameTaken.
	Update(ctx context.Context, r Rule) error
	// Delete removes a rule. ErrNotFound.
	Delete(ctx context.Context, id string) error
}
