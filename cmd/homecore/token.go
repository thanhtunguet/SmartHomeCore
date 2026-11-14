package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/thanhtunguet/homecore/internal/audit"
	"github.com/thanhtunguet/homecore/internal/auth"
	"github.com/thanhtunguet/homecore/internal/secrets"
)

const tokenUsage = `Usage:
  homecore token create --user USERNAME [flags]   issue a long-lived token for a legacy client

Flags:
  --user NAME       account the token belongs to (required); see ` + "`homecore user list`" + `
  --label TEXT      what the token is for, e.g. "kitchen tablet" (recorded in the audit log)
  --ttl DURATION    lifetime: 10y, 3650d, 720h ... (default 10y, at most 20y)
  --config FILE     YAML config (env HOMECORE_CONFIG); selects the data directory
  --data-dir DIR    data directory (env HOMECORE_DATA_DIR), overrides the config

Prints the token alone on standard output (details go to standard error), so it can be captured:
  TOKEN=$(homecore token create --user alice --label "kitchen tablet")
Configure the old app to send it as "Authorization: Bearer <token>".

The token works only on GET /api/turn-on, /api/turn-off and /api/is-online; every other endpoint
answers 403. Actions are audited as the owning user. It cannot be refreshed. It stops working
when it expires, when the user is disabled or deleted, or when the JWT signing key is replaced; it
cannot be revoked individually, so give each client its own account if you need that.
The command works directly on <data_dir>/homecore.db; the Controller need not be running.
`

const defaultLegacyTTL = 10 * 365 * 24 * time.Hour

// parseTTL reads a Go duration or a whole number of days ("3650d") or years ("10y", 365 days).
func parseTTL(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	for suffix, unit := range map[string]time.Duration{"d": 24 * time.Hour, "y": 365 * 24 * time.Hour} {
		if n, ok := strings.CutSuffix(s, suffix); ok {
			v, err := strconv.Atoi(n)
			if err != nil || v <= 0 || v > int(auth.MaxLegacyTokenTTL/unit) {
				return 0, fmt.Errorf("invalid --ttl %q", s)
			}
			return time.Duration(v) * unit, nil
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 || d > auth.MaxLegacyTokenTTL {
		return 0, fmt.Errorf("invalid --ttl %q (use 10y, 3650d or 720h; at most 20y)", s)
	}
	return d, nil
}

// runToken implements `homecore token create`. Like `homecore user`, it works on the database file.
func runToken(args []string, env cliEnv) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(env.Stderr, tokenUsage)
		if len(args) == 0 {
			return errors.New("missing token subcommand")
		}
		return nil
	}
	if args[0] != "create" {
		fmt.Fprint(env.Stderr, tokenUsage)
		return fmt.Errorf("unknown token subcommand %q", args[0])
	}
	fs := flag.NewFlagSet("token create", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	fs.Usage = func() { fmt.Fprint(env.Stderr, tokenUsage) }
	envConfig, _ := env.Getenv("HOMECORE_CONFIG")
	configPath := fs.String("config", envConfig, "")
	dataDir := fs.String("data-dir", "", "")
	username := fs.String("user", "", "")
	label := fs.String("label", "", "")
	ttlText := fs.String("ttl", "10y", "")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 || *username == "" {
		fmt.Fprint(env.Stderr, tokenUsage)
		return errors.New("token create: --user is required and no other arguments are accepted")
	}
	if len(*label) > 100 || strings.ContainsFunc(*label, func(r rune) bool { return r < ' ' }) {
		return errors.New("--label must be at most 100 characters without control characters")
	}
	ttl, err := parseTTL(*ttlText)
	if err != nil {
		return err
	}

	cfg, err := loadConfig(*configPath, "", *dataDir, env.Getenv)
	if err != nil {
		return err
	}
	ctx := context.Background()
	// Only an existing database: a wrong --config must not mint a token for a fresh, empty one.
	db, err := openUserDB(ctx, cfg, false)
	if err != nil {
		return err
	}
	defer db.Close()

	masterKey, err := secrets.LoadOrCreateMasterKey(cfg.DataDir, nil)
	if err != nil {
		return fmt.Errorf("master key: %w", err)
	}
	sealer, err := secrets.NewSealer(masterKey)
	if err != nil {
		return err
	}
	lt, err := auth.IssueLegacyToken(ctx, db, sealer, *username, *label, ttl, audit.NewWriter(db, env.Now), env.Now)
	if err != nil {
		return userError(cfg, *username, err)
	}
	fmt.Fprintf(env.Stderr, "token for %q (id %s), valid until %s; works only on /api/turn-on, /api/turn-off, /api/is-online\n",
		lt.User.Username, lt.ID, lt.Expires.UTC().Format(time.RFC3339))
	fmt.Fprintln(env.Stdout, lt.Token)
	return nil
}
