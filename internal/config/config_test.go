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
		"unknown field":          "mdoe: agent\n",
		"bad mode":               "mode: server\n",
		"bad action":             "mode: agent\nagent:\n  allowed_actions: [rm-rf]\n",
		"removed bootstrap key":  "controller:\n  auth:\n    bootstrap_admin_user: admin\n", // ADR-011: accounts come from `homecore user add`
		"zero wake deadline":     "controller:\n  commands:\n    wake_deadline: 0s\n",
		"zero shutdown deadline": "controller:\n  commands:\n    shutdown_deadline: 0s\n",
		"negative retention":     "controller:\n  retention_days: -1\n",
		"one ddns provider":      "controller:\n  ddns:\n    ip_providers: [\"https://icanhazip.com\"]\n",
		"http ddns provider":     "controller:\n  ddns:\n    ip_providers: [\"http://a.example/ip\", \"https://b.example/ip\"]\n",
		"same-host providers":    "controller:\n  ddns:\n    ip_providers: [\"https://a.example/ip\", \"https://A.example/v4\"]\n",
		"provider credentials":   "controller:\n  ddns:\n    ip_providers: [\"https://u:p@a.example/ip\", \"https://b.example/ip\"]\n",
		"http cloudflare url":    "controller:\n  ddns:\n    cloudflare_api_url: http://127.0.0.1:9/client/v4\n",
		"cloudflare url query":   "controller:\n  ddns:\n    cloudflare_api_url: \"https://api.example.com/v4?x=1\"\n",
		"same listener":          "mode: both\ncontroller:\n  listen: \":8080\"\nagent:\n  listen: \":8080\"\n",
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

// REQ-RT-004: retention_days 0 is valid (keep everything); the command deadlines must be positive.
func TestRetentionZeroKeepsEverything(t *testing.T) {
	cfg, err := LoadBytes([]byte("controller:\n  retention_days: 0\n"), nil)
	if err != nil || cfg.Controller.RetentionDays != 0 {
		t.Fatalf("retention_days 0: %+v, %v", cfg.Controller.RetentionDays, err)
	}
}

// REQ-DDNS-003, REQ-RT-013: the DDNS keys have safe defaults, a YAML list replaces them, and http is
// accepted only with allow_insecure_http.
func TestDDNSConfig(t *testing.T) {
	cfg, err := LoadBytes(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	d := cfg.Controller.DDNS
	if len(d.IPProviders) < 2 || d.CloudflareAPIURL != DefaultCloudflareAPIURL || d.AllowInsecureHTTP {
		t.Fatalf("defaults: %+v", d)
	}
	cfg, err = LoadBytes([]byte("controller:\n  ddns:\n    ip_providers: [\"https://a.example/ip\", \"https://b.example/ip\"]\n"), nil)
	if err != nil || len(cfg.Controller.DDNS.IPProviders) != 2 || cfg.Controller.DDNS.IPProviders[0] != "https://a.example/ip" {
		t.Fatalf("a YAML list must replace the defaults: %+v, %v", cfg.Controller.DDNS.IPProviders, err)
	}
	dev := "controller:\n  ddns:\n    allow_insecure_http: true\n    cloudflare_api_url: http://127.0.0.1:9/client/v4\n" +
		"    ip_providers: [\"http://127.0.0.1:1/ip\", \"http://127.0.0.2:1/ip\"]\n"
	if _, err := LoadBytes([]byte(dev), nil); err != nil {
		t.Fatalf("allow_insecure_http: %v", err)
	}
	// An agent-only process does not validate controller keys.
	if _, err := LoadBytes([]byte("mode: agent\ncontroller:\n  ddns:\n    ip_providers: []\n"), nil); err != nil {
		t.Fatalf("agent mode: %v", err)
	}
}
