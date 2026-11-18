// Package portforward relays TCP connections and UDP datagrams from a port of the Controller to
// another host, driven by rules stored in the database (REQ-PF-*, ADR-022).
//
// Files: portforward.go (types, validation), repo.go / repo_sqlite.go (rule persistence),
// service.go (rule CRUD, the set of running rules and its Run loop), runner.go (the listeners of
// one rule: TCP and UDP relays).
//
// A rule's `enabled` flag is the desired state and is stored; whether a listener is open now is
// runtime state. Service.Run opens every enabled rule at start-up and keeps retrying the ones that
// could not bind (port in use, interface not up yet).
package portforward

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"
	"unicode"
)

// Protocol is the kind of traffic a rule relays.
type Protocol string

const (
	TCP Protocol = "tcp"
	UDP Protocol = "udp"
	// Universal relays TCP and UDP on the same port.
	Universal Protocol = "universal"
)

// Valid reports whether p is a known protocol.
func (p Protocol) Valid() bool { return p == TCP || p == UDP || p == Universal }

// HasTCP reports whether the rule relays TCP.
func (p Protocol) HasTCP() bool { return p == TCP || p == Universal }

// HasUDP reports whether the rule relays UDP.
func (p Protocol) HasUDP() bool { return p == UDP || p == Universal }

// DefaultListenAddress binds every IPv4 interface.
const DefaultListenAddress = "0.0.0.0"

// Rule is one port forwarding rule as stored.
type Rule struct {
	ID            string
	Name          string
	Protocol      Protocol
	ListenAddress string // canonical IP literal
	ListenPort    int
	TargetHost    string // canonical IP literal or lower-case host name
	TargetPort    int
	Enabled       bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Status is what the Controller is doing with a rule now.
type Status string

const (
	// StatusRunning: the listeners are open.
	StatusRunning Status = "running"
	// StatusStopped: the rule is disabled, or the Controller has not started it yet.
	StatusStopped Status = "stopped"
	// StatusError: the rule is enabled but could not listen; it is retried.
	StatusError Status = "error"
)

// View is a Rule with its runtime state.
type View struct {
	Rule
	Status    Status
	LastError string // set when Status is StatusError
}

// Errors of the service.
var (
	// ErrNotFound: no rule has that id.
	ErrNotFound = errors.New("port forwarding rule not found")
	// ErrNameTaken: another rule already has that name (compared case-insensitively).
	ErrNameTaken = errors.New("a port forwarding rule with this name already exists")
)

// ConflictError: an enabled rule already listens where this one would.
type ConflictError struct{ With string }

func (e *ConflictError) Error() string {
	return fmt.Sprintf("rule %q is enabled on the same port, protocol and address: disable it first", e.With)
}

// ValidationError lists what is wrong with a request (HTTP 400).
type ValidationError struct{ Problems []string }

func (e *ValidationError) Error() string {
	if len(e.Problems) == 0 {
		return "invalid port forwarding rule"
	}
	return strings.Join(e.Problems, "; ")
}

const (
	maxNameLen = 100
	maxHostLen = 253
)

func cleanName(n string) (string, error) {
	n = strings.TrimSpace(n)
	if n == "" || len([]rune(n)) > maxNameLen {
		return "", fmt.Errorf("name must be 1..%d characters", maxNameLen)
	}
	if strings.IndexFunc(n, unicode.IsControl) >= 0 {
		return "", errors.New("name must not contain control characters")
	}
	return n, nil
}

func checkPort(what string, p int) error {
	if p < 1 || p > 65535 {
		return fmt.Errorf("%s must be 1..65535", what)
	}
	return nil
}

// cleanListenAddress returns the canonical form of an IP literal ("" = the default). IPv4-mapped
// IPv6 is unmapped; zones are refused (a zone would only be valid on the host that wrote it).
func cleanListenAddress(a string) (string, error) {
	a = strings.TrimSpace(a)
	if a == "" {
		return DefaultListenAddress, nil
	}
	ip, err := netip.ParseAddr(a)
	if err != nil || ip.Zone() != "" || ip.IsMulticast() {
		return "", errors.New("listen_address must be an IPv4 or IPv6 address of this host (0.0.0.0 = all interfaces)")
	}
	return ip.Unmap().String(), nil
}

// cleanTargetHost accepts an IP literal or a host name (RFC 1123 labels, plus "_").
func cleanTargetHost(h string) (string, error) {
	h = strings.TrimSpace(h)
	if h == "" || len(h) > maxHostLen {
		return "", fmt.Errorf("target_host must be 1..%d characters", maxHostLen)
	}
	if ip, err := netip.ParseAddr(h); err == nil {
		if ip.Zone() != "" {
			return "", errors.New("target_host must not carry an IPv6 zone")
		}
		return ip.Unmap().String(), nil
	}
	h = strings.ToLower(strings.TrimSuffix(h, "."))
	for _, label := range strings.Split(h, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", errors.New("target_host must be an IP address or a host name")
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
				return "", errors.New("target_host must be an IP address or a host name")
			}
		}
	}
	return h, nil
}

// selfLoop reports whether the rule would feed its own listener: it forwards to the port it
// listens on, at an address its own listener covers.
func selfLoop(r Rule) bool {
	if r.ListenPort != r.TargetPort {
		return false
	}
	listen, _ := netip.ParseAddr(r.ListenAddress)
	if r.TargetHost == r.ListenAddress {
		return true
	}
	if !listen.IsUnspecified() {
		return false
	}
	if r.TargetHost == "localhost" {
		return true
	}
	t, err := netip.ParseAddr(r.TargetHost)
	return err == nil && (t.IsLoopback() || t.IsUnspecified())
}

// validate checks a rule whose text fields were already cleaned and returns every problem. `prior`
// are the problems found while cleaning; the self-loop check is skipped then, as it needs the
// address and the target to be valid.
func validate(r Rule, prior []string) []string {
	problems := prior
	if !r.Protocol.Valid() {
		problems = append(problems, fmt.Sprintf("protocol must be tcp, udp or universal, got %q", r.Protocol))
	}
	for _, err := range []error{checkPort("listen_port", r.ListenPort), checkPort("target_port", r.TargetPort)} {
		if err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) == 0 && selfLoop(r) {
		problems = append(problems, "the rule would forward to its own listener: choose another target")
	}
	return problems
}

// overlaps reports whether two rules would bind the same socket: same port, a protocol in common,
// and addresses that are equal or one of which is the wildcard.
func overlaps(a, b Rule) bool {
	if a.ListenPort != b.ListenPort {
		return false
	}
	if !(a.Protocol.HasTCP() && b.Protocol.HasTCP() || a.Protocol.HasUDP() && b.Protocol.HasUDP()) {
		return false
	}
	if a.ListenAddress == b.ListenAddress {
		return true
	}
	ia, _ := netip.ParseAddr(a.ListenAddress)
	ib, _ := netip.ParseAddr(b.ListenAddress)
	return ia.IsUnspecified() || ib.IsUnspecified()
}
