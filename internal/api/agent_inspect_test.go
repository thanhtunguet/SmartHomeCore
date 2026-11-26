package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/thanhtunguet/homecore/internal/agent/protocol"
	"github.com/thanhtunguet/homecore/internal/api/gen"
)

func otherPublicKey(t *testing.T) ed25519.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub
}

func inspectBody(port int) map[string]any { return map[string]any{"host": "127.0.0.1", "port": port} }

// REQ-AG-020: POST /agents/inspect returns the real agent's id and whether this Controller is
// trusted by it, checked against api/openapi.yaml by devEnv.
func TestInspectAgentReportsIdVersionAndTrust(t *testing.T) {
	e := newDevEnv(t)
	ctl, err := protocol.ParsePublicKey(e.key.Public().PublicKey)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		trusted []ed25519.PublicKey
		want    bool
	}{
		{"trusts this controller", []ed25519.PublicKey{ctl}, true},
		{"trusts this controller among others", []ed25519.PublicKey{otherPublicKey(t), ctl}, true},
		{"trusts only another controller", []ed25519.PublicKey{otherPublicKey(t)}, false},
		{"trusts nobody", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ra := startAgent(t, tc.trusted...)
			got := decode[gen.AgentInspection](t, ok(t, e.as("POST", "/api/v1/agents/inspect", inspectBody(ra.port)), 200))
			if !got.Reachable || got.AgentId == nil || got.AgentId.String() != ra.ag.ID() || got.Version == nil {
				t.Fatalf("inspection: %+v", got)
			}
			if got.TrustedKeyIds == nil || len(*got.TrustedKeyIds) != len(tc.trusted) {
				t.Fatalf("trusted_key_ids: %v, want %d ids", got.TrustedKeyIds, len(tc.trusted))
			}
			if got.ControllerTrusted == nil || *got.ControllerTrusted != tc.want {
				t.Fatalf("controller_trusted = %v, want %v", got.ControllerTrusted, tc.want)
			}
		})
	}
}

// Nothing listening, or something that is not an agent: 200 with reachable=false and a reason.
func TestInspectAgentReportsAnUnreachableOrForeignTarget(t *testing.T) {
	e := newDevEnv(t)
	closed := startMachine(t)
	port := closed.port
	closed.ln.Close()
	got := decode[gen.AgentInspection](t, ok(t, e.as("POST", "/api/v1/agents/inspect", inspectBody(port)), 200))
	if got.Reachable || got.Error == nil || *got.Error == "" || got.AgentId != nil {
		t.Fatalf("closed port: %+v", got)
	}

	// A live HTTP service that is not an agent: the Controller's own API has no /agent/v1/healthz.
	foreign := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(foreign.Close)
	_, p, _ := net.SplitHostPort(strings.TrimPrefix(foreign.URL, "http://"))
	fp, _ := strconv.Atoi(p)
	got = decode[gen.AgentInspection](t, ok(t, e.as("POST", "/api/v1/agents/inspect", inspectBody(fp)), 200))
	if got.Reachable || got.Error == nil || got.AgentId != nil {
		t.Fatalf("a foreign HTTP service is not an agent: %+v", got)
	}
}

// The Controller makes this request for the user, so the target is restricted (SSRF guard).
func TestInspectAgentRefusesTargetsOutsidePrivateRanges(t *testing.T) {
	e := newDevEnv(t)
	for name, body := range map[string]map[string]any{
		"public ip":      {"host": "8.8.8.8"},
		"host name":      {"host": "localhost"},
		"empty":          {"host": ""},
		"unspecified":    {"host": "0.0.0.0"},
		"multicast":      {"host": "224.0.0.1"},
		"metadata":       {"host": "169.254.169.254", "port": 80},
		"bad scheme":     {"host": "10.0.0.2", "scheme": "ftp"},
		"ipv6 linklocal": {"host": "fe80::1"},
		"bad port":       {"host": "10.0.0.2", "port": 70000},
		"unknown field":  {"host": "10.0.0.2", "path": "/x"},
	} {
		rec := e.as("POST", "/api/v1/agents/inspect", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400: %s", name, rec.Code, rec.Body)
		}
	}
	want(t, e.call("POST", "/api/v1/agents/inspect", inspectBody(9750), ""), 401)
}
