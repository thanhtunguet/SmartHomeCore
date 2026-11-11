package agent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thanhtunguet/homecore/internal/agent/protocol"
	"github.com/thanhtunguet/homecore/internal/config"
)

type countingPower struct{ off atomic.Int32 }

func (p *countingPower) PowerOff(context.Context) error { p.off.Add(1); return nil }
func (p *countingPower) Reboot(context.Context) error   { return nil }

func newTestServer(t *testing.T) (*Server, ed25519.PrivateKey, *countingPower) {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	cfg := config.Default().Agent
	cfg.DataDir = t.TempDir()
	cfg.ActionDelay = 10 * time.Millisecond
	cfg.TrustedControllers = []config.TrustedController{{Name: "test", PublicKey: protocol.EncodePublicKey(pub)}}
	power := &countingPower{}
	s, err := New(cfg, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)), power)
	if err != nil {
		t.Fatal(err)
	}
	return s, priv, power
}

func post(t *testing.T, h http.Handler, req protocol.SignedRequest) (*httptest.ResponseRecorder, Response) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, protocol.CommandsPath, bytes.NewReader(req.Body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set(protocol.HeaderKeyID, req.KeyID)
	r.Header.Set(protocol.HeaderSignature, req.Signature)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	var resp Response
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	return rec, resp
}

func TestShutdownIsAcknowledgedThenExecutedOnceAndIdempotent(t *testing.T) {
	s, priv, power := newTestServer(t)
	h := s.Handler()

	cmdID := uuid.NewString()
	sign := func() protocol.SignedRequest {
		cmd, err := protocol.NewCommand(cmdID, s.ID(), protocol.ActionShutdown, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		req, err := protocol.Sign(priv, cmd)
		if err != nil {
			t.Fatal(err)
		}
		return req
	}

	rec, resp := post(t, h, sign())
	if rec.Code != http.StatusAccepted || resp.Status != "accepted" {
		t.Fatalf("first: %d %s", rec.Code, rec.Body)
	}
	// Retry with the same command_id and a fresh nonce → idempotent.
	rec, resp = post(t, h, sign())
	if rec.Code != http.StatusOK || resp.Status != "already_accepted" {
		t.Fatalf("retry: %d %s", rec.Code, rec.Body)
	}
	// Different command during cooldown → already_scheduled.
	cmdID = uuid.NewString()
	rec, resp = post(t, h, sign())
	if rec.Code != http.StatusOK || resp.Status != "already_scheduled" {
		t.Fatalf("cooldown: %d %s", rec.Code, rec.Body)
	}

	deadline := time.Now().Add(2 * time.Second)
	for power.off.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := power.off.Load(); got != 1 {
		t.Fatalf("power off executed %d times, want 1", got)
	}
}

func TestUnsignedRequestRejected(t *testing.T) {
	s, _, power := newTestServer(t)
	rec, _ := post(t, s.Handler(), protocol.SignedRequest{Body: []byte(`{"action":"shutdown"}`)})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", rec.Code)
	}
	if power.off.Load() != 0 {
		t.Fatal("must not execute")
	}
}
