// Package commands models asynchronous device commands (wake, shutdown) and their lifecycle
// (docs/architecture.md §8.2, ADR-003).
//
// TODO(phase-2/3): Repository (commands table; rely on the commands_one_active unique index for
// dedup), Dispatcher component (WOL send / signed agent call with retries using the same
// command_id), deadline sweeper, and audit_log entries.
package commands

import (
	"fmt"
	"slices"
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
