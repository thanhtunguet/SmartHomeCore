// Package devices is the device registry: model, validation and persistence (devices,
// agent_bindings and device_status_history tables). Identity is the UUID; IP/hostname/MAC are
// mutable attributes (ADR-002).
//
// Files: device.go (model), validate.go (input rules), service.go (use cases), repo.go
// (Repository interface and errors), repo_sqlite.go (SQLite implementation).
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

// Type is the informational kind of a machine (icons and labels in clients).
type Type string

// OS is the informational operating system family.
type OS string

// Enumerations of the API (api/openapi.yaml DeviceType, OperatingSystem).
const (
	TypeDesktop Type = "desktop"
	TypeServer  Type = "server"
	TypeLaptop  Type = "laptop"
	TypeTV      Type = "tv"
	TypeNAS     Type = "nas"
	TypeIoT     Type = "iot"
	TypeRouter  Type = "router"

	OSWindows  OS = "windows"
	OSLinux    OS = "linux"
	OSMacOS    OS = "macos"
	OSAndroid  OS = "android"
	OSFreeBSD  OS = "freebsd"
	OSEmbedded OS = "embedded"
)

// Defaults applied on creation (match the spec and the column defaults).
const (
	DefaultWOLPort         = 9
	DefaultMonitorInterval = 30 * time.Second
	DefaultMonitorTimeout  = 2000 * time.Millisecond
	DefaultAgentPort       = 9750
	DefaultAgentScheme     = "http"
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
	Type                Type
	OS                  OS
	MonitorPort         int
	MonitorInterval     time.Duration
	MonitorTimeout      time.Duration
	Enabled             bool
	Status              Status
	LastSeenAt          *time.Time
	LastCheckedAt       *time.Time
	LastLatencyMs       *int // connect time of the last successful probe
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

// AgentBinding links a device to the HomeCore Agent running on it. Holds no secret.
type AgentBinding struct {
	AgentID string
	Port    int
	Scheme  string // http | https

	// Last known state of the agent, written by the shutdown dispatcher and the health job (never
	// by the device API). Status is AgentUnknown until the first contact.
	Status   AgentStatus
	Version  string     // from GET /agent/v1/healthz, empty until a health check succeeded
	LastOKAt *time.Time // last successful contact
}

// AgentStatus is the Controller's view of an agent (api/openapi.yaml AgentBinding.status).
type AgentStatus string

const (
	AgentUnknown     AgentStatus = "unknown"
	AgentReady       AgentStatus = "ready"
	AgentUnreachable AgentStatus = "unreachable"
)

// AgentContact is the outcome of one contact with an agent, as the dispatcher or the health job
// reports it.
type AgentContact struct {
	OK      bool      // the right agent answered
	Version string    // reported version; empty keeps the stored one
	At      time.Time // when the contact happened
}

// StatusChange is one row of the status history: a transition, not every probe.
type StatusChange struct {
	ID             int64
	Status         Status
	PreviousStatus Status // empty when unknown to the writer
	Reason         string
	ChangedAt      time.Time
}

// Observation is the result of one probe as the monitor records it.
type Observation struct {
	DeviceID  string
	At        time.Time
	Reachable bool
	Latency   time.Duration // meaningful only when Reachable
	Status    Status        // status after applying hysteresis
	Previous  Status        // status before this observation
	Changed   bool          // Status != Previous: append a history row
	Reason    string        // history reason, e.g. "probe" or "manual probe"
}

// MonitorTarget is a device the monitor should consider, with a hint about urgency.
type MonitorTarget struct {
	Device Device
	// Urgent is true while the device has an active (non-terminal) command: the monitor then
	// checks it every tick instead of every monitor_interval, so a wake or shutdown completes
	// quickly. It is also the reason a disabled device is still probed.
	Urgent bool
}
