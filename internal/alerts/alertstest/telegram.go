// Package alertstest provides an in-memory Telegram Bot API for tests: it records the messages that
// sendMessage receives and can be told to fail.
package alertstest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// Message is one sendMessage call.
type Message struct {
	ChatID string
	Text   string
}

// Telegram is a fake Bot API server. Only the one bot token it was created with is accepted.
type Telegram struct {
	srv   *httptest.Server
	token string

	mu       sync.Mutex
	msgs     []Message
	requests int
	// Fail, when set, answers every sendMessage with this status and Telegram's error body, so a
	// test can script an outage (500), a throttle (429) or a refusal (403).
	fail        int
	failMessage string
}

// New starts a fake accepting token. Close it with the test (t.Cleanup is registered).
func New(t testing.TB, token string) *Telegram {
	t.Helper()
	f := &Telegram{token: token}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

// URL is the base URL to give to alerts.TelegramFactory.
func (f *Telegram) URL() string { return f.srv.URL }

// FailWith makes sendMessage answer status with description from now on; status 0 stops failing.
func (f *Telegram) FailWith(status int, description string) {
	f.mu.Lock()
	f.fail, f.failMessage = status, description
	f.mu.Unlock()
}

// Messages returns the messages received so far.
func (f *Telegram) Messages() []Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Message(nil), f.msgs...)
}

// Requests counts every sendMessage call, failed ones included.
func (f *Telegram) Requests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests
}

// WaitFor blocks until at least n messages arrived (or 5 s passed) and returns them.
func (f *Telegram) WaitFor(t testing.TB, n int) []Message {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if m := f.Messages(); len(m) >= n || time.Now().After(deadline) {
			if len(m) < n {
				t.Fatalf("waited for %d telegram message(s), got %d: %+v", n, len(m), m)
			}
			return m
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Settle waits a moment and returns the messages, for tests asserting that nothing (more) arrives.
func (f *Telegram) Settle() []Message {
	time.Sleep(150 * time.Millisecond)
	return f.Messages()
}

func (f *Telegram) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	rest, ok := strings.CutPrefix(r.URL.Path, "/bot")
	token, method, _ := strings.Cut(rest, "/")
	if r.Method != http.MethodPost || !ok || method != "sendMessage" {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":404,"description":"Not Found"}`))
		return
	}
	f.mu.Lock()
	f.requests++
	fail, failMsg := f.fail, f.failMessage
	f.mu.Unlock()
	if token != f.token {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":401,"description":"Unauthorized"}`))
		return
	}
	if fail != 0 {
		w.WriteHeader(fail)
		b, _ := json.Marshal(map[string]any{"ok": false, "error_code": fail, "description": failMsg})
		_, _ = w.Write(b)
		return
	}
	var in struct {
		ChatID string `json:"chat_id"`
		Text   string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.ChatID == "" || in.Text == "" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: message text is empty"}`))
		return
	}
	f.mu.Lock()
	f.msgs = append(f.msgs, Message{ChatID: in.ChatID, Text: in.Text})
	f.mu.Unlock()
	_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
}
