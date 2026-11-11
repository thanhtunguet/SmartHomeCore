// Package mobile is the gomobile binding surface used by the Android host app:
//
//	gomobile bind -target=android -androidapi 24 -o build/homecore.aar ./mobile
//
// Only gomobile-compatible types (string, []byte, bool, int, error, interfaces of those) may
// appear in exported signatures. The Kotlin host owns lifecycle (foreground service, boot
// receiver); the Go side must tolerate Start/Stop at any time.
package mobile

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/thanhtunguet/homecore/internal/app"
	"github.com/thanhtunguet/homecore/internal/config"
	"github.com/thanhtunguet/homecore/internal/logging"
	"github.com/thanhtunguet/homecore/internal/version"
)

// LogSink receives log lines; implement it in Kotlin to forward to Logcat.
type LogSink interface {
	Log(line string)
}

var (
	mu      sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{}
	lastErr error
	sink    LogSink
)

// SetLogSink installs a log receiver. Call before Start.
func SetLogSink(s LogSink) {
	mu.Lock()
	defer mu.Unlock()
	sink = s
}

// Start runs the Controller in the background. dataDir must be the app-private files directory
// (Context.getFilesDir()). configYAML may be empty for defaults. masterKey is 32 bytes from the
// Android Keystore, or nil to use a key file in dataDir.
func Start(dataDir, configYAML string, masterKey []byte) error {
	mu.Lock()
	defer mu.Unlock()
	if cancel != nil {
		return errors.New("homecore already running")
	}
	cfg, err := config.LoadBytes([]byte(configYAML), func(k string) (string, bool) {
		if k == "HOMECORE_DATA_DIR" {
			return dataDir, true
		}
		return "", false
	})
	if err != nil {
		return err
	}
	if cfg.Mode != config.ModeController {
		return errors.New("only controller mode is supported on Android")
	}

	var w io.Writer = io.Discard
	if sink != nil {
		w = sinkWriter{sink}
	}
	log := logging.New(cfg.Log, w)

	ctx, c := context.WithCancel(context.Background())
	cancel, done, lastErr = c, make(chan struct{}), nil
	go func(done chan struct{}) {
		defer close(done)
		err := app.Run(ctx, cfg, log, app.Options{HostMasterKey: masterKey})
		mu.Lock()
		lastErr = err
		mu.Unlock()
	}(done)
	return nil
}

// Stop cancels the Controller and waits up to timeoutMillis for a graceful shutdown.
func Stop(timeoutMillis int) error {
	mu.Lock()
	c, d := cancel, done
	cancel, done = nil, nil
	mu.Unlock()
	if c == nil {
		return nil
	}
	c()
	select {
	case <-d:
		mu.Lock()
		defer mu.Unlock()
		return lastErr
	case <-time.After(time.Duration(timeoutMillis) * time.Millisecond):
		return errors.New("timed out waiting for homecore to stop")
	}
}

// IsRunning reports whether Start has been called without a matching Stop.
func IsRunning() bool {
	mu.Lock()
	defer mu.Unlock()
	if done == nil {
		return false
	}
	select {
	case <-done:
		return false
	default:
		return true
	}
}

// LastError returns the error the last run ended with ("" if none).
func LastError() string {
	mu.Lock()
	defer mu.Unlock()
	if lastErr == nil {
		return ""
	}
	return lastErr.Error()
}

// Version returns the library version.
func Version() string { return version.String() }

type sinkWriter struct{ s LogSink }

func (w sinkWriter) Write(p []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		w.s.Log(line)
	}
	return len(p), nil
}
