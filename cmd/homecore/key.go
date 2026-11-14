package main

import (
	"context"
	"errors"
	"flag"
	"fmt"

	"github.com/thanhtunguet/homecore/internal/audit"
	"github.com/thanhtunguet/homecore/internal/controllerkey"
	"github.com/thanhtunguet/homecore/internal/secrets"
	"github.com/thanhtunguet/homecore/internal/storage"
)

const keyUsage = `Usage:
  homecore key show [flags]   print the Controller's public signing key

Flags:
  --config FILE     YAML config (env HOMECORE_CONFIG); selects the data directory
  --data-dir DIR    data directory (env HOMECORE_DATA_DIR), overrides the config

Prints the key_id and the public key to paste into an Agent's agent.trusted_controllers. Only the
public key is shown; the private key never leaves the database (sealed with the master key). The
same values are served by GET /api/v1/system/controller-key.
If the Controller has never run, the key is generated now (the Controller will use that same key
afterwards), so an Agent can be configured before the first start.
`

// runKey implements `homecore key show`. Like `homecore user`, it works on the database file and
// needs no running Controller.
func runKey(args []string, env cliEnv) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(env.Stderr, keyUsage)
		if len(args) == 0 {
			return errors.New("missing key subcommand")
		}
		return nil
	}
	if args[0] != "show" {
		fmt.Fprint(env.Stderr, keyUsage)
		return fmt.Errorf("unknown key subcommand %q", args[0])
	}
	fs := flag.NewFlagSet("key show", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	fs.Usage = func() { fmt.Fprint(env.Stderr, keyUsage) }
	envConfig, _ := env.Getenv("HOMECORE_CONFIG")
	configPath := fs.String("config", envConfig, "")
	dataDir := fs.String("data-dir", "", "")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		fmt.Fprint(env.Stderr, keyUsage)
		return errors.New("key show: unexpected arguments")
	}

	cfg, err := loadConfig(*configPath, "", *dataDir, env.Getenv)
	if err != nil {
		return err
	}
	ctx := context.Background()
	db, err := openUserDB(ctx, cfg, true)
	if err != nil {
		return err
	}
	defer db.Close()

	pub, err := controllerkey.ReadPublic(ctx, db)
	if errors.Is(err, controllerkey.ErrNoKey) {
		pub, err = createKey(ctx, cfg.DataDir, db, env)
	}
	if err != nil {
		return wrapDBError(cfg, err)
	}
	fmt.Fprintf(env.Stdout, "key_id:     %s\nalgorithm:  %s\npublic_key: %s\n\n", pub.KeyID, pub.Algorithm, pub.PublicKey)
	fmt.Fprintf(env.Stdout, "# Agent configuration (agent.trusted_controllers):\nagent:\n  trusted_controllers:\n    - name: homecore\n      public_key: %s\n", pub.PublicKey)
	return nil
}

// createKey generates the key the same way the Controller does at start.
func createKey(ctx context.Context, dataDir string, db *storage.DB, env cliEnv) (controllerkey.Public, error) {
	masterKey, err := secrets.LoadOrCreateMasterKey(dataDir, nil)
	if err != nil {
		return controllerkey.Public{}, fmt.Errorf("master key: %w", err)
	}
	sealer, err := secrets.NewSealer(masterKey)
	if err != nil {
		return controllerkey.Public{}, err
	}
	k, created, err := controllerkey.LoadOrCreate(ctx, db, sealer, env.Now)
	if err != nil {
		return controllerkey.Public{}, err
	}
	if created {
		fmt.Fprintln(env.Stderr, "no controller key existed: generated a new one")
		_ = audit.NewWriter(db, env.Now).Write(ctx, audit.Entry{Actor: "cli", Action: "controller_key.create",
			TargetType: "controller_key", TargetID: k.ID, Result: audit.ResultOK, Details: map[string]any{"key_id": k.ID}})
	}
	return k.Public(), nil
}
