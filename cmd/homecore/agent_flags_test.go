package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thanhtunguet/homecore/internal/config"
)

const flagTestID = "0198a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"

func noEnv(string) (string, bool) { return "", false }

func TestAgentFlagsOverrideTheEnvironmentAndTheFile(t *testing.T) {
	env := func(k string) (string, bool) {
		return map[string]string{"HOMECORE_AGENT_ID": "11111111-1111-4111-8111-111111111111", "HOMECORE_AGENT_LISTEN": ":1"}[k], true
	}
	cfg, err := loadConfigWith("", map[string]string{
		"HOMECORE_MODE": "agent", "HOMECORE_AGENT_ID": flagTestID, "HOMECORE_AGENT_LISTEN": ":9800",
	}, env)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != config.ModeAgent || cfg.Agent.ID != flagTestID || cfg.Agent.Listen != ":9800" {
		t.Fatalf("%+v", cfg.Agent)
	}
}

func TestAgentIDFromTheEnvironment(t *testing.T) {
	cfg, err := loadConfigWith("", map[string]string{"HOMECORE_MODE": "agent"}, func(k string) (string, bool) { return flagTestID, k == "HOMECORE_AGENT_ID" })
	if err != nil || cfg.Agent.ID != flagTestID {
		t.Fatalf("%v %q", err, cfg.Agent.ID)
	}
}

func TestAgentNeedsSetupOnlyWithoutAnyIdentity(t *testing.T) {
	load := func(mode, id, dir string) config.Config {
		t.Helper()
		cfg, err := loadConfigWith("", map[string]string{"HOMECORE_MODE": mode, "HOMECORE_AGENT_ID": id, "HOMECORE_DATA_DIR": dir}, noEnv)
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	dir := t.TempDir()
	if !agentNeedsSetup(load("agent", "", dir)) {
		t.Error("an agent with no id anywhere needs the setup")
	}
	if agentNeedsSetup(load("agent", flagTestID, dir)) {
		t.Error("an id from a flag is enough")
	}
	if agentNeedsSetup(load("controller", "", dir)) || agentNeedsSetup(load("both", "", dir)) {
		t.Error("only the agent-only mode asks")
	}
	agentDir := filepath.Join(dir, "agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "agent_id"), []byte(flagTestID+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if agentNeedsSetup(load("agent", "", dir)) {
		t.Error("an existing install (agent_id file) keeps working without a prompt")
	}
}
