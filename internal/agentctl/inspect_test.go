package agentctl

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// REQ-AG-020: only private, CGNAT and loopback IP literals; never names, public or link-local ones.
func TestCheckTarget(t *testing.T) {
	good := []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.100.12", "100.107.14.130", "::1", "fd00::1", "::ffff:192.168.1.5"}
	bad := []string{"", "localhost", "tungpt-mint", "8.8.8.8", "100.128.0.1", "169.254.169.254", "fe80::1", "fe80::1%eth0",
		"0.0.0.0", "::", "224.0.0.1", "2001:4860:4860::8888", "192.168.1.5:9750", "http://10.0.0.1"}
	for _, h := range good {
		if _, err := CheckTarget("http", h, 9750); err != nil {
			t.Errorf("%q should be allowed: %v", h, err)
		}
	}
	for _, h := range bad {
		if _, err := CheckTarget("http", h, 9750); !errors.Is(err, ErrInvalidTarget) {
			t.Errorf("%q should be refused, got %v", h, err)
		}
	}
	for _, c := range []struct {
		scheme string
		port   int
	}{{"ftp", 9750}, {"", 9750}, {"http", 0}, {"https", 65536}} {
		if _, err := CheckTarget(c.scheme, "10.0.0.2", c.port); !errors.Is(err, ErrInvalidTarget) {
			t.Errorf("%+v should be refused, got %v", c, err)
		}
	}
}

func inspectServer(t *testing.T, h http.HandlerFunc) (host string, port int, hits *atomic.Int32) {
	t.Helper()
	hits = &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); h(w, r) }))
	t.Cleanup(srv.Close)
	host, p, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	port, _ = strconv.Atoi(p)
	return host, port, hits
}

const idA = "0198a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"

func TestInspectParsesAndSanitisesTheAgentAnswer(t *testing.T) {
	huge := `"` + strings.Repeat("a", 16) + `"`
	body := `{"status":"ok","agent_id":"` + strings.ToUpper(idA) + `","version":"v1.2.3\u0007 \n","trusted_key_ids":["3f9a0c1d2b4e5f60","<script>","3F9A0C1D2B4E5F60",` + huge + `]}`
	host, port, _ := inspectServer(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) })
	res, err := NewInspector().Inspect(context.Background(), "http", host, port)
	if err != nil || !res.Reachable {
		t.Fatalf("%+v %v", res, err)
	}
	if res.AgentID != idA { // canonical lower case
		t.Errorf("agent id %q", res.AgentID)
	}
	if res.Version != "v1.2.3" {
		t.Errorf("version not cleaned: %q", res.Version)
	}
	// Only well-formed lower-case 16-hex ids survive: markup and upper case are dropped.
	if len(res.TrustedKeyIDs) != 2 || res.TrustedKeyIDs[0] != "3f9a0c1d2b4e5f60" {
		t.Errorf("key ids: %q", res.TrustedKeyIDs)
	}
}

// An agent older than the field answers without trusted_key_ids: nil ("not reported"), not [].
func TestInspectDistinguishesAnOldAgentFromOneThatTrustsNobody(t *testing.T) {
	old, p1, _ := inspectServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok","agent_id":"` + idA + `","version":"v1.1.1"}`))
	})
	none, p2, _ := inspectServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok","agent_id":"` + idA + `","version":"v1.2.0","trusted_key_ids":[]}`))
	})
	in := NewInspector()
	r1, _ := in.Inspect(context.Background(), "http", old, p1)
	r2, _ := in.Inspect(context.Background(), "http", none, p2)
	if !r1.Reachable || r1.TrustedKeyIDs != nil {
		t.Errorf("old agent: %+v", r1)
	}
	if !r2.Reachable || r2.TrustedKeyIDs == nil || len(r2.TrustedKeyIDs) != 0 {
		t.Errorf("agent that trusts nobody: %+v", r2)
	}
}

func TestInspectRejectsAnswersThatAreNotAnAgent(t *testing.T) {
	for name, h := range map[string]http.HandlerFunc{
		"404":  func(w http.ResponseWriter, _ *http.Request) { http.NotFound(w, nil) },
		"html": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>router</html>")) },
		"status not ok": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"status":"down","agent_id":"` + idA + `"}`))
		},
		"no uuid": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"status":"ok","agent_id":"nope"}`))
		},
		"nil uuid": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"status":"ok","agent_id":"00000000-0000-0000-0000-000000000000"}`))
		},
	} {
		host, port, _ := inspectServer(t, h)
		res, err := NewInspector().Inspect(context.Background(), "http", host, port)
		if err != nil || res.Reachable || res.Error == "" || res.AgentID != "" {
			t.Errorf("%s: %+v %v", name, res, err)
		}
	}
}

// A redirect must not be followed: it could point the Controller at an address CheckTarget refused.
func TestInspectDoesNotFollowRedirects(t *testing.T) {
	var target atomic.Int32
	tHost, tPort, _ := inspectServer(t, func(w http.ResponseWriter, _ *http.Request) { target.Add(1) })
	host, port, _ := inspectServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://"+net.JoinHostPort(tHost, strconv.Itoa(tPort))+"/agent/v1/healthz", http.StatusFound)
	})
	res, _ := NewInspector().Inspect(context.Background(), "http", host, port)
	if res.Reachable || target.Load() != 0 {
		t.Fatalf("redirect followed: %+v, target hit %d times", res, target.Load())
	}
}

func TestInspectRefusesBeforeAnyNetworkIO(t *testing.T) {
	_, err := NewInspector().Inspect(context.Background(), "http", "8.8.8.8", 9750)
	if !errors.Is(err, ErrInvalidTarget) {
		t.Fatal(err)
	}
}
