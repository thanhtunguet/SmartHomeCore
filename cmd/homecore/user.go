package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/thanhtunguet/homecore/internal/audit"
	"github.com/thanhtunguet/homecore/internal/auth"
	"github.com/thanhtunguet/homecore/internal/config"
	"github.com/thanhtunguet/homecore/internal/platform/tty"
	"github.com/thanhtunguet/homecore/internal/storage"
)

// envPassword is the documented non-interactive alternative to the prompt. It is visible to
// processes of the same user (and root), so prefer the prompt or standard input.
const envPassword = "HOMECORE_PASSWORD"

const userUsage = `Usage:
  homecore user add    [flags] USERNAME   create an account (prompts for the password)
  homecore user passwd [flags] USERNAME   change a password and sign the user out everywhere
  homecore user list   [flags]            list accounts

Flags:
  --config FILE     YAML config (env HOMECORE_CONFIG); selects the data directory
  --data-dir DIR    data directory (env HOMECORE_DATA_DIR), overrides the config

The command works directly on <data_dir>/homecore.db; the Controller need not be running.
Password input, first match wins:
  1. environment variable HOMECORE_PASSWORD (visible to other processes of your user)
  2. standard input, one line, when it is not a terminal (echo 'secret' | homecore user add bob)
  3. a no-echo prompt on the terminal, asked twice
The password must have at least 12 characters (at most 256). It is never printed or logged.
`

// cliEnv is everything the user command takes from the process, so tests can substitute it.
type cliEnv struct {
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
	Getenv     func(string) (string, bool)
	IsTerminal func() bool            // is Stdin an interactive terminal
	ReadSecret func() (string, error) // reads one line from the terminal without echo
	Now        func() time.Time
}

func defaultCLIEnv() cliEnv {
	return cliEnv{
		Stdin:      os.Stdin,
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
		Getenv:     os.LookupEnv,
		IsTerminal: func() bool { return tty.IsTerminal(os.Stdin) },
		ReadSecret: func() (string, error) { return tty.ReadPassword(os.Stdin) },
		Now:        time.Now,
	}
}

// runUser implements `homecore user add|passwd|list`.
func runUser(args []string, env cliEnv) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(env.Stderr, userUsage)
		if len(args) == 0 {
			return errors.New("missing user subcommand")
		}
		return nil
	}
	sub := args[0]
	if sub != "add" && sub != "passwd" && sub != "list" {
		fmt.Fprint(env.Stderr, userUsage)
		return fmt.Errorf("unknown user subcommand %q", sub)
	}

	fs := flag.NewFlagSet("user "+sub, flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	fs.Usage = func() { fmt.Fprint(env.Stderr, userUsage) }
	envConfig, _ := env.Getenv("HOMECORE_CONFIG")
	configPath := fs.String("config", envConfig, "")
	dataDir := fs.String("data-dir", "", "")
	// Flags may appear before or after the username.
	var positional []string
	rest := args[1:]
	for {
		if err := fs.Parse(rest); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		if fs.NArg() == 0 {
			break
		}
		positional = append(positional, fs.Arg(0))
		rest = fs.Args()[1:]
	}

	var username string
	switch {
	case sub == "list" && len(positional) != 0, sub != "list" && len(positional) != 1:
		fmt.Fprint(env.Stderr, userUsage)
		return fmt.Errorf("user %s: wrong number of arguments", sub)
	case sub != "list":
		username = positional[0]
	}

	cfg, err := loadConfig(*configPath, "", *dataDir, env.Getenv)
	if err != nil {
		return err
	}

	ctx := context.Background()
	db, err := openUserDB(ctx, cfg, sub == "add")
	if err != nil {
		return err
	}
	defer db.Close()

	store := auth.NewStore(db)
	acc := auth.NewAccounts(store, auth.NewHasher(auth.DefaultParams), audit.NewWriter(db, env.Now), nil, env.Now)

	switch sub {
	case "list":
		users, err := acc.List(ctx)
		if err != nil {
			return wrapDBError(cfg, err)
		}
		tw := tabwriter.NewWriter(env.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "USERNAME\tID\tDISABLED\tCREATED")
		for _, u := range users {
			fmt.Fprintf(tw, "%s\t%s\t%t\t%s\n", u.Username, u.ID, u.Disabled, u.CreatedAt.Format(time.RFC3339))
		}
		return tw.Flush()

	case "add":
		if err := auth.ValidateUsername(username); err != nil {
			return err
		}
		pw, err := readNewPassword(env)
		if err != nil {
			return err
		}
		u, err := acc.Create(ctx, username, pw, "cli")
		if err != nil {
			return userError(cfg, username, err)
		}
		fmt.Fprintf(env.Stdout, "created user %q (id %s)\n", u.Username, u.ID)
		return nil

	default: // passwd
		if _, err := store.UserByUsername(ctx, username); err != nil {
			return userError(cfg, username, err)
		}
		pw, err := readNewPassword(env)
		if err != nil {
			return err
		}
		n, err := acc.SetPassword(ctx, username, pw, "cli")
		if err != nil {
			return userError(cfg, username, err)
		}
		fmt.Fprintf(env.Stdout, "password changed for %q; %d refresh token(s) revoked (access tokens already issued expire within %s)\n",
			username, n, cfg.Controller.Auth.AccessTokenTTL)
		return nil
	}
}

// openUserDB opens the Controller database. Only `add` may create it (first account before the
// first start); the other commands insist that it exists so a wrong --config is not silently
// answered with an empty database.
func openUserDB(ctx context.Context, cfg config.Config, create bool) (*storage.DB, error) {
	path := cfg.DatabasePath()
	if !create {
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("database %s does not exist: check --config/--data-dir, or create the first account with `homecore user add`", path)
		}
	}
	db, err := storage.Open(ctx, path)
	if err != nil {
		return nil, wrapDBError(cfg, err)
	}
	if _, err := db.Migrate(ctx); err != nil {
		_ = db.Close()
		return nil, wrapDBError(cfg, err)
	}
	return db, nil
}

// wrapDBError turns SQLite's busy/locked errors into an actionable message.
func wrapDBError(cfg config.Config, err error) error {
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "locked") || strings.Contains(msg, "busy") {
		return fmt.Errorf("database %s is locked by another process (waited 5 s); retry in a moment: %w", cfg.DatabasePath(), err)
	}
	return fmt.Errorf("database %s: %w", cfg.DatabasePath(), err)
}

func userError(cfg config.Config, username string, err error) error {
	switch {
	case errors.Is(err, auth.ErrUsernameTaken):
		return fmt.Errorf("user %q already exists (use `homecore user passwd %s` to change the password)", username, username)
	case errors.Is(err, auth.ErrUserNotFound):
		return fmt.Errorf("no such user %q (see `homecore user list`)", username)
	case errors.Is(err, auth.ErrPasswordTooShort), errors.Is(err, auth.ErrPasswordTooLong):
		return err
	}
	return wrapDBError(cfg, err)
}

// readNewPassword obtains the new password as documented in userUsage. It never echoes it and
// never includes it in an error.
func readNewPassword(env cliEnv) (string, error) {
	pw, err := readPasswordRaw(env)
	if err != nil {
		return "", err
	}
	if err := auth.ValidateNewPassword(pw); err != nil {
		return "", err
	}
	return pw, nil
}

func readPasswordRaw(env cliEnv) (string, error) {
	if v, ok := env.Getenv(envPassword); ok && v != "" {
		return v, nil
	}
	if env.IsTerminal() {
		fmt.Fprintf(env.Stderr, "New password (at least %d characters): ", auth.MinPasswordLength)
		first, err := env.ReadSecret()
		if err != nil {
			return "", fmt.Errorf("read password: %w", err)
		}
		fmt.Fprint(env.Stderr, "Repeat password: ")
		second, err := env.ReadSecret()
		if err != nil {
			return "", fmt.Errorf("read password: %w", err)
		}
		if first != second {
			return "", errors.New("passwords do not match")
		}
		return first, nil
	}
	line, err := bufio.NewReader(io.LimitReader(env.Stdin, 4096)).ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && line != "") {
		return "", errors.New("no password: set HOMECORE_PASSWORD, pipe it on standard input, or run in a terminal")
	}
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	if line == "" {
		return "", errors.New("empty password on standard input")
	}
	return line, nil
}
