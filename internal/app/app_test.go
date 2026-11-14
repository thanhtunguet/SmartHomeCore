package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thanhtunguet/homecore/internal/audit"
	"github.com/thanhtunguet/homecore/internal/auth"
	"github.com/thanhtunguet/homecore/internal/config"
	"github.com/thanhtunguet/homecore/internal/controllerkey"
	"github.com/thanhtunguet/homecore/internal/secrets"
	"github.com/thanhtunguet/homecore/internal/storage"
)

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *syncBuffer) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// REQ-RT-001 + Phase 3: `--mode both` wired through app.Run, the way an operator does it: the
// Controller key exists first (what `homecore key show` creates), its public key goes into the
// agent's trusted_controllers, then Controller and Agent run side by side in one process. The agent
// is the real one with the platform's dry-run power controller (nothing is powered off).
func TestBothModeShutdownEndToEnd(t *testing.T) {
	t.Setenv(secrets.EnvMasterKey, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dataDir := t.TempDir()
	ctlPort, agentPort := freePort(t), freePort(t)

	// Operator step 1: the key and the first account (offline, directly on the database).
	cfg0, err := config.LoadBytes([]byte("data_dir: "+dataDir+"\n"), func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(ctx, cfg0.DatabasePath())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	master, err := secrets.LoadOrCreateMasterKey(dataDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	sealer, _ := secrets.NewSealer(master)
	key, created, err := controllerkey.LoadOrCreate(ctx, db, sealer, nil)
	if err != nil || !created {
		t.Fatalf("key: %v %v", created, err)
	}
	const password = "correct horse battery staple"
	acc := auth.NewAccounts(auth.NewStore(db), auth.NewHasher(auth.Params{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32}),
		audit.NewWriter(db, nil), nil, nil)
	if _, err := acc.Create(ctx, "alice", password, "test"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// Operator step 2: configure both roles.
	yaml := fmt.Sprintf(`mode: both
data_dir: %s
log: {level: info, format: text}
controller:
  listen: "127.0.0.1:%d"
  monitor: {tick: 100ms, offline_threshold: 2}
  commands: {shutdown_deadline: 30s}
agent:
  listen: "127.0.0.1:%d"
  dry_run: true
  action_delay: 20ms
  trusted_controllers:
    - {name: test, public_key: %q}
`, dataDir, ctlPort, agentPort, key.Public().PublicKey)
	cfg, err := config.LoadBytes([]byte(yaml), func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	logs := &syncBuffer{}
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, slog.New(slog.NewTextHandler(logs, nil)), Options{}) }()

	base := fmt.Sprintf("http://127.0.0.1:%d", ctlPort)
	call := func(method, path, token string, body any, out any) int {
		t.Helper()
		var rdr io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rdr = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, base+path, rdr)
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		if out != nil {
			if err := json.Unmarshal(raw, out); err != nil {
				t.Fatalf("%s %s: %v: %s", method, path, err, raw)
			}
		}
		return resp.StatusCode
	}
	waitFor := func(what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for !cond() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s\nlogs:\n%s", what, logs.String())
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	waitFor("the controller", func() bool {
		resp, err := http.Get(base + "/healthz")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == 200
	})
	var tokens struct {
		AccessToken string `json:"access_token"`
	}
	if st := call("POST", "/api/v1/auth/login", "", map[string]string{"username": "alice", "password": password}, &tokens); st != 200 {
		t.Fatalf("login: %d", st)
	}
	tok := tokens.AccessToken

	// The API hands out the key the operator configured, and the Controller kept (did not replace) it.
	var apiKey struct {
		KeyID     string `json:"key_id"`
		PublicKey string `json:"public_key"`
	}
	if st := call("GET", "/api/v1/system/controller-key", tok, nil, &apiKey); st != 200 || apiKey.PublicKey != key.Public().PublicKey || apiKey.KeyID != key.ID {
		t.Fatalf("controller key: %d %+v", st, apiKey)
	}

	// The agent printed its id at first start; the operator binds it to a device.
	var agentID string
	waitFor("the agent id", func() bool {
		b, err := os.ReadFile(filepath.Join(dataDir, "agent", "agent_id"))
		agentID = strings.TrimSpace(string(b))
		return err == nil && agentID != ""
	})
	machine, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer machine.Close()
	go func() {
		for {
			c, err := machine.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	var dev struct {
		ID string `json:"id"`
	}
	if st := call("POST", "/api/v1/devices", tok, map[string]any{
		"name": "both-mode pc", "ip_address": "127.0.0.1", "monitor_port": machine.Addr().(*net.TCPAddr).Port,
		"agent": map[string]any{"agent_id": agentID, "port": agentPort},
	}, &dev); st != 201 {
		t.Fatalf("create device: %d", st)
	}
	type device struct {
		Status          string `json:"status"`
		PowerTransition string `json:"power_transition"`
		Agent           struct {
			Status string `json:"status"`
		} `json:"agent"`
	}
	getDevice := func() device { var d device; call("GET", "/api/v1/devices/"+dev.ID, tok, nil, &d); return d }
	waitFor("the monitor to see the device online", func() bool { return getDevice().Status == "online" })

	// Shutdown: acknowledged by the real agent, which "powers off" (dry run).
	var cmd struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if st := call("POST", "/api/v1/devices/"+dev.ID+"/shutdown", tok, nil, &cmd); st != 202 || cmd.Status != "acknowledged" {
		t.Fatalf("shutdown: %d %+v\nlogs:\n%s", st, cmd, logs.String())
	}
	waitFor("the agent's dry-run power-off", func() bool { return strings.Contains(logs.String(), "DRY RUN: would power off") })
	if n := strings.Count(logs.String(), "DRY RUN: would power off"); n != 1 {
		t.Fatalf("%d power-offs", n)
	}
	if d := getDevice(); d.PowerTransition != "shutting_down" || d.Agent.Status != "ready" {
		t.Fatalf("device during shutdown: %+v", d)
	}

	// The machine goes down: the monitor completes the command.
	machine.Close()
	waitFor("the command to complete", func() bool {
		call("GET", "/api/v1/commands/"+cmd.ID, tok, nil, &cmd)
		return cmd.Status == "completed"
	})
	if d := getDevice(); d.Status != "offline" || d.PowerTransition != "none" {
		t.Fatalf("device after: %+v", d)
	}

	// The activity feed and the audit trail know who did it.
	var feed struct {
		Items []struct {
			Action, Actor, Result, Message string
		} `json:"items"`
	}
	if st := call("GET", "/api/v1/activity?category=command", tok, nil, &feed); st != 200 || len(feed.Items) != 1 ||
		feed.Items[0].Action != "device.shutdown" || feed.Items[0].Actor != "alice" {
		t.Fatalf("activity: %d %+v", st, feed)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil && err != context.Canceled {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("homecore did not stop after the context was cancelled")
	}
}
