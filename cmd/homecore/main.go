// Command homecore runs the HomeCore Controller, Agent, or both.
//
//	homecore --mode controller --config /etc/homecore/homecore.yaml
//	homecore --mode agent
//	homecore --mode both
//	homecore user add|passwd|list [--config FILE] [--data-dir DIR] [USERNAME]
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
	if len(os.Args) > 1 && os.Args[1] == "user" {
		return runUser(os.Args[2:], defaultCLIEnv())
	}

	var (
		configPath  = flag.String("config", os.Getenv("HOMECORE_CONFIG"), "path to YAML config file (env HOMECORE_CONFIG)")
		mode        = flag.String("mode", "", "runtime mode: controller | agent | both (overrides config)")
		dataDir     = flag.String("data-dir", "", "data directory (overrides config)")
		showVersion = flag.Bool("version", false, "print version and exit")
	)
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage:\n  homecore [flags]                    run the Controller and/or Agent\n  homecore user add|passwd|list ...   manage accounts (see `homecore user -h`)\n\nFlags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if *showVersion {
		fmt.Println(version.String())
		return nil
	}

	cfg, err := loadConfig(*configPath, *mode, *dataDir, os.LookupEnv)
	if err != nil {
		return err
	}

	log := logging.New(cfg.Log, os.Stderr)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return app.Run(ctx, cfg, log, app.Options{})
}

// loadConfig reads the YAML file (optional) and applies the environment and the flag overrides.
// Precedence: flags > environment > config file > defaults. The server and the `user` command
// share it, so both resolve the same data directory.
func loadConfig(configPath, mode, dataDir string, getenv func(string) (string, bool)) (config.Config, error) {
	var data []byte
	if configPath != "" {
		b, err := os.ReadFile(configPath)
		if err != nil {
			return config.Config{}, fmt.Errorf("read config: %w", err)
		}
		data = b
	}
	flags := map[string]string{"HOMECORE_MODE": mode, "HOMECORE_DATA_DIR": dataDir}
	lookup := func(key string) (string, bool) {
		if v := flags[key]; v != "" {
			return v, true
		}
		return getenv(key)
	}
	return config.LoadBytes(data, lookup)
}
