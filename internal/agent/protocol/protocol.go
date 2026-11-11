// Package protocol implements HomeCore Agent Protocol v1 (docs/agent-protocol.md): the wire
// format of Controller → Agent commands, Ed25519 signing and the Agent-side verification
// pipeline including replay protection. It depends only on the standard library and is shared by
// the Controller (signing client) and the Agent (verifier).
package protocol

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sync"
	"time"
)

// Wire constants.
const (
	Version         = 1
	SignatureDomain = "homecore-agent-cmd-v1"
	HeaderKeyID     = "X-HomeCore-Key-Id"
	HeaderSignature = "X-HomeCore-Signature"
	CommandsPath    = "/agent/v1/commands"
	HealthPath      = "/agent/v1/healthz"
	MaxBodyBytes    = 4 << 10
	minNonceBytes   = 16
)

// Action is a power action an Agent may execute. There is intentionally no generic "exec".
type Action string

const (
	ActionShutdown Action = "shutdown"
	ActionReboot   Action = "reboot"
)

// Valid reports whether a is a known action.
func (a Action) Valid() bool { return a == ActionShutdown || a == ActionReboot }

// Command is the signed request body.
type Command struct {
	V         int    `json:"v"`
	CommandID string `json:"command_id"`
	AgentID   string `json:"agent_id"`
	Action    Action `json:"action"`
	IssuedAt  int64  `json:"issued_at"`
	Nonce     string `json:"nonce"`
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func (c Command) validate() error {
	switch {
	case c.V != Version:
		return fmt.Errorf("unsupported version %d", c.V)
	case !uuidRe.MatchString(c.CommandID):
		return errors.New("command_id must be a UUID")
	case !uuidRe.MatchString(c.AgentID):
		return errors.New("agent_id must be a UUID")
	case !c.Action.Valid():
		return fmt.Errorf("unknown action %q", c.Action)
	case c.IssuedAt <= 0:
		return errors.New("issued_at required")
	}
	n, err := base64.RawURLEncoding.DecodeString(c.Nonce)
	if err != nil || len(n) < minNonceBytes {
		return fmt.Errorf("nonce must be base64url of >= %d bytes", minNonceBytes)
	}
	return nil
}

// KeyID derives the key identifier: hex of the first 8 bytes of SHA-256(public key).
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

// ParsePublicKey decodes a standard-base64 Ed25519 public key.
func ParsePublicKey(b64 string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, errors.New("public key must be standard base64 of 32 bytes")
	}
	return ed25519.PublicKey(b), nil
}

// EncodePublicKey encodes a public key for configuration files and the API.
func EncodePublicKey(pub ed25519.PublicKey) string { return base64.StdEncoding.EncodeToString(pub) }

// NewCommand builds a command with a fresh random nonce and issued_at = now.
func NewCommand(commandID, agentID string, action Action, now time.Time) (Command, error) {
	nonce := make([]byte, minNonceBytes)
	if _, err := rand.Read(nonce); err != nil {
		return Command{}, err
	}
	c := Command{
		V: Version, CommandID: commandID, AgentID: agentID, Action: action,
		IssuedAt: now.Unix(), Nonce: base64.RawURLEncoding.EncodeToString(nonce),
	}
	return c, c.validate()
}

func signedMessage(keyID string, body []byte) []byte {
	var b bytes.Buffer
	b.Grow(len(SignatureDomain) + len(keyID) + len(body) + 2)
	b.WriteString(SignatureDomain)
	b.WriteByte('\n')
	b.WriteString(keyID)
	b.WriteByte('\n')
	b.Write(body)
	return b.Bytes()
}

// SignedRequest is what the Controller sends: body plus the two headers.
type SignedRequest struct {
	Body      []byte
	KeyID     string
	Signature string // base64url, no padding
}

// Sign serialises and signs cmd with the Controller private key.
func Sign(priv ed25519.PrivateKey, cmd Command) (SignedRequest, error) {
	if err := cmd.validate(); err != nil {
		return SignedRequest{}, err
	}
	body, err := json.Marshal(cmd)
	if err != nil {
		return SignedRequest{}, err
	}
	keyID := KeyID(priv.Public().(ed25519.PublicKey))
	sig := ed25519.Sign(priv, signedMessage(keyID, body))
	return SignedRequest{Body: body, KeyID: keyID, Signature: base64.RawURLEncoding.EncodeToString(sig)}, nil
}

// Error codes returned by the Agent (docs/agent-protocol.md §3).
const (
	CodeUnauthorized     = "unauthorized"
	CodeBadRequest       = "bad_request"
	CodeWrongAgent       = "wrong_agent"
	CodeActionNotAllowed = "action_not_allowed"
	CodeClockSkew        = "clock_skew"
	CodeStale            = "stale"
	CodeReplay           = "replay"
	CodeBusy             = "busy"
)

// VerifyError is a rejected command with its HTTP status and protocol error code.
type VerifyError struct {
	Status  int
	Code    string
	Message string
}

func (e *VerifyError) Error() string { return e.Code + ": " + e.Message }

func reject(status int, code, msg string) *VerifyError {
	return &VerifyError{Status: status, Code: code, Message: msg}
}

// unauthorized is deliberately identical for unknown key and bad signature.
var errUnauthorized = reject(http.StatusUnauthorized, CodeUnauthorized, "authentication failed")

// VerifierConfig configures a Verifier.
type VerifierConfig struct {
	AgentID        string
	TrustedKeys    []ed25519.PublicKey
	AllowedActions []Action
	ClockSkew      time.Duration
	StartedAt      time.Time        // agent process start; commands issued earlier are rejected
	Now            func() time.Time // optional, for tests
	MaxNonces      int              // optional, default 10 000
}

// Verifier runs steps 2–9 of the verification pipeline. Steps 10–11 (command idempotency and
// cooldown) belong to the executor. Safe for concurrent use.
type Verifier struct {
	agentID string
	keys    map[string]ed25519.PublicKey
	allowed map[Action]bool
	skew    time.Duration
	started int64
	now     func() time.Time
	nonces  *ExpiringSet
}

// NewVerifier validates cfg and builds a Verifier.
func NewVerifier(cfg VerifierConfig) (*Verifier, error) {
	if !uuidRe.MatchString(cfg.AgentID) {
		return nil, errors.New("agent id must be a UUID")
	}
	if cfg.ClockSkew <= 0 {
		return nil, errors.New("clock skew must be > 0")
	}
	v := &Verifier{
		agentID: cfg.AgentID,
		keys:    make(map[string]ed25519.PublicKey, len(cfg.TrustedKeys)),
		allowed: make(map[Action]bool, len(cfg.AllowedActions)),
		skew:    cfg.ClockSkew,
		started: cfg.StartedAt.Unix(),
		now:     cfg.Now,
	}
	if v.now == nil {
		v.now = time.Now
	}
	for _, k := range cfg.TrustedKeys {
		if len(k) != ed25519.PublicKeySize {
			return nil, errors.New("invalid trusted key size")
		}
		v.keys[KeyID(k)] = k
	}
	for _, a := range cfg.AllowedActions {
		if !a.Valid() {
			return nil, fmt.Errorf("invalid allowed action %q", a)
		}
		v.allowed[a] = true
	}
	maxNonces := cfg.MaxNonces
	if maxNonces <= 0 {
		maxNonces = 10_000
	}
	v.nonces = NewExpiringSet(maxNonces)
	return v, nil
}

// Verify authenticates and validates one request. On success the nonce is consumed.
func (v *Verifier) Verify(keyID, signature string, body []byte) (Command, *VerifyError) {
	if len(body) > MaxBodyBytes {
		return Command{}, reject(http.StatusRequestEntityTooLarge, CodeBadRequest, "body too large")
	}
	// Steps 2–3: authenticate before parsing anything.
	pub, ok := v.keys[keyID]
	if !ok {
		return Command{}, errUnauthorized
	}
	sig, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil || len(sig) != ed25519.SignatureSize || !ed25519.Verify(pub, signedMessage(keyID, body), sig) {
		return Command{}, errUnauthorized
	}

	// Step 4: strict parse.
	var cmd Command
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cmd); err != nil {
		return Command{}, reject(http.StatusBadRequest, CodeBadRequest, "malformed body")
	}
	if dec.More() {
		return Command{}, reject(http.StatusBadRequest, CodeBadRequest, "trailing data")
	}
	if err := cmd.validate(); err != nil {
		return Command{}, reject(http.StatusBadRequest, CodeBadRequest, err.Error())
	}

	// Steps 5–6.
	if cmd.AgentID != v.agentID {
		return Command{}, reject(http.StatusForbidden, CodeWrongAgent, "command addressed to another agent")
	}
	if !v.allowed[cmd.Action] {
		return Command{}, reject(http.StatusForbidden, CodeActionNotAllowed, "action not allowed on this agent")
	}

	// Steps 7–8: freshness.
	now := v.now()
	if d := now.Unix() - cmd.IssuedAt; d > int64(v.skew/time.Second) || -d > int64(v.skew/time.Second) {
		return Command{}, reject(http.StatusUnauthorized, CodeClockSkew,
			fmt.Sprintf("issued_at differs from agent clock by %ds (max %s)", d, v.skew))
	}
	if cmd.IssuedAt < v.started {
		return Command{}, reject(http.StatusUnauthorized, CodeStale, "command issued before agent start")
	}

	// Step 9: nonce single use. Entries outlive the freshness window, so an expired nonce can
	// never be replayed successfully (it would fail step 7).
	switch v.nonces.Add(cmd.Nonce, now.Add(2*v.skew), now) {
	case AddDuplicate:
		return Command{}, reject(http.StatusConflict, CodeReplay, "nonce already used")
	case AddFull:
		return Command{}, reject(http.StatusServiceUnavailable, CodeBusy, "replay cache full")
	}
	return cmd, nil
}

// AddResult is the outcome of ExpiringSet.Add.
type AddResult int

const (
	AddOK AddResult = iota
	AddDuplicate
	AddFull
)

// ExpiringSet is a bounded set of strings with per-entry expiry. It fails closed when full.
type ExpiringSet struct {
	mu      sync.Mutex
	max     int
	entries map[string]time.Time
}

// NewExpiringSet creates a set holding at most max live entries.
func NewExpiringSet(max int) *ExpiringSet {
	return &ExpiringSet{max: max, entries: make(map[string]time.Time)}
}

// Add inserts key until expiresAt unless it is already present and unexpired.
func (s *ExpiringSet) Add(key string, expiresAt, now time.Time) AddResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	if exp, ok := s.entries[key]; ok && now.Before(exp) {
		return AddDuplicate
	}
	if len(s.entries) >= s.max {
		for k, exp := range s.entries {
			if !now.Before(exp) {
				delete(s.entries, k)
			}
		}
		if len(s.entries) >= s.max {
			return AddFull
		}
	}
	s.entries[key] = expiresAt
	return AddOK
}

// Contains reports whether key is present and unexpired.
func (s *ExpiringSet) Contains(key string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.entries[key]
	return ok && now.Before(exp)
}
