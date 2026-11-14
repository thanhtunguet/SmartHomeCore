package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thanhtunguet/homecore/internal/audit"
	"github.com/thanhtunguet/homecore/internal/auth"
	"github.com/thanhtunguet/homecore/internal/secrets"
	"github.com/thanhtunguet/homecore/internal/storage"
)

const (
	pw1 = "first long password"
	pw2 = "second long password!"
)

type cli struct {
	t       *testing.T
	dataDir string
	env     map[string]string
	stdin   string
	tty     []string // lines the fake terminal returns, in order; nil = stdin is not a terminal
	out     bytes.Buffer
	errOut  bytes.Buffer
}

func newCLI(t *testing.T) *cli {
	return &cli{t: t, dataDir: filepath.Join(t.TempDir(), "data"), env: map[string]string{}}
}

func (c *cli) run(args ...string) error {
	c.out.Reset()
	c.errOut.Reset()
	ttyLines := c.tty
	env := cliEnv{
		Stdin:      strings.NewReader(c.stdin),
		Stdout:     &c.out,
		Stderr:     &c.errOut,
		Getenv:     func(k string) (string, bool) { v, ok := c.env[k]; return v, ok },
		IsTerminal: func() bool { return ttyLines != nil },
		ReadSecret: func() (string, error) {
			if len(ttyLines) == 0 {
				return "", errors.New("no more input")
			}
			l := ttyLines[0]
			ttyLines = ttyLines[1:]
			return l, nil
		},
		Now: time.Now,
	}
	return runUser(append(args, "--data-dir", c.dataDir), env)
}

func (c *cli) db() *storage.DB {
	c.t.Helper()
	db, err := storage.Open(context.Background(), filepath.Join(c.dataDir, "homecore.db"))
	if err != nil {
		c.t.Fatal(err)
	}
	c.t.Cleanup(func() { _ = db.Close() })
	return db
}

func (c *cli) mustRun(args ...string) {
	c.t.Helper()
	if err := c.run(args...); err != nil {
		c.t.Fatalf("homecore user %v: %v\nstderr: %s", args, err, c.errOut.String())
	}
}

func (c *cli) assertNoSecret(secrets ...string) {
	c.t.Helper()
	for _, s := range secrets {
		if strings.Contains(c.out.String(), s) || strings.Contains(c.errOut.String(), s) {
			c.t.Fatalf("password echoed in output: stdout=%q stderr=%q", c.out.String(), c.errOut.String())
		}
	}
}

func newService(t *testing.T, db *storage.DB) *auth.Service {
	t.Helper()
	sealer, err := secrets.NewSealer(bytes.Repeat([]byte{5}, secrets.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	svc, err := auth.NewService(context.Background(), auth.Config{DB: db, Sealer: sealer, Audit: audit.NewWriter(db, nil),
		AccessTTL: 15 * time.Minute, RefreshTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

// End to end: add (stdin) -> login against the same database -> passwd (env) -> old session
// gone, old password gone, new password works -> list.
func TestUserAddPasswdListEndToEnd(t *testing.T) {
	c := newCLI(t)
	ctx := context.Background()

	c.stdin = pw1 + "\n"
	c.mustRun("add", "alice")
	if !strings.Contains(c.out.String(), `created user "alice"`) {
		t.Fatalf("unexpected output %q", c.out.String())
	}
	c.assertNoSecret(pw1)

	svc := newService(t, c.db())
	session, err := svc.Login(ctx, "alice", pw1, "")
	if err != nil {
		t.Fatalf("account created by the CLI cannot log in: %v", err)
	}

	c.stdin = ""
	c.env[envPassword] = pw2
	c.mustRun("passwd", "alice")
	if !strings.Contains(c.out.String(), "1 refresh token(s) revoked") {
		t.Fatalf("unexpected output %q", c.out.String())
	}
	c.assertNoSecret(pw1, pw2)

	if _, err := svc.Refresh(ctx, session.RefreshToken, ""); err != auth.ErrInvalidRefresh {
		t.Fatalf("session survived passwd: %v", err)
	}
	if _, err := svc.Login(ctx, "alice", pw1, ""); err != auth.ErrInvalidCredentials {
		t.Fatalf("old password still valid: %v", err)
	}
	if _, err := svc.Login(ctx, "alice", pw2, ""); err != nil {
		t.Fatalf("new password rejected: %v", err)
	}

	delete(c.env, envPassword)
	c.stdin = "another long password\n"
	c.mustRun("add", "bob")
	c.mustRun("list")
	out := c.out.String()
	for _, want := range []string{"USERNAME", "alice", "bob", "false"} {
		if !strings.Contains(out, want) {
			t.Errorf("list output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "argon2") || strings.Contains(out, "$") {
		t.Fatalf("list prints hashes:\n%s", out)
	}
	if strings.Index(out, "alice") > strings.Index(out, "bob") {
		t.Fatal("list not ordered by username")
	}

	// Both account changes are audited by the CLI, without the passwords.
	var n int
	if err := c.db().QueryRow(`SELECT count(*) FROM audit_log WHERE actor = 'cli' AND action IN ('user.create', 'user.password_change')`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("cli audit rows = %d (%v), want 3", n, err)
	}
}

func TestUserAddPrompt(t *testing.T) {
	c := newCLI(t)
	c.tty = []string{pw1, pw1}
	c.mustRun("add", "alice")
	if !strings.Contains(c.errOut.String(), "New password") || !strings.Contains(c.errOut.String(), "Repeat password") {
		t.Fatalf("no prompt shown: %q", c.errOut.String())
	}
	c.assertNoSecret(pw1)

	c.tty = []string{pw1, "typo typo typo typo"}
	err := c.run("add", "carol")
	if err == nil || !strings.Contains(err.Error(), "do not match") {
		t.Fatalf("mismatched confirmation: %v", err)
	}
	if strings.Contains(err.Error(), pw1) {
		t.Fatal("error leaks the password")
	}
	var n int
	c.db().QueryRow(`SELECT count(*) FROM users WHERE username = 'carol'`).Scan(&n)
	if n != 0 {
		t.Fatal("account created despite mismatch")
	}
}

func TestUserFlagsMayComeBeforeOrAfterTheUsername(t *testing.T) {
	c := newCLI(t)
	run := func(args ...string) error {
		return runUser(args, cliEnv{Stdin: strings.NewReader(pw1), Stdout: &c.out, Stderr: &c.errOut,
			Getenv: func(string) (string, bool) { return "", false }, IsTerminal: func() bool { return false }, Now: time.Now})
	}
	if err := run("add", "--data-dir", c.dataDir, "alice"); err != nil { // before; stdin has no trailing newline
		t.Fatal(err)
	}
	if err := run("add", "bob", "--data-dir", c.dataDir); err != nil { // after
		t.Fatal(err)
	}
	if err := run("add", "bob", "carol", "--data-dir", c.dataDir); err == nil {
		t.Fatal("two usernames accepted")
	}
	var n int
	c.db().QueryRow(`SELECT count(*) FROM users`).Scan(&n)
	if n != 2 {
		t.Fatalf("%d users, want 2", n)
	}
}

func TestUserPasswordPolicyAndErrors(t *testing.T) {
	c := newCLI(t)
	c.stdin = "short\n"
	if err := c.run("add", "alice"); !errors.Is(err, auth.ErrPasswordTooShort) {
		t.Fatalf("short password: %v", err)
	}
	c.stdin = strings.Repeat("a", 257) + "\n"
	if err := c.run("add", "alice"); !errors.Is(err, auth.ErrPasswordTooLong) {
		t.Fatalf("long password: %v", err)
	}
	c.stdin = pw1 + "\n"
	if err := c.run("add", "bad name"); err == nil {
		t.Fatal("invalid username accepted")
	}
	c.mustRun("add", "alice")
	c.stdin = pw1 + "\n"
	if err := c.run("add", "ALICE"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate: %v", err)
	}
	c.stdin = pw2 + "\n"
	if err := c.run("passwd", "ghost"); err == nil || !strings.Contains(err.Error(), "no such user") {
		t.Fatalf("unknown user: %v", err)
	}
	c.stdin = ""
	if err := c.run("passwd", "alice"); err == nil || !strings.Contains(err.Error(), "no password") {
		t.Fatalf("no password source: %v", err)
	}
	if err := c.run("frobnicate"); err == nil {
		t.Fatal("unknown subcommand accepted")
	}
	if err := c.run("add"); err == nil {
		t.Fatal("missing username accepted")
	}
}

func TestUserCommandsRequireExistingDatabaseExceptAdd(t *testing.T) {
	c := newCLI(t)
	for _, args := range [][]string{{"list"}, {"passwd", "alice"}} {
		c.stdin = pw1 + "\n"
		err := c.run(args...)
		if err == nil || !strings.Contains(err.Error(), "does not exist") {
			t.Fatalf("%v on a missing database: %v", args, err)
		}
	}
	// The failed commands must not have created a database as a side effect.
	if _, err := os.Stat(filepath.Join(c.dataDir, "homecore.db")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a command that needs an existing database created one (stat err %v)", err)
	}
}

func TestUserCommandReportsLockedDatabase(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for the 5 s busy timeout")
	}
	c := newCLI(t)
	c.stdin = pw1 + "\n"
	c.mustRun("add", "alice")

	// Another process (the Controller) holds a write transaction.
	db := c.db()
	tx, err := db.BeginTx(context.Background(), nil) // _txlock=immediate: takes the write lock now
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	c.stdin = pw2 + "\n"
	start := time.Now()
	err = c.run("passwd", "alice")
	if err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("locked database: %v", err)
	}
	if time.Since(start) > 15*time.Second {
		t.Fatal("did not give up in reasonable time")
	}
	c.assertNoSecret(pw2)
}

func TestUserDataDirFromEnvironment(t *testing.T) {
	c := newCLI(t)
	c.env["HOMECORE_DATA_DIR"] = c.dataDir
	c.stdin = pw1 + "\n"
	env := cliEnv{Stdin: strings.NewReader(c.stdin), Stdout: &c.out, Stderr: &c.errOut,
		Getenv:     func(k string) (string, bool) { v, ok := c.env[k]; return v, ok },
		IsTerminal: func() bool { return false }, Now: time.Now}
	if err := runUser([]string{"add", "alice"}, env); err != nil { // no --data-dir flag
		t.Fatal(err)
	}
	if _, err := storage.Open(context.Background(), filepath.Join(c.dataDir, "homecore.db")); err != nil {
		t.Fatal(err)
	}
	var n int
	c.db().QueryRow(`SELECT count(*) FROM users`).Scan(&n)
	if n != 1 {
		t.Fatalf("user not created in the HOMECORE_DATA_DIR database (%d)", n)
	}
}
