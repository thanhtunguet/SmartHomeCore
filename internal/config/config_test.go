package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadBytesDefaults(t *testing.T) {
	cfg, err := LoadBytes(nil, nil)
	if err != nil {
		t.Fatalf("defaults must be valid: %v", err)
	}
	if cfg.Mode != ModeController || cfg.Agent.DataDir == "" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestLoadBytesOverridesAndEnv(t *testing.T) {
	yml := []byte(`
mode: both
agent:
  listen: ":9999"
  clock_skew: 45s
`)
	env := map[string]string{"HOMECORE_LOG_LEVEL": "debug"}
	cfg, err := LoadBytes(yml, func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != ModeBoth || cfg.Agent.Listen != ":9999" || cfg.Agent.ClockSkew != 45*time.Second {
		t.Fatalf("yaml not applied: %+v", cfg)
	}
	if cfg.Log.Level != "debug" {
		t.Fatalf("env not applied: %q", cfg.Log.Level)
	}
}

func TestLoadBytesRejectsUnknownFieldsAndBadValues(t *testing.T) {
	cases := map[string]string{
		"unknown field":         "mdoe: agent\n",
		"bad mode":              "mode: server\n",
		"bad action":            "mode: agent\nagent:\n  allowed_actions: [rm-rf]\n",
		"removed bootstrap key": "controller:\n  auth:\n    bootstrap_admin_user: admin\n", // ADR-011: accounts come from `homecore user add`
		"same listener":         "mode: both\ncontroller:\n  listen: \":8080\"\nagent:\n  listen: \":8080\"\n",
	}
	for name, yml := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadBytes([]byte(yml), nil); err == nil {
				t.Fatalf("expected error for %q", strings.TrimSpace(yml))
			}
		})
	}
}

func TestShippedConfigFilesAreValid(t *testing.T) {
	files, _ := filepath.Glob("../../configs/*.yaml")
	if len(files) == 0 {
		t.Fatal("no config files found")
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, mode := range []string{"controller", "agent", "both"} {
			env := func(k string) (string, bool) { return mode, k == "HOMECORE_MODE" }
			if _, err := LoadBytes(data, env); err != nil {
				t.Errorf("%s (mode %s): %v", filepath.Base(f), mode, err)
			}
		}
	}
}
