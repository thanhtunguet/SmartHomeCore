// Package version exposes build metadata injected via -ldflags.
package version

// Set at build time:
//
//	go build -ldflags "-X github.com/thanhtunguet/homecore/internal/version.Version=v0.1.0"
var (
	Version = "dev"
	Commit  = "none"
)

// String returns a human-readable version string.
func String() string { return Version + " (" + Commit + ")" }
