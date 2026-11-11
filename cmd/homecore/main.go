// Command homecore runs the HomeCore Controller, Agent, or both.
//
//	homecore --mode controller --config /etc/homecore/homecore.yaml
//	homecore --mode agent
//	homecore --mode both
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/thanhtunguet/homecore/internal/app"
	"github.com/thanhtunguet/homecore/internal/config"
	"github.com/thanhtunguet/homecore/internal/logging"
	"github.com/thanhtunguet/homecore/internal/version"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "homecore:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		configPath  = flag.String("config", os.Getenv("HOMECORE_CONFIG"), "path to YAML config file (env HOMECORE_CONFIG)")
		mode        = flag.String("mode", "", "runtime mode: controller | agent | both (overrides config)")
		dataDir     = flag.String("data-dir", "", "data directory (overrides config)")
		showVersion = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println(version.String())
		return nil
	}

	var data []byte
	if *configPath != "" {
		b, err := os.ReadFile(*configPath)
		if err != nil {
			return fmt.Errorf("read config: %w", err)
		}
		data = b
	}
	// Precedence: flags > environment > config file > defaults.
	flags := map[string]string{"HOMECORE_MODE": *mode, "HOMECORE_DATA_DIR": *dataDir}
	lookup := func(key string) (string, bool) {
		if v := flags[key]; v != "" {
			return v, true
		}
		return os.LookupEnv(key)
	}
	cfg, err := config.LoadBytes(data, lookup)
	if err != nil {
		return err
	}

	log := logging.New(cfg.Log, os.Stderr)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return app.Run(ctx, cfg, log, app.Options{})
}
