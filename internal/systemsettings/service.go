// Package systemsettings manages persisted, non-secret Controller settings.
package systemsettings

import (
	"context"
	"time"
)

// Repository persists settings used by the Controller.
type Repository interface {
	OpenAPIEnabled(context.Context) (bool, error)
	SetOpenAPIEnabled(context.Context, bool, time.Time) error
}

// Service applies system setting operations.
type Service struct{ repo Repository }

func New(repo Repository) *Service { return &Service{repo: repo} }

func (s *Service) OpenAPIEnabled(ctx context.Context) (bool, error) {
	return s.repo.OpenAPIEnabled(ctx)
}

func (s *Service) SetOpenAPIEnabled(ctx context.Context, enabled bool, now time.Time) error {
	return s.repo.SetOpenAPIEnabled(ctx, enabled, now)
}
