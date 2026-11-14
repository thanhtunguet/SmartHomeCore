package agentctl

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/thanhtunguet/homecore/internal/agent/protocol"
	"github.com/thanhtunguet/homecore/internal/commands"
	"github.com/thanhtunguet/homecore/internal/devices"
)

// Signer signs agent commands (implemented by *controllerkey.Key).
type Signer interface {
	Sign(cmd protocol.Command) (protocol.SignedRequest, error)
}

// ContactRecorder stores what the Controller learned about an agent (implemented by
// *devices.SQLiteRepository).
type ContactRecorder interface {
	RecordAgentContact(ctx context.Context, deviceID, agentID string, c devices.AgentContact) error
}

// ShutdownDispatcher is the commands.Dispatcher of `shutdown` commands. Per agent-protocol §5 and
// ADR-012 it makes one signed POST with a short timeout and at most one immediate retry, with the
// same command_id and a new nonce and issued_at, when the first attempt hit a network error or a
// 5xx. It never loops or backs off: the user can press the button again.
//
// Outcomes: 200/202 from the right agent → acknowledged; an agent error (4xx) → failed with the
// agent's error.code; a network problem → failed with agent_unreachable / agent_timeout; anything
// unexpected → agent_bad_response / agent_error / dispatch_error.
type ShutdownDispatcher struct {
	Signer Signer          // required
	Status ContactRecorder // optional: records AgentBinding.status on every contact
	HTTP   *http.Client    // default NewHTTPClient()
	// Timeout of one attempt (default DefaultTimeout).
	Timeout time.Duration
	Now     func() time.Time // default time.Now
	Log     *slog.Logger     // default slog.Default()
}

var _ commands.Dispatcher = (*ShutdownDispatcher)(nil)

const maxAttempts = 2 // the first try plus one immediate retry

// Precheck requires an agent binding.
func (*ShutdownDispatcher) Precheck(d devices.Device) error {
	if d.Agent == nil {
		return &commands.PreconditionError{Message: "the device has no agent binding; bind a HomeCore agent to enable remote shutdown"}
	}
	return nil
}

func (s *ShutdownDispatcher) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *ShutdownDispatcher) timeout() time.Duration {
	if s.Timeout > 0 {
		return s.Timeout
	}
	return DefaultTimeout
}

// agentReply is the body of a protocol response (success or error).
type agentReply struct {
	CommandID string    `json:"command_id"`
	AgentID   string    `json:"agent_id"`
	Status    string    `json:"status"`
	AgentTime time.Time `json:"agent_time"`
	Error     *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Dispatch implements commands.Dispatcher.
func (s *ShutdownDispatcher) Dispatch(ctx context.Context, c commands.Command, d devices.Device) commands.Result {
	log := s.Log
	if log == nil {
		log = slog.Default()
	}
	hc := s.HTTP
	if hc == nil {
		hc = NewHTTPClient()
	}
	fail := func(attempts int, code, msg string) commands.Result {
		return commands.Result{Status: commands.StatusFailed, ErrorCode: code, Error: msg, Attempts: attempts}
	}
	target, err := agentURL(d, protocol.CommandsPath)
	if err != nil {
		return fail(1, ErrCodeDispatch, err.Error())
	}

	var (
		last reply
		sent protocol.SignedRequest
	)
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		// A fresh nonce and issued_at on every attempt; the command_id never changes (step 10 of
		// the verification makes the retry idempotent on the agent).
		cmd, err := protocol.NewCommand(c.ID, d.Agent.AgentID, protocol.ActionShutdown, s.now())
		if err != nil {
			return fail(attempt, ErrCodeDispatch, "cannot build the agent command: "+err.Error())
		}
		if sent, err = s.Signer.Sign(cmd); err != nil {
			return fail(attempt, ErrCodeDispatch, "cannot sign the agent command: "+err.Error())
		}
		req, err := http.NewRequest(http.MethodPost, target, bytes.NewReader(sent.Body))
		if err != nil {
			return fail(attempt, ErrCodeDispatch, "cannot build the agent request: "+err.Error())
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(protocol.HeaderKeyID, sent.KeyID)
		req.Header.Set(protocol.HeaderSignature, sent.Signature)

		last = do(ctx, hc, s.timeout(), req)
		if !last.transient() || attempt == maxAttempts || ctx.Err() != nil {
			res := s.interpret(last, c, d, sent.KeyID, attempt)
			s.record(ctx, d, res.Status == commands.StatusAcknowledged || agentRefused(last), log)
			if res.Status == commands.StatusFailed {
				log.Warn("agent rejected or missed the shutdown", "command", c.ID, "device", d.ID,
					"code", res.ErrorCode, "attempts", res.Attempts)
			}
			return res
		}
		log.Debug("retrying the agent request", "command", c.ID, "device", d.ID, "attempt", attempt)
	}
	return fail(maxAttempts, ErrCodeDispatch, "unreachable") // the loop always returns
}

// agentRefused reports whether the reply is a protocol error from a live, correct agent (4xx with
// a known code other than wrong_agent): the agent is "ready" even though it refused this command.
func agentRefused(r reply) bool {
	if r.networkFailure() || r.Status >= 500 {
		return false
	}
	var body agentReply
	if json.Unmarshal(r.Body, &body) != nil || body.Error == nil {
		return false
	}
	return agentCodes[body.Error.Code] && body.Error.Code != protocol.CodeWrongAgent
}

func (s *ShutdownDispatcher) record(ctx context.Context, d devices.Device, ok bool, log *slog.Logger) {
	if s.Status == nil {
		return
	}
	err := s.Status.RecordAgentContact(ctx, d.ID, d.Agent.AgentID, devices.AgentContact{OK: ok, At: s.now().UTC()})
	if err != nil {
		log.Warn("recording agent status failed", "device", d.ID, "err", err)
	}
}

// interpret maps the final reply to a command result.
func (s *ShutdownDispatcher) interpret(r reply, c commands.Command, d devices.Device, keyID string, attempts int) commands.Result {
	fail := func(code, msg string) commands.Result {
		return commands.Result{Status: commands.StatusFailed, ErrorCode: code, Error: msg, Attempts: attempts}
	}
	if r.networkFailure() {
		code, msg := classify(r.Err, d, s.timeout())
		return fail(code, msg)
	}
	var body agentReply
	jsonErr := json.Unmarshal(r.Body, &body)

	switch {
	case r.Status == http.StatusAccepted || r.Status == http.StatusOK:
		okStatus := body.Status == "accepted" || body.Status == "already_accepted" || body.Status == "already_scheduled"
		if jsonErr != nil || !okStatus || !strings.EqualFold(body.AgentID, d.Agent.AgentID) || !strings.EqualFold(body.CommandID, c.ID) {
			return fail(ErrCodeBadResponse, fmt.Sprintf("the agent at %s answered HTTP %d with an unexpected body", hostPort(d), r.Status))
		}
		// accepted, or already accepted / already scheduled (REQ-AG-008): the power action is on its way.
		return commands.Result{Status: commands.StatusAcknowledged, Attempts: attempts}

	case jsonErr != nil || body.Error == nil:
		if r.Status >= 500 {
			return fail(ErrCodeAgentError, fmt.Sprintf("the agent at %s answered HTTP %d", hostPort(d), r.Status))
		}
		return fail(ErrCodeBadResponse, fmt.Sprintf("the service at %s answered HTTP %d and is not a HomeCore agent (wrong port?)", hostPort(d), r.Status))
	}

	code := body.Error.Code
	text := clean(body.Error.Message, 200)
	if !agentCodes[code] {
		return fail(ErrCodeAgentError, fmt.Sprintf("the agent at %s answered HTTP %d with an unknown error code %q", hostPort(d), r.Status, clean(code, 40)))
	}
	return fail(code, s.describe(code, text, body, r.Status, keyID))
}

// describe writes the human text of an agent error. Several codes get the hint an operator needs.
func (s *ShutdownDispatcher) describe(code, text string, body agentReply, status int, keyID string) string {
	base := fmt.Sprintf("the agent rejected the command (HTTP %d, %s)", status, code)
	if text != "" {
		base += ": " + text
	}
	switch code {
	case protocol.CodeUnauthorized:
		return base + "; check that this Controller's public key (key_id " + keyID + ") is in the agent's trusted_controllers"
	case protocol.CodeClockSkew:
		if !body.AgentTime.IsZero() {
			drift := body.AgentTime.Sub(s.now()).Round(time.Second)
			return fmt.Sprintf("%s; agent_time %s, drift about %s from the Controller clock: fix the clock of the device or raise agent.clock_skew",
				base, body.AgentTime.UTC().Format(time.RFC3339), drift)
		}
		return base + "; fix the clock of the device or raise agent.clock_skew"
	case protocol.CodeWrongAgent:
		return base + "; the agent_id of the binding does not match the agent answering at that address"
	case protocol.CodeActionNotAllowed:
		return base + "; add shutdown to agent.allowed_actions on the device"
	case protocol.CodeStale:
		return base + "; the agent restarted moments ago, try again"
	}
	return base
}
