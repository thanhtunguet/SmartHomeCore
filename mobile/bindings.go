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
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/thanhtunguet/homecore/internal/app"
	"github.com/thanhtunguet/homecore/internal/audit"
	"github.com/thanhtunguet/homecore/internal/auth"
	"github.com/thanhtunguet/homecore/internal/config"
	"github.com/thanhtunguet/homecore/internal/logging"
	"github.com/thanhtunguet/homecore/internal/storage"
	"github.com/thanhtunguet/homecore/internal/version"
)

// LogSink receives log lines; implement it in Kotlin to forward to Logcat.
type LogSink interface {
	Log(line string)
}

var (
	mu            sync.Mutex
	cancel        context.CancelFunc
	done          chan struct{}
	lastErr       error
	sink          LogSink
	activeDataDir string
	stopping      bool
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
	if cancel != nil || stopping {
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

	// Complete directory creation, SQLite open and append-only migrations before Start reports
	// success. Android account onboarding is available immediately after the service starts, and
	// a database/migration error is returned to the host instead of looking like a healthy run.
	db, err := storage.Open(context.Background(), cfg.DatabasePath())
	if err != nil {
		return fmt.Errorf("prepare controller database: %w", err)
	}
	if _, err := db.Migrate(context.Background()); err != nil {
		_ = db.Close()
		return fmt.Errorf("migrate controller database: %w", err)
	}
	if err := db.Close(); err != nil {
		return fmt.Errorf("close prepared controller database: %w", err)
	}

	var w io.Writer = io.Discard
	if sink != nil {
		w = sinkWriter{sink}
	}
	log := logging.New(cfg.Log, w)

	ctx, c := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	cancel, done, lastErr, activeDataDir = c, runDone, nil, cfg.DataDir
	go func() {
		defer close(runDone)
		err := app.Run(ctx, cfg, log, app.Options{HostMasterKey: masterKey})
		if errors.Is(err, context.Canceled) {
			err = nil // A caller-requested stop is not a failed HomeCore run.
		}
		mu.Lock()
		lastErr = err
		cancel, done, activeDataDir = nil, nil, ""
		stopping = false
		mu.Unlock()
	}()
	return nil
}

// Stop cancels the Controller and waits up to timeoutMillis for a graceful shutdown.
func Stop(timeoutMillis int) error {
	mu.Lock()
	c, d := cancel, done
	if c != nil {
		stopping = true
	}
	mu.Unlock()
	if c == nil {
		return nil
	}
	c()
	select {
	case <-d:
		mu.Lock()
		err := lastErr
		stopping = false
		mu.Unlock()
		return err
	case <-time.After(time.Duration(timeoutMillis) * time.Millisecond):
		// Keep the controller reserved as stopping until its run goroutine exits. Clearing
		// this flag here lets a retry call Start while the old listener is still alive.
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

// AccountCount returns the number of local HomeCore accounts. It runs the same append-only
// migrations as the Controller before reading, so onboarding is safe if called during startup.
func AccountCount() (int, error) {
	db, err := openAccountDB()
	if err != nil {
		return 0, err
	}
	defer db.Close()
	return auth.NewStore(db).CountUsers(context.Background())
}

// CreateInitialAccount creates the first local account only. The process-wide lock makes the
// count-and-create guard atomic with respect to all Android bridge callers in this process.
func CreateInitialAccount(username, password string) error {
	mu.Lock()
	defer mu.Unlock()
	if cancel == nil || stopping {
		return errors.New("homecore is not running")
	}
	db, err := openAccountDBLocked()
	if err != nil {
		return err
	}
	defer db.Close()
	ctx := context.Background()
	n, err := auth.NewStore(db).CountUsers(ctx)
	if err != nil {
		return err
	}
	if n != 0 {
		return errors.New("initial account already exists")
	}
	accounts := auth.NewAccounts(auth.NewStore(db), auth.NewHasher(auth.DefaultParams), audit.NewWriter(db, nil), slog.Default(), nil)
	_, err = accounts.Create(ctx, username, password, "android")
	return err
}

// VerifyAccountPassword checks a local account credential without exposing password hashes.
func VerifyAccountPassword(username, password string) error {
	mu.Lock()
	defer mu.Unlock()
	if cancel == nil || stopping {
		return errors.New("homecore is not running")
	}
	db, err := openAccountDBLocked()
	if err != nil {
		return err
	}
	defer db.Close()
	store := auth.NewStore(db)
	user, err := store.UserByUsername(context.Background(), username)
	if err != nil || user.Disabled {
		return errors.New("invalid credentials")
	}
	ok, _, err := auth.NewHasher(auth.DefaultParams).Verify(context.Background(), password, user.PasswordHash)
	if err != nil || !ok {
		return errors.New("invalid credentials")
	}
	return nil
}

// ChangeAccountPassword verifies the current password before changing it and revoking refresh tokens.
func ChangeAccountPassword(username, currentPassword, newPassword string) error {
	mu.Lock()
	defer mu.Unlock()
	if cancel == nil || stopping {
		return errors.New("homecore is not running")
	}
	db, err := openAccountDBLocked()
	if err != nil {
		return err
	}
	defer db.Close()
	store := auth.NewStore(db)
	user, err := store.UserByUsername(context.Background(), username)
	if err != nil || user.Disabled {
		return errors.New("invalid credentials")
	}
	ok, _, err := auth.NewHasher(auth.DefaultParams).Verify(context.Background(), currentPassword, user.PasswordHash)
	if err != nil || !ok {
		return errors.New("invalid credentials")
	}
	accounts := auth.NewAccounts(store, auth.NewHasher(auth.DefaultParams), audit.NewWriter(db, nil), slog.Default(), nil)
	_, err = accounts.SetPassword(context.Background(), username, newPassword, "android")
	return err
}

func openAccountDB() (*storage.DB, error) {
	mu.Lock()
	defer mu.Unlock()
	if cancel == nil || stopping {
		return nil, errors.New("homecore is not running")
	}
	return openAccountDBLocked()
}

func openAccountDBLocked() (*storage.DB, error) {
	if activeDataDir == "" {
		return nil, errors.New("homecore data directory is unavailable")
	}
	db, err := storage.Open(context.Background(), filepath.Join(activeDataDir, "homecore.db"))
	if err != nil {
		return nil, err
	}
	if _, err := db.Migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

type sinkWriter struct{ s LogSink }

func (w sinkWriter) Write(p []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		w.s.Log(line)
	}
	return len(p), nil
}
