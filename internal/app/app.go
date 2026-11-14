// Package app is the composition root: it wires components for the selected mode and runs them
// until the context is cancelled. It is the only package that knows about every module.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/thanhtunguet/homecore/internal/agent"
	"github.com/thanhtunguet/homecore/internal/api"
	"github.com/thanhtunguet/homecore/internal/audit"
	"github.com/thanhtunguet/homecore/internal/auth"
	"github.com/thanhtunguet/homecore/internal/commands"
	"github.com/thanhtunguet/homecore/internal/config"
	"github.com/thanhtunguet/homecore/internal/ddns"
	"github.com/thanhtunguet/homecore/internal/devices"
	"github.com/thanhtunguet/homecore/internal/monitoring"
	"github.com/thanhtunguet/homecore/internal/retention"
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
	// TODO(phase-3/4): inject the sealer into controller key management and DDNS tokens too.

	authSvc, err := auth.NewService(ctx, auth.Config{
		DB:         db,
		Sealer:     sealer,
		Audit:      audit.NewWriter(db, nil),
		Log:        log,
		AccessTTL:  cfg.Controller.Auth.AccessTokenTTL,
		RefreshTTL: cfg.Controller.Auth.RefreshTokenTTL,
	})
	if err != nil {
		return err
	}
	if n, err := authSvc.Accounts().Store().CountUsers(ctx); err != nil {
		return fmt.Errorf("count users: %w", err)
	} else if n == 0 {
		log.Warn("no user accounts exist, nobody can sign in: run `homecore user add <username>` (add --config/--data-dir as for the server)")
	}

	devRepo := devices.NewSQLiteRepository(db)
	devSvc := devices.NewService(devRepo, nil)
	mon := monitoring.New(monitoring.Config{
		Store:            devRepo,
		Log:              log.With("component", "monitor"),
		Tick:             cfg.Controller.Monitor.Tick,
		Concurrency:      cfg.Controller.Monitor.Concurrency,
		OfflineThreshold: cfg.Controller.Monitor.OfflineThreshold,
	})
	cmdRepo := commands.NewSQLiteRepository(db)
	engine := commands.NewEngine(commands.Config{
		Repo:             cmdRepo,
		Devices:          devSvc,
		Prober:           monitorProber{mon},
		Log:              log.With("component", "commands"),
		WakeDeadline:     cfg.Controller.Commands.WakeDeadline,
		ShutdownDeadline: cfg.Controller.Commands.ShutdownDeadline,
	})
	engine.Register(commands.TypeWake, commands.WakeDispatcher{})
	// TODO(phase-3): engine.Register(commands.TypeShutdown, <signed agent dispatcher>).
	mon.SetObserver(engine) // the monitor completes wake/shutdown commands through the engine
	prune := retention.New(retention.Config{
		Days: cfg.Controller.RetentionDays,
		Log:  log.With("component", "retention"),
		Targets: []retention.Target{
			{Name: "device_status_history", Prune: devRepo.PruneHistory},
			{Name: "commands", Prune: cmdRepo.PruneTerminal},
			{Name: "ddns_sync_history", Prune: func(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
				return ddns.PruneSyncHistory(ctx, db, cutoff, limit)
			}},
		},
	})

	srv, err := api.New(api.Deps{
		Config:          cfg.Controller,
		Mode:            cfg.Mode,
		ShutdownTimeout: cfg.ShutdownTimeout,
		Log:             log,
		DB:              db,
		Auth:            authSvc,
		Devices:         devSvc,
		Commands:        engine,
		Monitor:         mon,
		Audit:           audit.NewWriter(db, nil),
	})
	if err != nil {
		return err
	}

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return srv.Run(gctx) })
	g.Go(func() error { return authSvc.Run(gctx) }) // refresh-token housekeeping
	g.Go(func() error { return mon.Run(gctx) })     // TCP probes, status persistence
	g.Go(func() error { return engine.Run(gctx) })  // command recovery and deadlines
	g.Go(func() error { return prune.Run(gctx) })   // retention_days pruning
	// TODO(phase-4): g.Go(ddnsSyncer.Run).
	return g.Wait()
}

// monitorProber lets the commands engine ask the monitor for a fresh probe.
type monitorProber struct{ m *monitoring.Monitor }

func (p monitorProber) Probe(ctx context.Context, deviceID string) (bool, error) {
	out, err := p.m.ProbeNow(ctx, deviceID)
	return out.Result.Reachable, err
}

func runAgent(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	srv, err := agent.New(cfg.Agent, cfg.ShutdownTimeout, log, nil)
	if err != nil {
		return fmt.Errorf("agent: %w", err)
	}
	return srv.Run(ctx)
}
