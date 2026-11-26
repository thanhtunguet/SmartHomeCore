package agentctl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/thanhtunguet/homecore/internal/agent/protocol"
	"github.com/thanhtunguet/homecore/internal/devices"
)

// ErrInvalidTarget marks an inspect request the Controller refuses to send (bad scheme, port or
// address). It is the caller's mistake, not a network failure.
var ErrInvalidTarget = errors.New("invalid agent address")

// maxReportedKeyIDs bounds how many key ids of an agent's answer are passed on.
const maxReportedKeyIDs = 32

var keyIDPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)

// cgnat is 100.64.0.0/10 (carrier-grade NAT and Tailscale-style overlays): private in practice,
// but netip.Addr.IsPrivate does not cover it.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// InspectResult is what one agent answered on GET /agent/v1/healthz, already sanitised.
type InspectResult struct {
	Reachable bool
	AgentID   string // canonical lower-case UUID
	Version   string
	// TrustedKeyIDs is nil when the agent does not report the field (an older agent).
	TrustedKeyIDs []string
	// Error says why Reachable is false (never contains the remote body).
	Error string
}

// Inspector asks an agent who it is before a device is bound to it (REQ-AG-020). It is a server-side
// request on behalf of an authenticated user, so it only talks to private addresses, never follows
// redirects or a proxy, and returns only fields it parsed itself.
type Inspector struct {
	HTTP    *http.Client
	Timeout time.Duration // zero: DefaultTimeout
}

// NewInspector uses the same LAN client and timeout as the shutdown dispatcher.
func NewInspector() *Inspector { return &Inspector{HTTP: NewHTTPClient(), Timeout: DefaultTimeout} }

// CheckTarget validates what the caller may ask the Controller to contact: http or https, a port
// 1..65535 and an IP literal that is loopback, private (RFC 1918, ULA) or CGNAT. Link-local is
// refused on purpose: 169.254.169.254 is the cloud metadata address and the Controller may run on a VPS.
// Host names are refused: no DNS lookups on behalf of a user (also avoids DNS rebinding), and the
// Controller itself addresses devices by IP (REQ-MON-010).
func CheckTarget(scheme, host string, port int) (netip.Addr, error) {
	if scheme != "http" && scheme != "https" {
		return netip.Addr{}, fmt.Errorf("%w: scheme must be http or https", ErrInvalidTarget)
	}
	if port < 1 || port > 65535 {
		return netip.Addr{}, fmt.Errorf("%w: port must be between 1 and 65535", ErrInvalidTarget)
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(host))
	if err != nil || ip.Zone() != "" {
		return netip.Addr{}, fmt.Errorf("%w: host must be an IP address", ErrInvalidTarget)
	}
	ip = ip.Unmap()
	local := ip.IsLoopback() || ip.IsPrivate() || (ip.Is4() && cgnat.Contains(ip))
	if !local || ip.IsUnspecified() || ip.IsMulticast() {
		return netip.Addr{}, fmt.Errorf("%w: only private (RFC 1918, ULA), CGNAT or loopback addresses are allowed", ErrInvalidTarget)
	}
	return ip, nil
}

// Inspect contacts the agent at scheme://host:port. A bad target is an error (ErrInvalidTarget); a
// target that does not answer like a HomeCore agent is a result with Reachable false.
func (i *Inspector) Inspect(ctx context.Context, scheme, host string, port int) (InspectResult, error) {
	ip, err := CheckTarget(scheme, host, port)
	if err != nil {
		return InspectResult{}, err
	}
	// A throwaway device value reuses the URL builder and the error wording of the other agent calls.
	d := devices.Device{IPAddress: ip.String(), Agent: &devices.AgentBinding{Port: port, Scheme: scheme}}
	target, err := agentURL(d, protocol.HealthPath)
	if err != nil {
		return InspectResult{}, fmt.Errorf("%w: %v", ErrInvalidTarget, err)
	}
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return InspectResult{}, fmt.Errorf("%w: %v", ErrInvalidTarget, err)
	}
	timeout := i.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	r := do(ctx, i.HTTP, timeout, req)
	if r.networkFailure() {
		_, msg := classify(r.Err, d, timeout)
		return InspectResult{Error: msg}, nil
	}
	notAgent := InspectResult{Error: "the service at " + hostPort(d) + " is not a HomeCore agent"}
	var body healthBody
	if r.Status != http.StatusOK || json.Unmarshal(r.Body, &body) != nil || body.Status != "ok" {
		return notAgent, nil
	}
	id, err := uuid.Parse(body.AgentID)
	if err != nil || id == uuid.Nil {
		return notAgent, nil
	}
	res := InspectResult{Reachable: true, AgentID: id.String(), Version: clean(body.Version, 64)}
	if body.TrustedKeyIDs != nil {
		res.TrustedKeyIDs = []string{} // reported, possibly empty
		for _, k := range *body.TrustedKeyIDs {
			if keyIDPattern.MatchString(k) && len(res.TrustedKeyIDs) < maxReportedKeyIDs {
				res.TrustedKeyIDs = append(res.TrustedKeyIDs, k)
			}
		}
	}
	return res, nil
}
