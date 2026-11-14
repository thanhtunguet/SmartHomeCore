// Package config defines HomeCore runtime configuration: defaults, YAML loading, environment
// overrides and validation. Configuration is immutable after Load returns.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Mode selects which runtime roles are enabled.
type Mode string

const (
	ModeController Mode = "controller"
	ModeAgent      Mode = "agent"
	ModeBoth       Mode = "both"
)

// RunsController reports whether the Controller role is enabled.
func (m Mode) RunsController() bool { return m == ModeController || m == ModeBoth }

// RunsAgent reports whether the Agent role is enabled.
func (m Mode) RunsAgent() bool { return m == ModeAgent || m == ModeBoth }

// Config is the root configuration.
type Config struct {
	Mode            Mode          `yaml:"mode"`
	DataDir         string        `yaml:"data_dir"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
	Log             LogConfig     `yaml:"log"`
	Controller      Controller    `yaml:"controller"`
	Agent           Agent         `yaml:"agent"`
}

// LogConfig configures structured logging.
type LogConfig struct {
	Level  string `yaml:"level"`  // debug | info | warn | error
	Format string `yaml:"format"` // text | json
}

// TLS enables HTTPS on a listener when both files are set.
type TLS struct {
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

// Enabled reports whether TLS is configured.
func (t TLS) Enabled() bool { return t.CertFile != "" && t.KeyFile != "" }

// Controller configures the Controller role.
type Controller struct {
	Listen             string   `yaml:"listen"`
	TLS                TLS      `yaml:"tls"`
	APIDocs            bool     `yaml:"api_docs"`
	TrustedProxies     []string `yaml:"trusted_proxies"` // CIDRs whose X-Forwarded-For is trusted
	CORSAllowedOrigins []string `yaml:"cors_allowed_origins"`
	Auth               Auth     `yaml:"auth"`
	Monitor            Monitor  `yaml:"monitor"`
	Commands           Commands `yaml:"commands"`
	RetentionDays      int      `yaml:"retention_days"`
	DDNS               DDNS     `yaml:"ddns"`
}

// DDNS configures public-IP discovery and the Cloudflare client of the DDNS job.
type DDNS struct {
	// IPProviders are URLs that answer a plain GET with the caller's public address as bare text.
	// At least two, on different hosts; they must agree before DNS is touched. The providers learn
	// this Controller's public IP, which is inherent to asking "what is my IP".
	IPProviders []string `yaml:"ip_providers"`
	// CloudflareAPIURL is the Cloudflare v4 API root (a fake server in tests).
	CloudflareAPIURL string `yaml:"cloudflare_api_url"`
	// AllowInsecureHTTP accepts http:// provider and Cloudflare URLs. Development and tests only.
	AllowInsecureHTTP bool `yaml:"allow_insecure_http"`
}

// DefaultIPProviders are well-known plain-text endpoints on different operators. api64.ipify.org,
// icanhazip.com and ifconfig.me answer over IPv4 and IPv6; checkip.amazonaws.com is IPv4 only.
var DefaultIPProviders = []string{
	"https://api64.ipify.org",
	"https://icanhazip.com",
	"https://ifconfig.me/ip",
	"https://checkip.amazonaws.com",
}

// DefaultCloudflareAPIURL is the Cloudflare v4 API root.
const DefaultCloudflareAPIURL = "https://api.cloudflare.com/client/v4"

// Auth configures user authentication.
type Auth struct {
	AccessTokenTTL     time.Duration `yaml:"access_token_ttl"`
	RefreshTokenTTL    time.Duration `yaml:"refresh_token_ttl"`
	LoginRatePerMinute int           `yaml:"login_rate_per_minute"`
	APIRatePerMinute   int           `yaml:"api_rate_per_minute"`
}

// Monitor configures the TCP online monitor.
type Monitor struct {
	Tick             time.Duration `yaml:"tick"`
	DefaultInterval  time.Duration `yaml:"default_interval"`
	DefaultTimeout   time.Duration `yaml:"default_timeout"`
	Concurrency      int           `yaml:"concurrency"`
	OfflineThreshold int           `yaml:"offline_threshold"`
}

// Commands configures wake/shutdown command deadlines.
type Commands struct {
	WakeDeadline     time.Duration `yaml:"wake_deadline"`
	ShutdownDeadline time.Duration `yaml:"shutdown_deadline"`
}

// Agent configures the Agent role.
type Agent struct {
	Listen             string              `yaml:"listen"`
	DataDir            string              `yaml:"data_dir"`
	TLS                TLS                 `yaml:"tls"`
	AllowedActions     []string            `yaml:"allowed_actions"`
	ClockSkew          time.Duration       `yaml:"clock_skew"`
	ActionDelay        time.Duration       `yaml:"action_delay"`
	Cooldown           time.Duration       `yaml:"cooldown"`
	DryRun             bool                `yaml:"dry_run"`
	TrustedControllers []TrustedController `yaml:"trusted_controllers"`
}

// TrustedController is a Controller public key the Agent accepts commands from.
type TrustedController struct {
	Name      string `yaml:"name"`
	PublicKey string `yaml:"public_key"` // standard base64, 32-byte Ed25519 public key
}

// Default returns the built-in defaults.
func Default() Config {
	return Config{
		Mode:            ModeController,
		DataDir:         "./data",
		ShutdownTimeout: 10 * time.Second,
		Log:             LogConfig{Level: "info", Format: "text"},
		Controller: Controller{
			Listen:  ":8080",
			APIDocs: true,
			Auth: Auth{
				AccessTokenTTL:     15 * time.Minute,
				RefreshTokenTTL:    30 * 24 * time.Hour,
				LoginRatePerMinute: 10,
				APIRatePerMinute:   600,
			},
			Monitor: Monitor{
				Tick:             5 * time.Second,
				DefaultInterval:  30 * time.Second,
				DefaultTimeout:   2 * time.Second,
				Concurrency:      8,
				OfflineThreshold: 2,
			},
			Commands: Commands{
				WakeDeadline:     180 * time.Second,
				ShutdownDeadline: 120 * time.Second,
			},
			RetentionDays: 30,
			DDNS: DDNS{
				IPProviders:      slices.Clone(DefaultIPProviders),
				CloudflareAPIURL: DefaultCloudflareAPIURL,
			},
		},
		Agent: Agent{
			Listen:         ":9750",
			AllowedActions: []string{"shutdown"},
			ClockSkew:      30 * time.Second,
			ActionDelay:    3 * time.Second,
			Cooldown:       60 * time.Second,
		},
	}
}

// Load reads configuration from path (optional; "" means defaults only), applies HOMECORE_*
// environment overrides and validates the result.
func Load(path string) (Config, error) {
	var data []byte
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("read config: %w", err)
		}
		data = b
	}
	return LoadBytes(data, os.LookupEnv)
}

// LoadBytes parses YAML (may be empty) on top of defaults, applies overrides from lookupEnv
// (may be nil) and validates. Used directly by the mobile bindings.
func LoadBytes(data []byte, lookupEnv func(string) (string, bool)) (Config, error) {
	cfg := Default()
	if len(bytes.TrimSpace(data)) > 0 {
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
			return Config{}, fmt.Errorf("parse config: %w", err)
		}
	}
	if lookupEnv != nil {
		applyEnv(&cfg, lookupEnv)
	}
	cfg.finalize()
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func applyEnv(cfg *Config, lookup func(string) (string, bool)) {
	set := func(key string, dst *string) {
		if v, ok := lookup(key); ok && v != "" {
			*dst = v
		}
	}
	var mode string
	set("HOMECORE_MODE", &mode)
	if mode != "" {
		cfg.Mode = Mode(mode)
	}
	set("HOMECORE_DATA_DIR", &cfg.DataDir)
	set("HOMECORE_LOG_LEVEL", &cfg.Log.Level)
	set("HOMECORE_LOG_FORMAT", &cfg.Log.Format)
	set("HOMECORE_CONTROLLER_LISTEN", &cfg.Controller.Listen)
	set("HOMECORE_AGENT_LISTEN", &cfg.Agent.Listen)
}

// finalize derives dependent defaults.
func (c *Config) finalize() {
	if c.Agent.DataDir == "" {
		c.Agent.DataDir = filepath.Join(c.DataDir, "agent")
	}
}

// DatabasePath is the Controller SQLite file.
func (c Config) DatabasePath() string { return filepath.Join(c.DataDir, "homecore.db") }

// Validate checks semantic constraints.
func (c Config) Validate() error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	switch c.Mode {
	case ModeController, ModeAgent, ModeBoth:
	default:
		add("mode: must be controller, agent or both (got %q)", c.Mode)
	}
	if c.DataDir == "" {
		add("data_dir: required")
	}
	if !slices.Contains([]string{"debug", "info", "warn", "error"}, strings.ToLower(c.Log.Level)) {
		add("log.level: invalid %q", c.Log.Level)
	}
	if c.Log.Format != "text" && c.Log.Format != "json" {
		add("log.format: must be text or json")
	}
	if c.ShutdownTimeout <= 0 {
		add("shutdown_timeout: must be > 0")
	}

	if c.Mode.RunsController() {
		ctl := c.Controller
		if err := validListen(ctl.Listen); err != nil {
			add("controller.listen: %v", err)
		}
		if (ctl.TLS.CertFile == "") != (ctl.TLS.KeyFile == "") {
			add("controller.tls: cert_file and key_file must both be set")
		}
		for _, p := range ctl.TrustedProxies {
			if _, err := netip.ParsePrefix(p); err != nil {
				add("controller.trusted_proxies: %q is not a CIDR prefix", p)
			}
		}
		if ctl.Auth.AccessTokenTTL < time.Minute || ctl.Auth.RefreshTokenTTL < ctl.Auth.AccessTokenTTL {
			add("controller.auth: access_token_ttl >= 1m and refresh_token_ttl >= access_token_ttl")
		}
		if ctl.Monitor.Concurrency < 1 || ctl.Monitor.OfflineThreshold < 1 || ctl.Monitor.Tick <= 0 {
			add("controller.monitor: concurrency, offline_threshold and tick must be positive")
		}
		if ctl.Commands.WakeDeadline <= 0 || ctl.Commands.ShutdownDeadline <= 0 {
			add("controller.commands: wake_deadline and shutdown_deadline must be > 0")
		}
		if ctl.RetentionDays < 0 {
			add("controller.retention_days: must be >= 0 (0 keeps everything)")
		}
		for _, e := range validateDDNS(ctl.DDNS) {
			add("controller.ddns.%s", e)
		}
	}

	if c.Mode.RunsAgent() {
		ag := c.Agent
		if err := validListen(ag.Listen); err != nil {
			add("agent.listen: %v", err)
		}
		if c.Mode == ModeBoth && ag.Listen == c.Controller.Listen {
			add("agent.listen: must differ from controller.listen in both mode")
		}
		if (ag.TLS.CertFile == "") != (ag.TLS.KeyFile == "") {
			add("agent.tls: cert_file and key_file must both be set")
		}
		for _, a := range ag.AllowedActions {
			if a != "shutdown" && a != "reboot" {
				add("agent.allowed_actions: unsupported action %q", a)
			}
		}
		if ag.ClockSkew < 5*time.Second || ag.ClockSkew > 5*time.Minute {
			add("agent.clock_skew: must be between 5s and 5m")
		}
		if ag.ActionDelay < 0 || ag.Cooldown < 0 {
			add("agent.action_delay and agent.cooldown must be >= 0")
		}
		for i, tc := range ag.TrustedControllers {
			if tc.PublicKey == "" {
				add("agent.trusted_controllers[%d].public_key: required", i)
			}
		}
	}
	return errors.Join(errs...)
}

// validateDDNS checks the DDNS keys; the messages are prefixed with "controller.ddns." by the caller.
func validateDDNS(d DDNS) []string {
	var errs []string
	if len(d.IPProviders) < 2 {
		errs = append(errs, fmt.Sprintf("ip_providers: at least 2 providers are required (they must agree), got %d", len(d.IPProviders)))
	}
	hosts := map[string]bool{}
	for _, raw := range d.IPProviders {
		u, err := url.Parse(raw)
		switch {
		case err != nil || u.Host == "" || u.User != nil:
			errs = append(errs, fmt.Sprintf("ip_providers: %q is not a valid URL without credentials", raw))
		case u.Scheme != "https" && !(d.AllowInsecureHTTP && u.Scheme == "http"):
			errs = append(errs, fmt.Sprintf("ip_providers: %q must use https (allow_insecure_http is for development only)", u.Hostname()))
		case hosts[strings.ToLower(u.Hostname())]:
			errs = append(errs, fmt.Sprintf("ip_providers: %q is listed twice; providers must be on different hosts to be independent", u.Hostname()))
		default:
			hosts[strings.ToLower(u.Hostname())] = true
		}
	}
	u, err := url.Parse(d.CloudflareAPIURL)
	switch {
	case err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "":
		errs = append(errs, "cloudflare_api_url: must be an absolute URL without credentials, query or fragment")
	case u.Scheme != "https" && !(d.AllowInsecureHTTP && u.Scheme == "http"):
		errs = append(errs, "cloudflare_api_url: must use https (allow_insecure_http is for development only)")
	}
	return errs
}

func validListen(addr string) error {
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return fmt.Errorf("invalid address %q: %w", addr, err)
	}
	return nil
}
