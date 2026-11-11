//go:build !linux || android

package platform

import "log/slog"

// nativePower: no native implementation yet on this platform (macOS dev, Windows in a later
// phase, Android is controller-only). Fall back to dry-run so development is safe.
func nativePower(log *slog.Logger) PowerController {
	log.Warn("no native power controller for this platform; using dry-run")
	return DryRunPower{Log: log}
}
