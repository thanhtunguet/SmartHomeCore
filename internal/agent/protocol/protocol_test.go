package protocol

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

const agentID = "0193a0b2-7c4e-7f00-8000-000000000001"
const commandID = "0193a0b2-7c4e-7f00-8000-0000000000aa"

type fixture struct {
	priv  ed25519.PrivateKey
	now   time.Time
	v     *Verifier
	clock *time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	f := &fixture{priv: priv, now: now}
	f.clock = &f.now
	f.v, err = NewVerifier(VerifierConfig{
		AgentID:        agentID,
		TrustedKeys:    []ed25519.PublicKey{pub},
		AllowedActions: []Action{ActionShutdown},
		ClockSkew:      30 * time.Second,
		StartedAt:      now.Add(-time.Hour),
		Now:            func() time.Time { return *f.clock },
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) sign(t *testing.T, mutate func(*Command)) SignedRequest {
	t.Helper()
	cmd, err := NewCommand(commandID, agentID, ActionShutdown, f.now)
	if err != nil {
		t.Fatal(err)
	}
	if mutate != nil {
		mutate(&cmd)
	}
	// Bypass Sign's validation for negative cases by signing manually.
	body, _ := json.Marshal(cmd)
	keyID := KeyID(f.priv.Public().(ed25519.PublicKey))
	sig := ed25519.Sign(f.priv, signedMessage(keyID, body))
	return SignedRequest{Body: body, KeyID: keyID, Signature: b64url(sig)}
}

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func TestVerifyAcceptsValidCommand(t *testing.T) {
	f := newFixture(t)
	cmd, err := NewCommand(commandID, agentID, ActionShutdown, f.now)
	if err != nil {
		t.Fatal(err)
	}
	req, err := Sign(f.priv, cmd)
	if err != nil {
		t.Fatal(err)
	}
	got, verr := f.v.Verify(req.KeyID, req.Signature, req.Body)
	if verr != nil {
		t.Fatalf("unexpected rejection: %v", verr)
	}
	if got != cmd {
		t.Fatalf("decoded %+v, want %+v", got, cmd)
	}
}

func TestVerifyRejections(t *testing.T) {
	otherPub, otherPriv, _ := ed25519.GenerateKey(rand.Reader)

	tests := []struct {
		name   string
		build  func(t *testing.T, f *fixture) SignedRequest
		status int
		code   string
	}{
		{
			name: "unknown key",
			build: func(t *testing.T, f *fixture) SignedRequest {
				r := f.sign(t, nil)
				r.KeyID = KeyID(otherPub)
				return r
			},
			status: http.StatusUnauthorized, code: CodeUnauthorized,
		},
		{
			name: "signed by untrusted key under trusted key id",
			build: func(t *testing.T, f *fixture) SignedRequest {
				r := f.sign(t, nil)
				r.Signature = b64url(ed25519.Sign(otherPriv, signedMessage(r.KeyID, r.Body)))
				return r
			},
			status: http.StatusUnauthorized, code: CodeUnauthorized,
		},
		{
			name: "body tampered after signing",
			build: func(t *testing.T, f *fixture) SignedRequest {
				r := f.sign(t, nil)
				r.Body = []byte(string(r.Body[:len(r.Body)-1]) + " }")
				return r
			},
			status: http.StatusUnauthorized, code: CodeUnauthorized,
		},
		{
			name: "wrong agent",
			build: func(t *testing.T, f *fixture) SignedRequest {
				return f.sign(t, func(c *Command) { c.AgentID = "0193a0b2-7c4e-7f00-8000-000000000002" })
			},
			status: http.StatusForbidden, code: CodeWrongAgent,
		},
		{
			name: "action not allowed",
			build: func(t *testing.T, f *fixture) SignedRequest {
				return f.sign(t, func(c *Command) { c.Action = ActionReboot })
			},
			status: http.StatusForbidden, code: CodeActionNotAllowed,
		},
		{
			name: "unknown action",
			build: func(t *testing.T, f *fixture) SignedRequest {
				return f.sign(t, func(c *Command) { c.Action = "exec" })
			},
			status: http.StatusBadRequest, code: CodeBadRequest,
		},
		{
			name: "too old",
			build: func(t *testing.T, f *fixture) SignedRequest {
				return f.sign(t, func(c *Command) { c.IssuedAt -= 31 })
			},
			status: http.StatusUnauthorized, code: CodeClockSkew,
		},
		{
			name: "from the future",
			build: func(t *testing.T, f *fixture) SignedRequest {
				return f.sign(t, func(c *Command) { c.IssuedAt += 31 })
			},
			status: http.StatusUnauthorized, code: CodeClockSkew,
		},
		{
			name: "issued before agent start",
			build: func(t *testing.T, f *fixture) SignedRequest {
				f.v.started = f.now.Unix() + 1
				return f.sign(t, nil)
			},
			status: http.StatusUnauthorized, code: CodeStale,
		},
		{
			name: "short nonce",
			build: func(t *testing.T, f *fixture) SignedRequest {
				return f.sign(t, func(c *Command) { c.Nonce = "abc" })
			},
			status: http.StatusBadRequest, code: CodeBadRequest,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			r := tt.build(t, f)
			_, verr := f.v.Verify(r.KeyID, r.Signature, r.Body)
			if verr == nil {
				t.Fatal("expected rejection")
			}
			if verr.Status != tt.status || verr.Code != tt.code {
				t.Fatalf("got %d/%s, want %d/%s", verr.Status, verr.Code, tt.status, tt.code)
			}
		})
	}
}

func TestVerifyRejectsUnknownJSONFields(t *testing.T) {
	f := newFixture(t)
	r := f.sign(t, nil)
	body := []byte(string(r.Body[:len(r.Body)-1]) + `,"exec":"rm -rf /"}`)
	sig := b64url(ed25519.Sign(f.priv, signedMessage(r.KeyID, body)))
	if _, verr := f.v.Verify(r.KeyID, sig, body); verr == nil || verr.Code != CodeBadRequest {
		t.Fatalf("expected bad_request, got %v", verr)
	}
}

func TestVerifyRejectsReplay(t *testing.T) {
	f := newFixture(t)
	r := f.sign(t, nil)
	if _, verr := f.v.Verify(r.KeyID, r.Signature, r.Body); verr != nil {
		t.Fatalf("first: %v", verr)
	}
	f.now = f.now.Add(10 * time.Second)
	_, verr := f.v.Verify(r.KeyID, r.Signature, r.Body)
	if verr == nil || verr.Code != CodeReplay {
		t.Fatalf("replay must be rejected, got %v", verr)
	}
	// After the nonce expires the request is far outside the freshness window anyway.
	f.now = f.now.Add(2 * time.Minute)
	_, verr = f.v.Verify(r.KeyID, r.Signature, r.Body)
	if verr == nil || verr.Code != CodeClockSkew {
		t.Fatalf("old replay must fail freshness, got %v", verr)
	}
}

func TestExpiringSetFailsClosedWhenFull(t *testing.T) {
	now := time.Unix(0, 0)
	s := NewExpiringSet(2)
	if s.Add("a", now.Add(time.Minute), now) != AddOK || s.Add("b", now.Add(time.Minute), now) != AddOK {
		t.Fatal("setup")
	}
	if got := s.Add("c", now.Add(time.Minute), now); got != AddFull {
		t.Fatalf("got %v, want AddFull", got)
	}
	later := now.Add(2 * time.Minute)
	if got := s.Add("c", later.Add(time.Minute), later); got != AddOK {
		t.Fatalf("expired entries must be evicted, got %v", got)
	}
}
