package devices

import (
	"fmt"
	"net/netip"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/thanhtunguet/homecore/internal/wol"
)

// Limits from api/openapi.yaml (DeviceCreate / DeviceUpdate).
const (
	maxName        = 100
	maxDescription = 1000
	maxHostname    = 253
	minInterval    = 5
	maxInterval    = 3600
	minTimeoutMs   = 100
	maxTimeoutMs   = 10000
)

var (
	validTypes = map[Type]bool{TypeDesktop: true, TypeServer: true, TypeLaptop: true, TypeTV: true,
		TypeNAS: true, TypeIoT: true, TypeRouter: true}
	validOS = map[OS]bool{OSWindows: true, OSLinux: true, OSMacOS: true, OSAndroid: true,
		OSFreeBSD: true, OSEmbedded: true}

	hostLabel = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
	bareMAC   = regexp.MustCompile(`^[0-9A-Fa-f]{12}$`)
)

// AgentInput is the agent binding of a create/update request. A zero Port means the default
// (9750); an empty Scheme means http.
type AgentInput struct {
	AgentID string
	Port    int
	Scheme  string
}

// CreateInput is a validated-on-entry request to register a device. Pointer fields are optional
// and get the documented defaults.
type CreateInput struct {
	Name                   string
	Description            string
	Hostname               string
	IPAddress              string
	MACAddress             string
	WOLBroadcastAddress    string
	WOLPort                *int
	DeviceType             string
	OS                     string
	MonitorPort            int
	MonitorIntervalSeconds *int
	MonitorTimeoutMs       *int
	Enabled                *bool
	Agent                  *AgentInput
}

// UpdateInput is a partial update: nil means "unchanged"; an empty string clears description,
// hostname, mac_address and wol_broadcast_address. Agent replaces the whole binding; removing it
// is Service.RemoveAgent.
type UpdateInput struct {
	Name                   *string
	Description            *string
	Hostname               *string
	IPAddress              *string
	MACAddress             *string
	WOLBroadcastAddress    *string
	WOLPort                *int
	DeviceType             *string
	OS                     *string
	MonitorPort            *int
	MonitorIntervalSeconds *int
	MonitorTimeoutMs       *int
	Enabled                *bool
	Agent                  *AgentInput
}

// IsEmpty reports whether no field is set.
func (u UpdateInput) IsEmpty() bool { return u == UpdateInput{} }

// problems collects validation messages.
type problems []string

func (p *problems) add(format string, args ...any) { *p = append(*p, fmt.Sprintf(format, args...)) }

func (p problems) err() error {
	if len(p) == 0 {
		return nil
	}
	return &ValidationError{Problems: p}
}

// checkName trims and validates a device name (1..100 characters, no control characters).
func checkName(p *problems, s string) string {
	s = strings.TrimSpace(s)
	switch n := utf8.RuneCountInString(s); {
	case n == 0:
		p.add("name: required")
	case n > maxName:
		p.add("name: at most %d characters", maxName)
	case hasControl(s):
		p.add("name: must not contain control characters")
	}
	return s
}

func checkDescription(p *problems, s string) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > maxDescription {
		p.add("description: at most %d characters", maxDescription)
	} else if hasControl(strings.NewReplacer("\n", "", "\r", "", "\t", "").Replace(s)) {
		p.add("description: must not contain control characters")
	}
	return s
}

func hasControl(s string) bool {
	return strings.IndexFunc(s, unicode.IsControl) >= 0
}

// checkHostname accepts "" (not set) or an RFC 1123 host name (letters, digits, hyphens; labels
// of 1..63 characters; at most 253 in total; no trailing dot).
func checkHostname(p *problems, s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if !validHostname(s) {
		p.add("hostname: not a valid host name (letters, digits and hyphens, dot-separated, at most %d characters)", maxHostname)
	}
	return s
}

func validHostname(s string) bool {
	if len(s) == 0 || len(s) > maxHostname {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if !hostLabel.MatchString(label) {
			return false
		}
	}
	return true
}

// checkAddress validates ip_address: an IPv4/IPv6 literal (stored in canonical form) or a host
// name. Unspecified (0.0.0.0, ::) and multicast addresses are rejected; IPv6 zones are not
// supported.
func checkAddress(p *problems, s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		p.add("ip_address: required")
		return s
	}
	if len(s) > maxHostname {
		p.add("ip_address: at most %d characters", maxHostname)
		return s
	}
	if a, err := netip.ParseAddr(s); err == nil {
		switch {
		case a.Zone() != "":
			p.add("ip_address: IPv6 zones are not supported")
		case a.IsUnspecified() || a.IsMulticast():
			p.add("ip_address: %s is not a usable device address", s)
		}
		return a.String()
	}
	if !validHostname(s) {
		p.add("ip_address: must be an IPv4/IPv6 address or a host name")
	}
	return s
}

// checkMAC accepts "" (clears), or a 48-bit MAC in colon, dash, dot or bare-hex form, and returns
// the canonical lower-case colon form.
func checkMAC(p *problems, s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if bareMAC.MatchString(s) {
		s = strings.ToLower(s)
		s = s[0:2] + ":" + s[2:4] + ":" + s[4:6] + ":" + s[6:8] + ":" + s[8:10] + ":" + s[10:12]
	}
	mac, err := wol.NormalizeMAC(s)
	if err != nil {
		p.add("mac_address: not a valid 48-bit MAC address (expected aa:bb:cc:dd:ee:ff)")
		return s
	}
	return mac
}

// checkBroadcast accepts "" (derive automatically) or an IPv4 address (Wake-on-LAN uses UDP/IPv4).
func checkBroadcast(p *problems, s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	a, err := netip.ParseAddr(s)
	if err != nil || !a.Is4() {
		p.add("wol_broadcast_address: must be an IPv4 address")
		return s
	}
	return a.String()
}

func checkPort(p *problems, field string, v int) {
	if v < 1 || v > 65535 {
		p.add("%s: must be between 1 and 65535", field)
	}
}

func checkInterval(p *problems, seconds int) time.Duration {
	if seconds < minInterval || seconds > maxInterval {
		p.add("monitor_interval_seconds: must be between %d and %d", minInterval, maxInterval)
	}
	return time.Duration(seconds) * time.Second
}

func checkTimeout(p *problems, ms int) time.Duration {
	if ms < minTimeoutMs || ms > maxTimeoutMs {
		p.add("monitor_timeout_ms: must be between %d and %d", minTimeoutMs, maxTimeoutMs)
	}
	return time.Duration(ms) * time.Millisecond
}

func checkType(p *problems, s string) Type {
	if !validTypes[Type(s)] {
		p.add("device_type: must be one of desktop, server, laptop, tv, nas, iot, router")
	}
	return Type(s)
}

func checkOS(p *problems, s string) OS {
	if !validOS[OS(s)] {
		p.add("os: must be one of windows, linux, macos, android, freebsd, embedded")
	}
	return OS(s)
}

// checkAgent validates and normalises a binding (defaults: port 9750, scheme http).
func checkAgent(p *problems, in *AgentInput) *AgentBinding {
	id, err := uuid.Parse(strings.TrimSpace(in.AgentID))
	if err != nil || id == uuid.Nil {
		p.add("agent.agent_id: must be the agent's UUID")
	}
	port := in.Port
	if port == 0 {
		port = DefaultAgentPort
	}
	checkPort(p, "agent.port", port)
	scheme := in.Scheme
	if scheme == "" {
		scheme = DefaultAgentScheme
	}
	if scheme != "http" && scheme != "https" {
		p.add("agent.scheme: must be http or https")
	}
	return &AgentBinding{AgentID: id.String(), Port: port, Scheme: scheme}
}
