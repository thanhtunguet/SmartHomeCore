// Package alerts tells the owner about things that happen in the home: a new public IP, a wake or
// shutdown request, and a device that changed status without HomeCore's doing (REQ-ALERT-*, ADR-021).
//
// Files: alerts.go (types, adapter contract), telegram.go (the first adapter), repo.go /
// repo_sqlite.go (channel persistence), service.go (channel CRUD, test message, the delivery queue
// and its Run loop), hooks.go (the observers that connect ddns, commands and monitoring to the
// Service).
//
// A channel is one destination served by an adapter. Producers never wait for a delivery:
// Service.Notify only queues, and Service.Run delivers (with a few retries) and records the outcome
// on the channel.
package alerts

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Kind is a category of alert a channel can subscribe to.
type Kind string

const (
	// KindIPChanged: a DDNS record was moved to a new public IP address.
	KindIPChanged Kind = "ip_changed"
	// KindPowerCommand: a wake or shutdown request was accepted.
	KindPowerCommand Kind = "power_command"
	// KindStatusChanged: a monitored device went online or offline with no wake/shutdown command
	// in progress.
	KindStatusChanged Kind = "status_changed"
)

// AllKinds is every Kind, in documentation order. A channel created without `events` gets all of them.
func AllKinds() []Kind { return []Kind{KindIPChanged, KindPowerCommand, KindStatusChanged} }

// Valid reports whether k is a known Kind.
func (k Kind) Valid() bool {
	switch k {
	case KindIPChanged, KindPowerCommand, KindStatusChanged:
		return true
	}
	return false
}

// Message is what an adapter delivers: a one-line title and an optional multi-line body, both plain
// text. Adapters decide how to present them.
type Message struct {
	Kind  Kind
	Title string
	Body  string
}

// Adapter delivers messages to one destination. Implementations must be safe for concurrent use,
// must honour ctx, and must never put their secret in a returned error.
type Adapter interface {
	Send(ctx context.Context, m Message) error
}

// Factory builds the Adapter of one channel from its non-secret settings and its secret. It is also
// the validation of both: a returned error is reported to the user as a bad request, so its text must
// say what is wrong without echoing the secret.
type Factory func(settings map[string]string, secret string) (Adapter, error)

// Adapter names.
const TypeTelegram = "telegram"

// temporaryError marks a delivery failure worth retrying (network error, 429, 5xx).
type temporaryError struct{ err error }

func (e temporaryError) Error() string { return e.err.Error() }
func (e temporaryError) Unwrap() error { return e.err }

// Temporary marks err as worth retrying. Adapters use it for transient failures only.
func Temporary(err error) error {
	if err == nil {
		return nil
	}
	return temporaryError{err}
}

// IsTemporary reports whether err (or an error it wraps) was marked with Temporary.
func IsTemporary(err error) bool {
	var t temporaryError
	return errors.As(err, &t)
}

// Channel is one alert destination as the API shows it. The secret is never part of it.
type Channel struct {
	ID        string
	Name      string
	Type      string
	Enabled   bool
	Events    []Kind
	Settings  map[string]string
	HasSecret bool

	LastSentAt  *time.Time
	LastError   string
	LastErrorAt *time.Time

	CreatedAt time.Time
	UpdatedAt time.Time
}

// Subscribes reports whether the channel receives alerts of kind k.
func (c Channel) Subscribes(k Kind) bool {
	return slices.Contains(c.Events, k)
}

// Stored is a Channel together with its sealed secret, for the service only.
type Stored struct {
	Channel      Channel
	SecretSealed []byte
}

// Errors of the service.
var (
	// ErrNotFound: no channel has that id.
	ErrNotFound = errors.New("alert channel not found")
	// ErrNameTaken: another channel already has that name (compared case-insensitively).
	ErrNameTaken = errors.New("an alert channel with this name already exists")
)

// ValidationError lists what is wrong with a request (HTTP 400).
type ValidationError struct{ Problems []string }

func (e *ValidationError) Error() string {
	if len(e.Problems) == 0 {
		return "invalid alert channel"
	}
	return strings.Join(e.Problems, "; ")
}

func invalid(format string, a ...any) *ValidationError {
	return &ValidationError{Problems: []string{fmt.Sprintf(format, a...)}}
}
