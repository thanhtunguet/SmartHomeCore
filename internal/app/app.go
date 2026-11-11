// Package app is the composition root: it wires components for the selected mode and runs them
// until the context is cancelled. It is the only package that knows about every module.
package app

import (
	"context"
	"fmt"
	"log/slog"

	"golang.org/x/sync/errgroup"

	"github.com/thanhtunguet/homecore/internal/agent"
	"github.com/thanhtunguet/homecore/internal/api"
	"github.com/thanhtunguet/homecore/internal/config"
	"github.com/thanhtunguet/homecore/internal/secrets"
	"github.com/thanhtunguet/homecore/internal/storage"
	"github.com/thanhtunguet/homecore/internal/version"
)

// Options carries host-provided capabilities (used by the mobile bindings).
type Options struct {
	// HostMasterKey, if set, is used instead of <data_dir>/master.key (e.g. Android Keystore).
	HostMasterKey []byte
}

// Run starts every role enabled by cfg.Mode and blocks until ctx is cancelled or a component
// fails. In "both" mode Controller and Agent run side by side on separate listeners and share
// nothing but the logger; a failure in either stops the process.
func Run(ctx context.Context, cfg config.Config, log *slog.Logger, opts Options) error {
	log.Info("homecore starting", "version", version.String(), "mode", cfg.Mode, "data_dir", cfg.DataDir)

	g, gctx := errgroup.WithContext(ctx)
	if cfg.Mode.RunsController() {
		g.Go(func() error { return runController(gctx, cfg, log.With("role", "controller"), opts) })
	}
	if cfg.Mode.RunsAgent() {
		g.Go(func() error { return runAgent(gctx, cfg, log) })
	}
	err := g.Wait()
	log.Info("homecore stopped", "err", err)
	return err
}

func runController(ctx context.Context, cfg config.Config, log *slog.Logger, opts Options) error {
	db, err := storage.Open(ctx, cfg.DatabasePath())
	if err != nil {
		return err
	}
	defer db.Close()

	applied, err := db.Migrate(ctx)
	if err != nil {
		return err
	}
	log.Info("database ready", "path", cfg.DatabasePath(), "migrations_applied", applied,
		"schema_version", storage.LatestVersion())

	masterKey, err := secrets.LoadOrCreateMasterKey(cfg.DataDir, opts.HostMasterKey)
	if err != nil {
		return fmt.Errorf("master key: %w", err)
	}
	sealer, err := secrets.NewSealer(masterKey)
	if err != nil {
		return err
	}
	_ = sealer // TODO(phase-1/3/4): inject into auth (JWT key), controller keys and ddns tokens.

	srv, err := api.New(api.Deps{
		Config:          cfg.Controller,
		ShutdownTimeout: cfg.ShutdownTimeout,
		Log:             log,
		DB:              db,
		// Authn: auth.NewJWT(...)  TODO(phase-1): until then every protected endpoint returns 401.
	})
	if err != nil {
		return err
	}

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return srv.Run(gctx) })
	// TODO(phase-2): g.Go(monitor.Run), g.Go(commandDispatcher.Run), retention job.
	// TODO(phase-4): g.Go(ddnsSyncer.Run).
	return g.Wait()
}

func runAgent(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	srv, err := agent.New(cfg.Agent, cfg.ShutdownTimeout, log, nil)
	if err != nil {
		return fmt.Errorf("agent: %w", err)
	}
	return srv.Run(ctx)
}
