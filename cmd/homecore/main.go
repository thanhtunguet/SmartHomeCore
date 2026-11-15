// Command homecore runs the HomeCore Controller, Agent, or both.
//
//	homecore --mode controller --config /etc/homecore/homecore.yaml
//	homecore --mode agent --agent-id UUID --trusted-controller KEY [--agent-listen :9750]
//	homecore --mode agent               (terminal, no id yet: asks for the command copied in the UI)
//	homecore --mode both
//	homecore user add|passwd|list [--config FILE] [--data-dir DIR] [USERNAME]
//	homecore token create --user USERNAME [--label TEXT] [--ttl 10y] [--config FILE] [--data-dir DIR]
//	homecore key show [--config FILE] [--data-dir DIR]
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/thanhtunguet/homecore/internal/agent"
	"github.com/thanhtunguet/homecore/internal/agentsetup"
	"github.com/thanhtunguet/homecore/internal/app"
	"github.com/thanhtunguet/homecore/internal/config"
	"github.com/thanhtunguet/homecore/internal/logging"
	"github.com/thanhtunguet/homecore/internal/platform/tty"
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
	if len(os.Args) > 1 && os.Args[1] == "token" {
		return runToken(os.Args[2:], defaultCLIEnv())
	}
	if len(os.Args) > 1 && os.Args[1] == "key" {
		return runKey(os.Args[2:], defaultCLIEnv())
	}

	var (
		configPath  = flag.String("config", os.Getenv("HOMECORE_CONFIG"), "path to YAML config file (env HOMECORE_CONFIG)")
		mode        = flag.String("mode", "", "runtime mode: controller | agent | both (overrides config)")
		dataDir     = flag.String("data-dir", "", "data directory (overrides config)")
		showVersion = flag.Bool("version", false, "print version and exit")
		agentID     = flag.String("agent-id", "", "Agent id (UUID) from the web UI; nothing is stored on disk (env HOMECORE_AGENT_ID)")
		agentListen = flag.String("agent-listen", "", "Agent listen address, e.g. :9750 (overrides agent.listen)")
		trusted     []string
	)
	flag.Func("trusted-controller", "Controller public key (base64) the Agent trusts; repeatable, added to agent.trusted_controllers", func(v string) error {
		if v == "" {
			return fmt.Errorf("empty key")
		}
		trusted = append(trusted, v)
		return nil
	})
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage:\n  homecore [flags]                    run the Controller and/or Agent\n  homecore user add|passwd|list ...   manage accounts (see `homecore user -h`)\n  homecore token create --user NAME   issue a long-lived token for a legacy client (see `homecore token -h`)\n  homecore key show                   print the Controller public key for agents (see `homecore key -h`)\n\nFlags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if *showVersion {
		fmt.Println(version.String())
		return nil
	}

	cfg, err := loadConfigWith(*configPath, map[string]string{
		"HOMECORE_MODE":         *mode,
		"HOMECORE_DATA_DIR":     *dataDir,
		"HOMECORE_AGENT_ID":     *agentID,
		"HOMECORE_AGENT_LISTEN": *agentListen,
	}, os.LookupEnv)
	if err != nil {
		return err
	}
	for _, k := range trusted {
		cfg.Agent.TrustedControllers = append(cfg.Agent.TrustedControllers, config.TrustedController{Name: "cli", PublicKey: k})
	}

	// An Agent without an identity on a terminal: ask for the command copied in the web UI and write
	// the service files for this OS (ADR-019). Without a terminal (a service) the id is created in the
	// data dir as before.
	if agentNeedsSetup(cfg) && tty.IsTerminal(os.Stdin) {
		return runAgentSetup(os.Stdin, os.Stdout)
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
	return loadConfigWith(configPath, map[string]string{"HOMECORE_MODE": mode, "HOMECORE_DATA_DIR": dataDir}, getenv)
}

// loadConfigWith is loadConfig with flag values keyed by the environment variable they override;
// empty values are ignored.
func loadConfigWith(configPath string, flags map[string]string, getenv func(string) (string, bool)) (config.Config, error) {
	var data []byte
	if configPath != "" {
		b, err := os.ReadFile(configPath)
		if err != nil {
			return config.Config{}, fmt.Errorf("read config: %w", err)
		}
		data = b
	}
	lookup := func(key string) (string, bool) {
		if v := flags[key]; v != "" {
			return v, true
		}
		return getenv(key)
	}
	return config.LoadBytes(data, lookup)
}

// agentNeedsSetup reports whether this is an Agent-only run that has no identity: no id from flag,
// environment or config, and none persisted by an earlier run.
func agentNeedsSetup(cfg config.Config) bool {
	return cfg.Mode == config.ModeAgent && cfg.Agent.ID == "" && !agent.HasStoredID(cfg.Agent.DataDir)
}

// runAgentSetup prompts for the UI command and writes the service files into the working directory.
func runAgentSetup(in *os.File, out *os.File) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate the running binary: %w", err)
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	return agentsetup.Run(in, out, agentsetup.Options{Exe: exe, Dir: dir})
}
