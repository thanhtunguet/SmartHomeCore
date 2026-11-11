// Package agent implements the Agent role: a small HTTP listener that accepts Ed25519-signed
// power commands from trusted Controllers (docs/design/agent-protocol.md) and executes an allowlisted
// action through platform.PowerController. It never runs shell commands supplied by a client.
package agent

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"sync"
	"time"

	"github.com/thanhtunguet/homecore/internal/agent/protocol"
	"github.com/thanhtunguet/homecore/internal/config"
	"github.com/thanhtunguet/homecore/internal/httpx"
	"github.com/thanhtunguet/homecore/internal/platform"
	"github.com/thanhtunguet/homecore/internal/version"
)

// Response is the success body of POST /agent/v1/commands.
type Response struct {
	CommandID string    `json:"command_id"`
	AgentID   string    `json:"agent_id"`
	Status    string    `json:"status"` // accepted | already_accepted | already_scheduled
	ExecuteAt time.Time `json:"execute_at"`
	AgentTime time.Time `json:"agent_time"`
}

// Server is the Agent HTTP listener.
type Server struct {
	cfg      config.Agent
	shutdown time.Duration
	log      *slog.Logger
	agentID  string
	verifier *protocol.Verifier
	power    platform.PowerController
	now      func() time.Time

	mu        sync.Mutex
	runCtx    context.Context
	accepted  map[string]time.Time // command_id -> execute_at
	scheduled *scheduledAction
}

type scheduledAction struct {
	commandID string
	executeAt time.Time
	cooldown  time.Time
}

// New builds an Agent server. power may be nil to use the platform default.
func New(cfg config.Agent, shutdownTimeout time.Duration, log *slog.Logger, power platform.PowerController) (*Server, error) {
	log = log.With("role", "agent")
	agentID, err := LoadOrCreateID(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	keys := make([]ed25519.PublicKey, 0, len(cfg.TrustedControllers))
	for i, tc := range cfg.TrustedControllers {
		k, err := protocol.ParsePublicKey(tc.PublicKey)
		if err != nil {
			return nil, fmt.Errorf("agent.trusted_controllers[%d] (%s): %w", i, tc.Name, err)
		}
		keys = append(keys, k)
		log.Info("trusting controller", "name", tc.Name, "key_id", protocol.KeyID(k))
	}
	if len(keys) == 0 {
		log.Warn("no trusted controllers configured: every command will be rejected")
	}
	actions := make([]protocol.Action, 0, len(cfg.AllowedActions))
	for _, a := range cfg.AllowedActions {
		actions = append(actions, protocol.Action(a))
	}
	v, err := protocol.NewVerifier(protocol.VerifierConfig{
		AgentID:        agentID,
		TrustedKeys:    keys,
		AllowedActions: actions,
		ClockSkew:      cfg.ClockSkew,
		StartedAt:      time.Now(),
	})
	if err != nil {
		return nil, err
	}
	if power == nil {
		power = platform.NewPowerController(cfg.DryRun, log)
	}
	log.Info("agent identity", "agent_id", agentID)
	return &Server{
		cfg: cfg, shutdown: shutdownTimeout, log: log, agentID: agentID,
		verifier: v, power: power, now: time.Now,
		accepted: make(map[string]time.Time),
	}, nil
}

// ID returns the persistent agent_id.
func (s *Server) ID() string { return s.agentID }

// Handler returns the Agent HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+protocol.HealthPath, s.handleHealth)
	mux.HandleFunc(protocol.CommandsPath, s.handleCommand)
	return mux
}

// Run serves until ctx is cancelled. Pending (delayed) actions are cancelled with ctx.
func (s *Server) Run(ctx context.Context) error {
	s.mu.Lock()
	s.runCtx = ctx
	s.mu.Unlock()
	srv := httpx.NewServer(s.cfg.Listen, s.Handler(), s.log)
	return httpx.Serve(ctx, s.log, srv, s.cfg.TLS.CertFile, s.cfg.TLS.KeyFile, s.shutdown)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]string{
		"status": "ok", "agent_id": s.agentID, "version": version.Version,
	})
}

type errorResponse struct {
	httpx.ErrorBody
	AgentTime time.Time `json:"agent_time"`
}

func (s *Server) writeError(w http.ResponseWriter, status int, code, msg string) {
	httpx.WriteJSON(w, status, errorResponse{
		ErrorBody: httpx.ErrorBody{Error: httpx.ErrorDetail{Code: code, Message: msg}},
		AgentTime: s.now().UTC(),
	})
}

func (s *Server) handleCommand(w http.ResponseWriter, r *http.Request) {
	// Step 1: transport checks.
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		s.writeError(w, http.StatusMethodNotAllowed, protocol.CodeBadRequest, "method not allowed")
		return
	}
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		s.writeError(w, http.StatusUnsupportedMediaType, protocol.CodeBadRequest, "content type must be application/json")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, protocol.MaxBodyBytes))
	if err != nil {
		s.writeError(w, http.StatusRequestEntityTooLarge, protocol.CodeBadRequest, "body too large")
		return
	}

	// Steps 2–9.
	cmd, verr := s.verifier.Verify(r.Header.Get(protocol.HeaderKeyID), r.Header.Get(protocol.HeaderSignature), body)
	if verr != nil {
		s.log.Warn("command rejected", "remote", r.RemoteAddr, "code", verr.Code, "reason", verr.Message)
		s.writeError(w, verr.Status, verr.Code, verr.Message)
		return
	}

	// Steps 10–12.
	resp, status := s.accept(cmd)
	s.log.Info("command processed", "remote", r.RemoteAddr, "command_id", cmd.CommandID,
		"action", cmd.Action, "status", resp.Status, "execute_at", resp.ExecuteAt)
	httpx.WriteJSON(w, status, resp)
}

// accept applies idempotency (step 10), cooldown (step 11) and schedules the action (step 12).
func (s *Server) accept(cmd protocol.Command) (Response, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	resp := Response{CommandID: cmd.CommandID, AgentID: s.agentID, AgentTime: now.UTC()}

	for id, at := range s.accepted { // tiny map; prune entries older than 10 minutes
		if now.Sub(at) > 10*time.Minute {
			delete(s.accepted, id)
		}
	}
	if at, ok := s.accepted[cmd.CommandID]; ok {
		resp.Status, resp.ExecuteAt = "already_accepted", at.UTC()
		return resp, http.StatusOK
	}
	if sa := s.scheduled; sa != nil && now.Before(sa.cooldown) {
		resp.Status, resp.ExecuteAt = "already_scheduled", sa.executeAt.UTC()
		return resp, http.StatusOK
	}

	executeAt := now.Add(s.cfg.ActionDelay)
	s.accepted[cmd.CommandID] = executeAt
	s.scheduled = &scheduledAction{
		commandID: cmd.CommandID,
		executeAt: executeAt,
		cooldown:  executeAt.Add(s.cfg.Cooldown),
	}
	ctx := s.runCtx
	if ctx == nil {
		ctx = context.Background()
	}
	go s.execute(ctx, cmd, s.cfg.ActionDelay)

	resp.Status, resp.ExecuteAt = "accepted", executeAt.UTC()
	return resp, http.StatusAccepted
}

// execute runs after the HTTP response has been sent, so the Controller receives the
// acknowledgement before the network goes down (brief §6).
func (s *Server) execute(ctx context.Context, cmd protocol.Command, delay time.Duration) {
	t := time.NewTimer(delay)
	defer t.Stop()
	select {
	case <-ctx.Done():
		s.log.Warn("agent stopping; scheduled action cancelled", "command_id", cmd.CommandID)
		return
	case <-t.C:
	}
	var err error
	switch cmd.Action {
	case protocol.ActionShutdown:
		err = s.power.PowerOff(context.WithoutCancel(ctx))
	case protocol.ActionReboot:
		err = s.power.Reboot(context.WithoutCancel(ctx))
	}
	if err != nil {
		s.log.Error("power action failed", "command_id", cmd.CommandID, "action", cmd.Action, "err", err)
	}
}
