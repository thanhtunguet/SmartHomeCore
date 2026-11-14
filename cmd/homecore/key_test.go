package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thanhtunguet/homecore/internal/agent/protocol"
	"github.com/thanhtunguet/homecore/internal/controllerkey"
	"github.com/thanhtunguet/homecore/internal/secrets"
	"github.com/thanhtunguet/homecore/internal/storage"
)

func runKeyCmd(t *testing.T, dataDir string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	env := cliEnv{
		Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errOut,
		Getenv:     func(string) (string, bool) { return "", false },
		IsTerminal: func() bool { return false },
		ReadSecret: func() (string, error) { return "", errors.New("no terminal") },
		Now:        time.Now,
	}
	err = runKey(append(args, "--data-dir", dataDir), env)
	return out.String(), errOut.String(), err
}

// `homecore key show` prints the public key the Controller will use, creating it when the
// Controller never ran, and never the private key.
func TestKeyShowCreatesThenReprintsTheSameKey(t *testing.T) {
	t.Setenv(secrets.EnvMasterKey, "")
	dir := filepath.Join(t.TempDir(), "data")

	out, errOut, err := runKeyCmd(t, dir, "show")
	if err != nil || !strings.Contains(errOut, "generated a new one") {
		t.Fatalf("first run: %v\nstderr: %s", err, errOut)
	}
	out2, errOut2, err := runKeyCmd(t, dir, "show")
	if err != nil || out2 != out || errOut2 != "" {
		t.Fatalf("second run differs: %v\n%s\n---\n%s\nstderr: %s", err, out, out2, errOut2)
	}

	// What it printed is what the Controller loads at start (same database, same master key).
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(dir, "homecore.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	master, _ := secrets.LoadOrCreateMasterKey(dir, nil)
	sealer, _ := secrets.NewSealer(master)
	k, created, err := controllerkey.LoadOrCreate(ctx, db, sealer, nil)
	if err != nil || created {
		t.Fatalf("controller side: created=%v err=%v", created, err)
	}
	pub := k.Public()
	for _, want := range []string{"key_id:     " + pub.KeyID, "algorithm:  ed25519", "public_key: " + pub.PublicKey, "trusted_controllers:", "- name: homecore"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if _, err := protocol.ParsePublicKey(pub.PublicKey); err != nil {
		t.Fatal(err)
	}
	// An audit row records who created it.
	var actor string
	if err := db.QueryRow(`SELECT actor FROM audit_log WHERE action = 'controller_key.create'`).Scan(&actor); err != nil || actor != "cli" {
		t.Fatalf("audit: %q %v", actor, err)
	}
}

func TestKeyUsageErrors(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{{}, {"nope"}, {"show", "extra"}} {
		if _, _, err := runKeyCmd(t, dir, args...); err == nil && len(args) > 0 && args[0] != "help" {
			t.Errorf("%v: expected an error", args)
		}
	}
}
