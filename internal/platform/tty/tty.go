// Package tty reads a secret from a terminal without echoing it, using only golang.org/x/sys
// (golang.org/x/term is not an approved dependency). It is used by `homecore user add|passwd`.
//
// Supported: Linux/Android, macOS and the BSDs (termios), Windows (console mode). On other
// platforms IsTerminal reports false and ReadPassword fails, which makes callers fall back to
// reading standard input or the HOMECORE_PASSWORD environment variable.
package tty

import (
	"errors"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

// ErrUnsupported is returned by ReadPassword where no-echo input is not implemented.
var ErrUnsupported = errors.New("reading a password without echo is not supported on this platform")

// ReadPassword reads one line from the terminal f with echo disabled and returns it without the
// trailing line break. The terminal state is restored before it returns; on SIGINT or SIGTERM it
// is restored and the process exits with status 130.
func ReadPassword(f *os.File) (string, error) {
	restore, err := disableEcho(f)
	if err != nil {
		return "", err
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		select {
		case <-sig:
			// The read below cannot be interrupted portably, so restore the terminal and leave.
			restore()
			os.Stderr.WriteString("\ninterrupted\n")
			os.Exit(130)
		case <-done:
		}
	}()
	defer func() {
		close(done)
		signal.Stop(sig)
		restore()
	}()

	var buf []byte
	one := make([]byte, 1)
	for {
		n, err := f.Read(one)
		if n == 1 {
			if one[0] == '\n' {
				break
			}
			buf = append(buf, one[0])
		}
		if err != nil {
			if len(buf) > 0 {
				break
			}
			return "", err
		}
	}
	os.Stderr.WriteString("\n")
	return strings.TrimSuffix(string(buf), "\r"), nil
}
