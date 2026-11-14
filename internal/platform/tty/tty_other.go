//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly && !windows

package tty

import "os"

// IsTerminal always reports false here.
func IsTerminal(*os.File) bool { return false }

func disableEcho(*os.File) (func(), error) { return nil, ErrUnsupported }
