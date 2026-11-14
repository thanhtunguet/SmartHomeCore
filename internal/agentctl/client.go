// Package agentctl is the Controller side of the Agent protocol (docs/design/agent-protocol.md §5,
// §6): the shutdown Dispatcher that sends the signed request, and the periodic health check that
// keeps AgentBinding.status/version/last_ok_at current. Both use the same small HTTP helpers and
// both record what they learn through devices.Repository.RecordAgentContact.
//
// Files: client.go (HTTP plumbing, URLs, response parsing), shutdown.go (the Dispatcher),
// health.go (the health job).
package agentctl

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/thanhtunguet/homecore/internal/agent/protocol"
	"github.com/thanhtunguet/homecore/internal/devices"
)

// Controller-side error codes of failed shutdown commands (codes the Agent returns are passed
// through unchanged, see agentCodes). Documented in docs/requirements/shutdown-agent.md.
const (
	ErrCodeUnreachable = "agent_unreachable"  // connection refused, no route, DNS failure, TLS failure...
	ErrCodeTimeout     = "agent_timeout"      // no answer within the per-attempt timeout (2 s)
	ErrCodeBadResponse = "agent_bad_response" // something answered, but not as a HomeCore agent would
	ErrCodeAgentError  = "agent_error"        // the agent answered 5xx (or an unknown error code)
	ErrCodeDispatch    = "dispatch_error"     // Controller-side problem: signing, request building
)

// DefaultTimeout is the per-attempt timeout (LAN, fail fast, ADR-012).
const DefaultTimeout = 2 * time.Second

// maxResponseBytes bounds what is read from an agent: its answers are tiny JSON documents.
const maxResponseBytes = 8 << 10

// agentCodes are the error codes of the protocol (§3). Other codes from an agent are not trusted
// into error_code: the response is not signed in protocol v1.
var agentCodes = map[string]bool{
	protocol.CodeUnauthorized: true, protocol.CodeBadRequest: true, protocol.CodeWrongAgent: true,
	protocol.CodeActionNotAllowed: true, protocol.CodeClockSkew: true, protocol.CodeStale: true,
	protocol.CodeReplay: true, protocol.CodeBusy: true,
}

// NewHTTPClient returns the client used for LAN requests to agents: no proxy (the environment's
// HTTP_PROXY must never carry commands for a LAN device), no redirects, no connection reuse
// (a request every minute does not need it, and a stale connection must never mask a dead agent).
func NewHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:             nil,
			DisableKeepAlives: true,
			DialContext:       (&net.Dialer{Timeout: DefaultTimeout}).DialContext,

			TLSHandshakeTimeout:   DefaultTimeout,
			ResponseHeaderTimeout: DefaultTimeout,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// agentURL builds <scheme>://<host>:<agent port><path> for a bound device. The host is the one the
// monitor probes (hostname if set, else the IP address).
func agentURL(d devices.Device, path string) (string, error) {
	if d.Agent == nil {
		return "", errors.New("device has no agent binding")
	}
	scheme := d.Agent.Scheme
	if scheme != "http" && scheme != "https" {
		return "", errors.New("agent binding has an invalid scheme")
	}
	u := url.URL{Scheme: scheme, Host: net.JoinHostPort(d.ProbeHost(), strconv.Itoa(d.Agent.Port)), Path: path}
	return u.String(), nil
}

// hostPort is the address for messages (never includes a path or query).
func hostPort(d devices.Device) string {
	return net.JoinHostPort(d.ProbeHost(), strconv.Itoa(d.Agent.Port))
}

// reply is what came back from one HTTP exchange: either a transport error or a status and body.
type reply struct {
	Err    error
	Status int
	Body   []byte
}

// networkFailure reports a transport-level problem (no HTTP answer at all).
func (r reply) networkFailure() bool { return r.Err != nil }

// transient reports whether a retry may help: no answer, or a 5xx.
func (r reply) transient() bool { return r.Err != nil || r.Status >= 500 }

// do performs one request with its own timeout and reads a bounded body.
func do(ctx context.Context, hc *http.Client, timeout time.Duration, req *http.Request) reply {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resp, err := hc.Do(req.WithContext(ctx))
	if err != nil {
		return reply{Err: err}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return reply{Err: err}
	}
	return reply{Status: resp.StatusCode, Body: body}
}

// classify turns a transport error into a Controller error code and a human message.
func classify(err error, d devices.Device, timeout time.Duration) (code, msg string) {
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return ErrCodeTimeout, "the agent at " + hostPort(d) + " did not answer within " + timeout.String()
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err // drop the URL: the message names the address once, itself
	}
	return ErrCodeUnreachable, "cannot reach the agent at " + hostPort(d) + ": " + clean(err.Error(), 200)
}

// clean makes remote or system text safe to store and show: printable runes only, bounded.
func clean(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return ' '
	}, s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > max {
		s = string(r[:max]) + "..."
	}
	return s
}
