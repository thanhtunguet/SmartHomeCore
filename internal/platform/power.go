// Package platform contains OS-specific adapters behind small interfaces. Domain code receives
// these as dependencies and never switches on runtime.GOOS itself.
//
// Build-tag note: GOOS=android also satisfies the "linux" constraint, so Linux-only files use
// `//go:build linux && !android`.
package platform

import (
	"context"
	"log/slog"
)

// PowerController performs machine power actions for the Agent. Implementations must not accept
// arbitrary commands or arguments.
type PowerController interface {
	PowerOff(ctx context.Context) error
	Reboot(ctx context.Context) error
}

// NewPowerController returns the native implementation, or a dry-run implementation when dryRun
// is set or the platform has no native support.
func NewPowerController(dryRun bool, log *slog.Logger) PowerController {
	if dryRun {
		return DryRunPower{Log: log}
	}
	return nativePower(log)
}

// DryRunPower logs instead of acting. Used on macOS during development and in tests.
type DryRunPower struct{ Log *slog.Logger }

func (d DryRunPower) PowerOff(context.Context) error {
	d.Log.Warn("DRY RUN: would power off this machine")
	return nil
}

func (d DryRunPower) Reboot(context.Context) error {
	d.Log.Warn("DRY RUN: would reboot this machine")
	return nil
}
