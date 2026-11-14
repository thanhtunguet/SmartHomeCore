package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/thanhtunguet/homecore/internal/auth"
	"github.com/thanhtunguet/homecore/internal/secrets"
)

func (c *cli) runToken(args ...string) error {
	c.out.Reset()
	c.errOut.Reset()
	env := cliEnv{
		Stdin: strings.NewReader(""), Stdout: &c.out, Stderr: &c.errOut,
		Getenv:     func(k string) (string, bool) { v, ok := c.env[k]; return v, ok },
		IsTerminal: func() bool { return false },
		ReadSecret: func() (string, error) { return "", errors.New("no terminal") },
		Now:        time.Now,
	}
	return runToken(append(args, "--data-dir", c.dataDir), env)
}

func TestParseTTL(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"10y": 10 * 365 * 24 * time.Hour, "3650d": 3650 * 24 * time.Hour, "720h": 720 * time.Hour, " 1d ": 24 * time.Hour,
	} {
		if got, err := parseTTL(in); err != nil || got != want {
			t.Errorf("parseTTL(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "0", "0d", "-1y", "21y", "x", "1.5y", "99999999999999999999d"} {
		if _, err := parseTTL(in); err == nil {
			t.Errorf("parseTTL(%q) accepted", in)
		}
	}
}

// `homecore token create` prints one token on stdout that the Controller's own authenticator
// accepts for the owner, with the legacy scope and the requested expiry.
func TestTokenCreateIssuesALegacyScopedToken(t *testing.T) {
	t.Setenv(secrets.EnvMasterKey, "")
	c := newCLI(t)
	c.env[envPassword] = pw1
	c.mustRun("add", "alice")

	if err := c.runToken("create", "--user", "alice", "--label", "kitchen tablet"); err != nil {
		t.Fatalf("token create: %v\n%s", err, c.errOut.String())
	}
	token := strings.TrimSpace(c.out.String())
	if strings.Count(token, ".") != 2 || strings.ContainsAny(token, " \n") {
		t.Fatalf("stdout must be the bare token, got %q", c.out.String())
	}
	if strings.Contains(c.errOut.String(), token) {
		t.Fatal("the token was also written to stderr")
	}

	// Verify with the same key material the Controller loads.
	ctx := context.Background()
	db := c.db()
	master, err := secrets.LoadOrCreateMasterKey(c.dataDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	sealer, _ := secrets.NewSealer(master)
	svc, err := auth.NewService(ctx, auth.Config{DB: db, Sealer: sealer, AccessTTL: time.Minute, RefreshTTL: time.Hour,
		Params: auth.Params{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := svc.Authenticate(ctx, token)
	if err != nil || p.Username != "alice" || p.Scope != auth.ScopeLegacy {
		t.Fatalf("authenticate: %+v %v", p, err)
	}
	claims, err := svc.Authenticator().Parse(token)
	if err != nil {
		t.Fatal(err)
	}
	if left := time.Until(time.Unix(claims.Expires, 0)); left < 9*365*24*time.Hour || left > 10*365*24*time.Hour+time.Hour {
		t.Fatalf("default lifetime is %v, want about 10 years", left)
	}

	// The audit entry records who/what/which token, never the token.
	var details string
	if err := db.QueryRow(`SELECT details FROM audit_log WHERE action = 'auth.legacy_token_issue'`).Scan(&details); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(details, "kitchen tablet") || !strings.Contains(details, claims.ID) || strings.Contains(details, token) {
		t.Fatalf("audit details: %s", details)
	}
}

func TestTokenCreateRefusals(t *testing.T) {
	t.Setenv(secrets.EnvMasterKey, "")
	c := newCLI(t)

	// No database yet: a wrong --config must not create one.
	if err := c.runToken("create", "--user", "alice"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("no database: %v", err)
	}
	c.env[envPassword] = pw1
	c.mustRun("add", "alice")
	for name, args := range map[string][]string{
		"no user":         {"create"},
		"unknown user":    {"create", "--user", "bob"},
		"bad ttl":         {"create", "--user", "alice", "--ttl", "forever"},
		"ttl too long":    {"create", "--user", "alice", "--ttl", "50y"},
		"label with ctrl": {"create", "--user", "alice", "--label", "a\nb"},
		"extra argument":  {"create", "--user", "alice", "oops"},
		"unknown subcmd":  {"list"},
		"missing subcmd":  {},
	} {
		if err := c.runToken(args...); err == nil {
			t.Errorf("%s: expected an error", name)
		} else if strings.TrimSpace(c.out.String()) != "" {
			t.Errorf("%s: printed to stdout: %q", name, c.out.String())
		}
	}
}
