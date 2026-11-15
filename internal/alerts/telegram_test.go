package alerts_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/thanhtunguet/homecore/internal/alerts"
	"github.com/thanhtunguet/homecore/internal/alerts/alertstest"
)

const (
	botToken = "123456789:AAH-test_token_0123456789abcdefghij"
	target   = "424242"
)

func newTelegram(t *testing.T) (alerts.Adapter, *alertstest.Telegram) {
	t.Helper()
	fake := alertstest.New(t, botToken)
	a, err := alerts.TelegramFactory(fake.URL(), nil)(map[string]string{"target_user_id": target}, botToken)
	if err != nil {
		t.Fatal(err)
	}
	return a, fake
}

// REQ-ALERT-005: the adapter takes exactly two parameters, a bot token and a target user id.
func TestTelegramFactoryValidation(t *testing.T) {
	f := alerts.TelegramFactory("", nil)
	good := map[string]string{"target_user_id": target}
	cases := []struct {
		name     string
		settings map[string]string
		secret   string
		wantErr  string // "" = accepted
	}{
		{"valid user", good, botToken, ""},
		{"valid group id", map[string]string{"target_user_id": "-1001234567890"}, botToken, ""},
		{"surrounding spaces", map[string]string{"target_user_id": " 42 "}, botToken, ""},
		{"no target", map[string]string{}, botToken, "target_user_id"},
		{"nil settings", nil, botToken, "target_user_id"},
		{"target is a name", map[string]string{"target_user_id": "@alice"}, botToken, "numeric"},
		{"target with text", map[string]string{"target_user_id": "12abc"}, botToken, "numeric"},
		{"unknown setting", map[string]string{"target_user_id": target, "chat": "1"}, botToken, "unknown setting"},
		{"no token", good, "", "bot token"},
		{"malformed token", good, "not-a-token", "bot token"},
		{"token without secret part", good, "123456789:", "bot token"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := f(c.settings, c.secret)
			switch {
			case c.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
				t.Fatalf("error %v, want it to mention %q", err, c.wantErr)
			}
			if err != nil && strings.Contains(err.Error(), c.secret) && c.secret != "" {
				t.Fatalf("the error echoes the secret: %v", err)
			}
		})
	}
}

func TestTelegramSendsPlainTextToTheTarget(t *testing.T) {
	a, fake := newTelegram(t)
	if err := a.Send(context.Background(), alerts.Message{Title: "🔴 NAS went offline", Body: "192.168.1.5:22"}); err != nil {
		t.Fatal(err)
	}
	got := fake.Messages()
	if len(got) != 1 || got[0].ChatID != target || got[0].Text != "🔴 NAS went offline\n192.168.1.5:22" {
		t.Fatalf("messages: %+v", got)
	}
}

func TestTelegramTruncatesLongText(t *testing.T) {
	a, fake := newTelegram(t)
	if err := a.Send(context.Background(), alerts.Message{Title: "t", Body: strings.Repeat("é", 5000)}); err != nil {
		t.Fatal(err)
	}
	text := fake.Messages()[0].Text
	if n := len([]rune(text)); n != 4096 || !strings.HasSuffix(text, "…") {
		t.Fatalf("text has %d characters, ends %q", n, text[len(text)-4:])
	}
}

// Telegram's own refusal is the error, and a refusal is final; a throttle or an outage is worth a retry.
func TestTelegramErrors(t *testing.T) {
	a, fake := newTelegram(t)
	cases := []struct {
		status    int
		desc      string
		temporary bool
	}{
		{403, "Forbidden: bot can't initiate conversation with a user", false},
		{400, "Bad Request: chat not found", false},
		{429, "Too Many Requests: retry after 3", true},
		{502, "Bad Gateway", true},
	}
	for _, c := range cases {
		fake.FailWith(c.status, c.desc)
		err := a.Send(context.Background(), alerts.Message{Title: "x"})
		if err == nil || !strings.Contains(err.Error(), c.desc) || alerts.IsTemporary(err) != c.temporary {
			t.Errorf("status %d: err=%v temporary=%v, want %q temporary=%v", c.status, err, alerts.IsTemporary(err), c.desc, c.temporary)
		}
	}
}

// The bot token is part of the request URL; no error may carry it (REQ-SEC-007).
func TestTelegramErrorsNeverContainTheToken(t *testing.T) {
	// A server that is gone: the transport error names the URL.
	dead := httptest.NewServer(http.NotFoundHandler())
	url := dead.URL
	dead.Close()
	a, err := alerts.TelegramFactory(url, nil)(map[string]string{"target_user_id": target}, botToken)
	if err != nil {
		t.Fatal(err)
	}
	err = a.Send(context.Background(), alerts.Message{Title: "x"})
	if err == nil || strings.Contains(err.Error(), botToken) || !alerts.IsTemporary(err) {
		t.Fatalf("transport error: %v (temporary=%v)", err, alerts.IsTemporary(err))
	}

	// A server that echoes the token in its description.
	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"ok":false,"description":"bad token ` + botToken + `"}`))
	}))
	t.Cleanup(echo.Close)
	a, _ = alerts.TelegramFactory(echo.URL, nil)(map[string]string{"target_user_id": target}, botToken)
	if err = a.Send(context.Background(), alerts.Message{Title: "x"}); err == nil || strings.Contains(err.Error(), botToken) {
		t.Fatalf("echoed token: %v", err)
	}
}

func TestTelegramHonoursContext(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body) // the server notices a closed connection once the body is read
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	t.Cleanup(slow.Close)
	a, _ := alerts.TelegramFactory(slow.URL, nil)(map[string]string{"target_user_id": target}, botToken)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := a.Send(ctx, alerts.Message{Title: "x"}); err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("err=%v after %v", err, time.Since(start))
	}
}
