// Package devices is the device registry: model, validation and persistence (devices and
// agent_bindings tables). Identity is the UUID; IP/hostname/MAC are mutable attributes (ADR-002).
//
// TODO(phase-2): Repository interface + SQLite implementation (repo_sqlite.go), Service with
// validation (MAC via wol.NormalizeMAC, ports, name uniqueness → 409), and API handlers.
package devices

import "time"

// Status is the observed reachability of a device.
type Status string

const (
	StatusUnknown Status = "unknown"
	StatusOnline  Status = "online"
	StatusOffline Status = "offline"
)

// PowerTransition is derived from the device's active command; it is never stored.
type PowerTransition string

const (
	TransitionNone         PowerTransition = "none"
	TransitionWaking       PowerTransition = "waking"
	TransitionShuttingDown PowerTransition = "shutting_down"
)

// Device is a managed machine.
type Device struct {
	ID                  string
	Name                string
	Description         string
	Hostname            string
	IPAddress           string
	MACAddress          string
	WOLBroadcastAddress string
	WOLPort             int
	MonitorPort         int
	MonitorInterval     time.Duration
	MonitorTimeout      time.Duration
	Enabled             bool
	Status              Status
	LastSeenAt          *time.Time
	LastCheckedAt       *time.Time
	Agent               *AgentBinding
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// ProbeHost returns the address used for TCP probes: hostname if set, else IP.
func (d Device) ProbeHost() string {
	if d.Hostname != "" {
		return d.Hostname
	}
	return d.IPAddress
}

// AgentBinding links a device to the HomeCore Agent running on it.
type AgentBinding struct {
	AgentID string
	Port    int
	Scheme  string // http | https
}
