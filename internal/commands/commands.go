// Package commands models asynchronous device commands (wake, shutdown) and their lifecycle
// (docs/design/architecture.md §8.2, ADR-003, ADR-012).
//
// Files: commands.go (types and the state machine), repo.go / repo_sqlite.go (persistence; the
// commands_one_active unique index enforces dedup), engine.go (request flow, completion by monitor
// observations, deadline sweep, restart recovery), wake.go (the Wake-on-LAN Dispatcher). Phase 3
// registers a second Dispatcher for shutdown; the engine is already type-generic.
package commands

import (
	"fmt"
	"slices"
	"time"
)

// Type is the kind of command.
type Type string

const (
	TypeWake     Type = "wake"
	TypeShutdown Type = "shutdown"
)

// Status is the lifecycle state of a command.
type Status string

const (
	StatusPending      Status = "pending"
	StatusSent         Status = "sent"
	StatusAcknowledged Status = "acknowledged"
	StatusCompleted    Status = "completed"
	StatusFailed       Status = "failed"
	StatusTimedOut     Status = "timed_out"
)

// Terminal reports whether no further transitions are possible.
func (s Status) Terminal() bool {
	return s == StatusCompleted || s == StatusFailed || s == StatusTimedOut
}

var transitions = map[Type]map[Status][]Status{
	TypeWake: {
		StatusPending: {StatusSent, StatusFailed},
		StatusSent:    {StatusCompleted, StatusFailed, StatusTimedOut},
	},
	TypeShutdown: {
		StatusPending:      {StatusSent, StatusFailed},
		StatusSent:         {StatusAcknowledged, StatusFailed, StatusTimedOut},
		StatusAcknowledged: {StatusCompleted, StatusTimedOut},
	},
}

// CanTransition reports whether a command of type t may move from one status to another.
func CanTransition(t Type, from, to Status) bool {
	return slices.Contains(transitions[t][from], to)
}

// Transition validates a state change; an error here indicates a programming bug.
func Transition(t Type, from, to Status) error {
	if !CanTransition(t, from, to) {
		return fmt.Errorf("illegal %s command transition %s → %s", t, from, to)
	}
	return nil
}

// Command is one wake or shutdown request and its progress.
type Command struct {
	ID             string
	DeviceID       string
	Type           Type
	Status         Status
	RequestedBy    string // user id; empty when the user no longer exists
	ErrorCode      string // machine-readable, set when failed
	Error          string // human-readable, set when failed
	Attempts       int
	DeadlineAt     *time.Time
	SentAt         *time.Time
	AcknowledgedAt *time.Time
	CompletedAt    *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Active reports whether the command is still in flight.
func (c Command) Active() bool { return !c.Status.Terminal() }

// Valid reports whether t is a known command type.
func (t Type) Valid() bool { return t == TypeWake || t == TypeShutdown }

// Valid reports whether s is a known status.
func (s Status) Valid() bool {
	switch s {
	case StatusPending, StatusSent, StatusAcknowledged, StatusCompleted, StatusFailed, StatusTimedOut:
		return true
	}
	return false
}
