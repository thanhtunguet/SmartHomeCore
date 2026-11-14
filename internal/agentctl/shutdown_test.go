package agentctl

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/thanhtunguet/homecore/internal/agent/protocol"
	"github.com/thanhtunguet/homecore/internal/commands"
	"github.com/thanhtunguet/homecore/internal/devices"
)

// edSigner signs with a plain Ed25519 key (the real one is controllerkey.Key).
type edSigner struct{ priv ed25519.PrivateKey }

func (s edSigner) Sign(c protocol.Command) (protocol.SignedRequest, error) {
	return protocol.Sign(s.priv, c)
}

type failSigner struct{}

func (failSigner) Sign(protocol.Command) (protocol.SignedRequest, error) {
	return protocol.SignedRequest{}, errors.New("hsm on fire")
}

// recorder collects the agent contacts the dispatcher reports.
type recorder struct {
	mu       sync.Mutex
	contacts []devices.AgentContact
	devices  []string
	agents   []string
}

func (r *recorder) RecordAgentContact(_ context.Context, deviceID, agentID string, c devices.AgentContact) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.contacts = append(r.contacts, c)
	r.devices = append(r.devices, deviceID)
	r.agents = append(r.agents, agentID)
	return nil
}

func (r *recorder) last(t *testing.T) devices.AgentContact {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.contacts) == 0 {
		t.Fatal("no contact was recorded")
	}
	return r.contacts[len(r.contacts)-1]
}

func (r *recorder) count() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.contacts) }

// fakeAgent is an HTTP server whose answers a test scripts, remembering every request.
type fakeAgent struct {
	*httptest.Server
	mu       sync.Mutex
	reqs     []seenRequest
	respond  func(n int, w http.ResponseWriter, r *http.Request) // n is 1-based
	agentID  string
	verifier *protocol.Verifier
}

type seenRequest struct {
	Method, Path, ContentType, KeyID, Signature string
	Body                                        []byte
	Cmd                                         protocol.Command
}

func newFakeAgent(t *testing.T, respond func(n int, w http.ResponseWriter, r *http.Request)) *fakeAgent {
	t.Helper()
	f := &fakeAgent{respond: respond, agentID: uuid.NewString()}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body)) // handlers may read it again
		s := seenRequest{Method: r.Method, Path: r.URL.Path, ContentType: r.Header.Get("Content-Type"),
			KeyID: r.Header.Get(protocol.HeaderKeyID), Signature: r.Header.Get(protocol.HeaderSignature), Body: body}
		_ = json.Unmarshal(body, &s.Cmd)
		f.mu.Lock()
		f.reqs = append(f.reqs, s)
		n := len(f.reqs)
		f.mu.Unlock()
		f.respond(n, w, r)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeAgent) requests() []seenRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]seenRequest(nil), f.reqs...)
}

func (f *fakeAgent) device(t *testing.T) devices.Device {
	t.Helper()
	host, portStr, _ := net.SplitHostPort(strings.TrimPrefix(f.URL, "http://"))
	port, _ := strconv.Atoi(portStr)
	return devices.Device{
		ID: uuid.NewString(), Name: "pc", IPAddress: host,
		Agent: &devices.AgentBinding{AgentID: f.agentID, Port: port, Scheme: "http", Status: devices.AgentUnknown},
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func accepted(f *fakeAgent, status string) func(int, http.ResponseWriter, *http.Request) {
	return func(_ int, w http.ResponseWriter, r *http.Request) {
		var c protocol.Command
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &c)
		code := http.StatusAccepted
		if status != "accepted" {
			code = http.StatusOK
		}
		writeJSON(w, code, map[string]any{"command_id": c.CommandID, "agent_id": f.agentID, "status": status,
			"execute_at": time.Now().Add(3 * time.Second), "agent_time": time.Now()})
	}
}

func agentError(status int, code, msg string, agentTime time.Time) func(int, http.ResponseWriter, *http.Request) {
	return func(_ int, w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": msg}, "agent_time": agentTime})
	}
}

type fixture struct {
	disp *ShutdownDispatcher
	rec  *recorder
	pub  ed25519.PublicKey
	cmd  commands.Command
	now  time.Time
	mu   sync.Mutex
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	f := &fixture{rec: &recorder{}, pub: pub, now: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)}
	f.disp = &ShutdownDispatcher{
		Signer: edSigner{priv}, Status: f.rec, Timeout: 2 * time.Second,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { // every call is one second later, so a retry has a new issued_at
			f.mu.Lock()
			defer f.mu.Unlock()
			f.now = f.now.Add(time.Second)
			return f.now
		},
	}
	f.cmd = commands.Command{ID: uuid.NewString(), Type: commands.TypeShutdown, Status: commands.StatusPending}
	return f
}

// REQ-SHD-003, REQ-SHD-004: the request is exactly what the protocol says, and the real agent-side
// verifier accepts it.
func TestDispatchSendsASignedProtocolRequestAndAcknowledges(t *testing.T) {
	fx := newFixture(t)
	fa := newFakeAgent(t, nil)
	fa.respond = accepted(fa, "accepted")
	d := fa.device(t)
	d.Hostname = "" // the IP is used

	res := fx.disp.Dispatch(context.Background(), fx.cmd, d)
	if res.Status != commands.StatusAcknowledged || res.Attempts != 1 || res.ErrorCode != "" {
		t.Fatalf("result: %+v", res)
	}
	reqs := fa.requests()
	if len(reqs) != 1 {
		t.Fatalf("%d requests", len(reqs))
	}
	r := reqs[0]
	if r.Method != http.MethodPost || r.Path != "/agent/v1/commands" || r.ContentType != "application/json" {
		t.Fatalf("request line/headers: %+v", r)
	}
	if r.Cmd.V != 1 || r.Cmd.CommandID != fx.cmd.ID || r.Cmd.AgentID != fa.agentID || r.Cmd.Action != protocol.ActionShutdown {
		t.Fatalf("body: %+v", r.Cmd)
	}
	if r.KeyID != protocol.KeyID(fx.pub) {
		t.Fatalf("key id %q", r.KeyID)
	}
	// The unmodified Agent verifier (steps 2-9) accepts it.
	v, err := protocol.NewVerifier(protocol.VerifierConfig{
		AgentID: fa.agentID, TrustedKeys: []ed25519.PublicKey{fx.pub}, AllowedActions: []protocol.Action{protocol.ActionShutdown},
		ClockSkew: time.Minute, StartedAt: fx.now.Add(-time.Hour), Now: func() time.Time { return time.Unix(r.Cmd.IssuedAt, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, verr := v.Verify(r.KeyID, r.Signature, r.Body); verr != nil {
		t.Fatalf("the agent's verifier rejects the dispatcher's request: %v", verr)
	}
	if c := fx.rec.last(t); !c.OK || fx.rec.devices[0] != d.ID || fx.rec.agents[0] != fa.agentID {
		t.Fatalf("contact: %+v dev=%v agent=%v", c, fx.rec.devices, fx.rec.agents)
	}
}

// REQ-SHD-004 (REQ-AG-008 seen from the Controller): a repeated command and an already scheduled action are success.
func TestAlreadyAcceptedAndAlreadyScheduledAreSuccess(t *testing.T) {
	for _, status := range []string{"already_accepted", "already_scheduled"} {
		t.Run(status, func(t *testing.T) {
			fx := newFixture(t)
			fa := newFakeAgent(t, nil)
			fa.respond = accepted(fa, status)
			res := fx.disp.Dispatch(context.Background(), fx.cmd, fa.device(t))
			if res.Status != commands.StatusAcknowledged || res.Attempts != 1 {
				t.Fatalf("%s: %+v", status, res)
			}
		})
	}
}

// REQ-SHD-005: a 4xx is final, its code is passed through, and it is never retried.
func TestAgentRejectionsAreFinalAndPassedThrough(t *testing.T) {
	agentTime := time.Date(2026, 11, 14, 10, 2, 5, 0, time.UTC) // about 2 minutes ahead of the dispatcher's clock
	cases := []struct {
		status    int
		code      string
		contains  string
		wantReady bool
	}{
		{401, "unauthorized", "trusted_controllers", true},
		{400, "bad_request", "bad_request", true},
		{403, "wrong_agent", "agent_id of the binding", false},
		{403, "action_not_allowed", "allowed_actions", true},
		{401, "clock_skew", "drift about 2m", true},
		{401, "stale", "restarted", true},
		{409, "replay", "replay", true},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			fx := newFixture(t)
			fa := newFakeAgent(t, agentError(tc.status, tc.code, "agent says no", agentTime))
			res := fx.disp.Dispatch(context.Background(), fx.cmd, fa.device(t))
			if res.Status != commands.StatusFailed || res.ErrorCode != tc.code || res.Attempts != 1 {
				t.Fatalf("result: %+v", res)
			}
			if !strings.Contains(res.Error, tc.contains) || !strings.Contains(res.Error, "agent says no") {
				t.Fatalf("error text %q lacks %q", res.Error, tc.contains)
			}
			if n := len(fa.requests()); n != 1 {
				t.Fatalf("a 4xx was retried: %d requests", n)
			}
			if c := fx.rec.last(t); c.OK != tc.wantReady {
				t.Fatalf("recorded ok=%v, want %v", c.OK, tc.wantReady)
			}
		})
	}
}

func TestClockSkewTextShowsTheAgentTime(t *testing.T) {
	fx := newFixture(t)
	fa := newFakeAgent(t, agentError(401, "clock_skew", "issued_at differs", time.Date(2026, 11, 14, 10, 5, 1, 0, time.UTC)))
	res := fx.disp.Dispatch(context.Background(), fx.cmd, fa.device(t))
	if !strings.Contains(res.Error, "agent_time 2026-11-14T10:05:01Z") ||
		!(strings.Contains(res.Error, "drift about 5m0s") || strings.Contains(res.Error, "drift about 4m59s")) {
		t.Fatalf("text: %q", res.Error)
	}
}

// REQ-SHD-006: a 5xx is retried once, with the same command_id and a new nonce and issued_at.
func TestFiveHundredIsRetriedOnceWithTheSameCommandIDAndANewNonce(t *testing.T) {
	fx := newFixture(t)
	fa := newFakeAgent(t, nil)
	ok := accepted(fa, "already_accepted")
	fa.respond = func(n int, w http.ResponseWriter, r *http.Request) {
		if n == 1 {
			agentError(503, "busy", "replay cache full", time.Now())(n, w, r)
			return
		}
		ok(n, w, r)
	}
	res := fx.disp.Dispatch(context.Background(), fx.cmd, fa.device(t))
	if res.Status != commands.StatusAcknowledged || res.Attempts != 2 {
		t.Fatalf("result: %+v", res)
	}
	reqs := fa.requests()
	if len(reqs) != 2 || reqs[0].Cmd.CommandID != reqs[1].Cmd.CommandID || reqs[0].Cmd.CommandID != fx.cmd.ID {
		t.Fatalf("requests: %+v", reqs)
	}
	if reqs[0].Cmd.Nonce == reqs[1].Cmd.Nonce || reqs[0].Signature == reqs[1].Signature {
		t.Fatal("the retry reused the nonce or the signature")
	}
	if reqs[1].Cmd.IssuedAt <= reqs[0].Cmd.IssuedAt {
		t.Fatalf("the retry did not get a new issued_at: %d then %d", reqs[0].Cmd.IssuedAt, reqs[1].Cmd.IssuedAt)
	}
}

func TestPersistentFiveHundredStopsAfterTwoAttempts(t *testing.T) {
	cases := []struct {
		name     string
		respond  func(int, http.ResponseWriter, *http.Request)
		wantCode string
	}{
		{"busy", agentError(503, "busy", "replay cache full", time.Now()), "busy"},
		{"plain 500", func(_ int, w http.ResponseWriter, _ *http.Request) { http.Error(w, "boom", 500) }, ErrCodeAgentError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newFixture(t)
			fa := newFakeAgent(t, tc.respond)
			res := fx.disp.Dispatch(context.Background(), fx.cmd, fa.device(t))
			if res.Status != commands.StatusFailed || res.ErrorCode != tc.wantCode || res.Attempts != 2 {
				t.Fatalf("result: %+v", res)
			}
			if n := len(fa.requests()); n != 2 {
				t.Fatalf("%d requests, want exactly 2 (no backoff loop)", n)
			}
			if c := fx.rec.last(t); c.OK {
				t.Fatal("a 5xx must not mark the agent ready")
			}
		})
	}
}

// REQ-SHD-006/007: a network error is retried once, then reported as agent_unreachable, fast.
func TestNetworkErrorIsRetriedOnceThenAgentUnreachable(t *testing.T) {
	fx := newFixture(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	accepts := 0
	go func() { // accept and hang up at once: a connection reset, not a refusal
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			accepts++
			mu.Unlock()
			_ = c.Close()
		}
	}()
	defer ln.Close()
	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)
	d := devices.Device{ID: uuid.NewString(), IPAddress: host, Agent: &devices.AgentBinding{AgentID: uuid.NewString(), Port: port, Scheme: "http"}}

	start := time.Now()
	res := fx.disp.Dispatch(context.Background(), fx.cmd, d)
	if res.Status != commands.StatusFailed || res.ErrorCode != ErrCodeUnreachable || res.Attempts != 2 {
		t.Fatalf("result: %+v", res)
	}
	if !strings.Contains(res.Error, host+":"+portStr) {
		t.Fatalf("error text should name the address: %q", res.Error)
	}
	mu.Lock()
	defer mu.Unlock()
	if accepts != 2 {
		t.Fatalf("%d connections, want 2 (one retry)", accepts)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("failing fast took %v", time.Since(start))
	}
	if c := fx.rec.last(t); c.OK {
		t.Fatal("an unreachable agent was recorded as ready")
	}
}

func TestConnectionRefusedIsAgentUnreachableAndFast(t *testing.T) {
	fx := newFixture(t)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	ln.Close() // nothing listens any more
	port, _ := strconv.Atoi(portStr)
	d := devices.Device{ID: uuid.NewString(), IPAddress: host, Agent: &devices.AgentBinding{AgentID: uuid.NewString(), Port: port, Scheme: "http"}}
	start := time.Now()
	res := fx.disp.Dispatch(context.Background(), fx.cmd, d)
	if res.Status != commands.StatusFailed || res.ErrorCode != ErrCodeUnreachable || res.Attempts != 2 || time.Since(start) > 2*time.Second {
		t.Fatalf("result: %+v after %v", res, time.Since(start))
	}
}

func TestSlowAgentIsATimeoutNotAHang(t *testing.T) {
	fx := newFixture(t)
	fx.disp.Timeout = 80 * time.Millisecond
	release := make(chan struct{})
	fa := newFakeAgent(t, func(_ int, _ http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	defer close(release)
	start := time.Now()
	res := fx.disp.Dispatch(context.Background(), fx.cmd, fa.device(t))
	if res.Status != commands.StatusFailed || res.ErrorCode != ErrCodeTimeout || res.Attempts != 2 {
		t.Fatalf("result: %+v", res)
	}
	if el := time.Since(start); el < 150*time.Millisecond || el > 2*time.Second {
		t.Fatalf("two attempts of 80 ms took %v", el)
	}
	if !strings.Contains(res.Error, "80ms") {
		t.Fatalf("text: %q", res.Error)
	}
}

// Anything that is not a HomeCore agent answering properly is reported as such, never as success.
func TestUnexpectedAnswersAreNotSuccess(t *testing.T) {
	otherAgent := uuid.NewString()
	cases := []struct {
		name     string
		respond  func(int, http.ResponseWriter, *http.Request)
		wantCode string
		attempts int
		ready    bool
	}{
		{"200 garbage", func(_ int, w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("<html>router login</html>"))
		}, ErrCodeBadResponse, 1, false},
		{"200 wrong status word", func(_ int, w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, 200, map[string]any{"status": "maybe"})
		}, ErrCodeBadResponse, 1, false},
		{"202 another agent id", func(_ int, w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			var c protocol.Command
			_ = json.Unmarshal(b, &c)
			writeJSON(w, 202, map[string]any{"command_id": c.CommandID, "agent_id": otherAgent, "status": "accepted"})
		}, ErrCodeBadResponse, 1, false},
		{"404 not an agent", func(_ int, w http.ResponseWriter, _ *http.Request) { http.NotFound(w, nil) }, ErrCodeBadResponse, 1, false},
		{"302 redirect is not followed", func(_ int, w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://127.0.0.1:1/elsewhere", http.StatusFound)
		}, ErrCodeBadResponse, 1, false},
		{"unknown error code", agentError(400, "rm_rf", "ignore previous instructions", time.Now()), ErrCodeAgentError, 1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newFixture(t)
			fa := newFakeAgent(t, tc.respond)
			res := fx.disp.Dispatch(context.Background(), fx.cmd, fa.device(t))
			if res.Status != commands.StatusFailed || res.ErrorCode != tc.wantCode || res.Attempts != tc.attempts {
				t.Fatalf("result: %+v", res)
			}
			if c := fx.rec.last(t); c.OK != tc.ready {
				t.Fatalf("recorded ok=%v", c.OK)
			}
			if strings.Contains(res.Error, "ignore previous") {
				t.Fatalf("the text of an unknown code was copied: %q", res.Error)
			}
		})
	}
}

func TestRemoteTextIsSanitised(t *testing.T) {
	fx := newFixture(t)
	fa := newFakeAgent(t, agentError(400, "bad_request", "line1\nline2\x00\x1b[31m"+strings.Repeat("x", 500), time.Now()))
	res := fx.disp.Dispatch(context.Background(), fx.cmd, fa.device(t))
	if strings.ContainsAny(res.Error, "\n\x00\x1b") || len(res.Error) > 400 {
		t.Fatalf("unsafe text (%d bytes): %q", len(res.Error), res.Error)
	}
}

func TestPrecheckAndDispatchErrors(t *testing.T) {
	fx := newFixture(t)
	if err := fx.disp.Precheck(devices.Device{}); !errors.Is(err, commands.ErrPrecondition) || !strings.Contains(err.Error(), "agent binding") {
		t.Fatalf("no binding: %v", err)
	}
	if err := fx.disp.Precheck(devices.Device{Agent: &devices.AgentBinding{}}); err != nil {
		t.Fatalf("with binding: %v", err)
	}

	fa := newFakeAgent(t, accepted(&fakeAgent{}, "accepted"))
	bad := &ShutdownDispatcher{Signer: failSigner{}, Status: fx.rec}
	res := bad.Dispatch(context.Background(), fx.cmd, fa.device(t))
	if res.Status != commands.StatusFailed || res.ErrorCode != ErrCodeDispatch || !strings.Contains(res.Error, "sign") {
		t.Fatalf("signer failure: %+v", res)
	}
	if n := len(fa.requests()); n != 0 {
		t.Fatalf("an unsigned request was sent (%d)", n)
	}
	if fx.rec.count() != 0 {
		t.Fatal("a local failure was recorded as an agent contact")
	}

	d := fa.device(t)
	d.Agent.Scheme = "gopher"
	if res := fx.disp.Dispatch(context.Background(), fx.cmd, d); res.ErrorCode != ErrCodeDispatch {
		t.Fatalf("bad scheme: %+v", res)
	}
}

func TestAgentURL(t *testing.T) {
	cases := []struct {
		d    devices.Device
		want string
	}{
		{devices.Device{IPAddress: "192.168.1.20", Agent: &devices.AgentBinding{Port: 9750, Scheme: "http"}}, "http://192.168.1.20:9750/agent/v1/commands"},
		{devices.Device{IPAddress: "192.168.1.20", Hostname: "nas.lan", Agent: &devices.AgentBinding{Port: 9751, Scheme: "https"}}, "https://nas.lan:9751/agent/v1/commands"},
		{devices.Device{IPAddress: "fd00::1", Agent: &devices.AgentBinding{Port: 9750, Scheme: "http"}}, "http://[fd00::1]:9750/agent/v1/commands"},
	}
	for _, tc := range cases {
		got, err := agentURL(tc.d, protocol.CommandsPath)
		if err != nil || got != tc.want {
			t.Errorf("agentURL = %q, %v; want %q", got, err, tc.want)
		}
	}
	if _, err := agentURL(devices.Device{}, "/x"); err == nil {
		t.Error("no binding must fail")
	}
}

// The dispatcher plugs into the engine's interface.
var _ commands.Dispatcher = (*ShutdownDispatcher)(nil)
