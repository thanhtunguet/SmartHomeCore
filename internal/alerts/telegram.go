package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Telegram adapter settings and limits.
const (
	// TelegramTargetKey is the one setting of the adapter: the id of the Telegram user (or group)
	// that receives the messages.
	TelegramTargetKey = "target_user_id"

	telegramAPI        = "https://api.telegram.org"
	telegramTimeout    = 10 * time.Second
	telegramMaxText    = 4096 // characters in one message
	telegramMaxReply   = 64 << 10
	telegramTruncation = "…"
)

var (
	// A bot token is "<bot id>:<secret>", for example 123456789:AAH-x_y... .
	botTokenRE = regexp.MustCompile(`^[0-9]{3,}:[A-Za-z0-9_-]{20,}$`)
	// A user id is a positive number; a group or channel id is negative.
	targetIDRE = regexp.MustCompile(`^-?[0-9]{1,20}$`)
)

// TelegramFactory returns the Factory of the Telegram adapter: it sends plain-text messages with the
// Bot API method sendMessage.
//
// settings: target_user_id (required, a number). secret: the bot token from BotFather. The target
// must have started a chat with the bot (a bot cannot message a user first); Telegram's refusal is
// returned as the delivery error.
//
// baseURL is the Bot API server ("" = https://api.telegram.org) and client the HTTP client ("" =
// a client with a 10 s timeout that follows no redirects); both exist for tests.
func TelegramFactory(baseURL string, client *http.Client) Factory {
	if baseURL == "" {
		baseURL = telegramAPI
	}
	baseURL = strings.TrimRight(baseURL, "/")
	if client == nil {
		client = &http.Client{
			Timeout:       telegramTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	return func(settings map[string]string, secret string) (Adapter, error) {
		for k := range settings {
			if k != TelegramTargetKey {
				return nil, fmt.Errorf("telegram: unknown setting %q (only %s exists)", k, TelegramTargetKey)
			}
		}
		target := strings.TrimSpace(settings[TelegramTargetKey])
		if !targetIDRE.MatchString(target) {
			return nil, fmt.Errorf("telegram: settings.%s must be the numeric id of the Telegram user or group", TelegramTargetKey)
		}
		if !botTokenRE.MatchString(secret) {
			return nil, errors.New("telegram: secret must be the bot token from BotFather (<bot id>:<secret>)")
		}
		return &telegram{base: baseURL, token: secret, target: target, client: client}, nil
	}
}

type telegram struct {
	base, token, target string
	client              *http.Client
}

// Send implements Adapter.
func (t *telegram) Send(ctx context.Context, m Message) error {
	text := m.Title
	if m.Body != "" {
		text += "\n" + m.Body
	}
	body, err := json.Marshal(map[string]any{
		"chat_id":                  t.target,
		"text":                     truncateText(text, telegramMaxText),
		"disable_web_page_preview": true,
	})
	if err != nil {
		return err
	}
	// The token is part of the URL (that is how the Bot API works), so no error from here on may be
	// returned as is: http.Client errors carry the URL.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.base+"/bot"+t.token+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		return errors.New("telegram: cannot build the request")
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		if ue, ok := errors.AsType[*url.Error](err); ok {
			err = ue.Err // drop the URL
		}
		return Temporary(fmt.Errorf("telegram: request failed: %s", t.redact(err.Error())))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, telegramMaxReply))

	var reply struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	_ = json.Unmarshal(raw, &reply)
	if resp.StatusCode == http.StatusOK && reply.OK {
		return nil
	}
	msg := fmt.Sprintf("telegram: %d", resp.StatusCode)
	if reply.Description != "" {
		msg += " " + t.redact(reply.Description)
	}
	e := errors.New(msg)
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return Temporary(e)
	}
	return e
}

func (t *telegram) redact(s string) string { return strings.ReplaceAll(s, t.token, "***") }

// truncateText cuts s to at most max characters, ending with an ellipsis when it was cut.
func truncateText(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max-1]) + telegramTruncation
}
