package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/thanhtunguet/homecore/internal/api/gen"
	"github.com/thanhtunguet/homecore/internal/auth"
	"github.com/thanhtunguet/homecore/internal/commands"
	"github.com/thanhtunguet/homecore/internal/devices"
	"github.com/thanhtunguet/homecore/internal/httpx"
	"github.com/thanhtunguet/homecore/internal/mcp"
	"github.com/thanhtunguet/homecore/internal/version"
)

// The MCP server (ADR-020) is a thin adapter over the device, command and monitoring services,
// exactly like the legacy endpoints: the same throttle, audit trail and idempotency rules apply.
// There is no per-tool permission; a valid service account key may call every tool.

// mcpVia is recorded in the audit details so an entry shows which door was used.
var mcpVia = map[string]any{"via": "mcp"}

// mcpListCap bounds list_devices so a huge inventory cannot produce an unbounded answer.
const mcpListCap = 500

const mcpInstructions = `Controls the computers of a home network. Use list_devices to see what exists, ` +
	`get_devices for one device, and control_device to turn a device on (Wake-on-LAN), turn it off ` +
	`(signed shutdown through its agent) or check its status. turn_on and turn_off only request the ` +
	`change and return at once: call control_device with check_status a little later to see whether ` +
	`the device is now online or offline.`

// CallMcp serves one MCP message. Only a service account key may use it; a session token is refused
// even though it is valid, because the endpoint exists for agents holding a key.
func (h *Handlers) CallMcp(w http.ResponseWriter, r *http.Request) {
	if h.Devices == nil || h.Commands == nil {
		h.Unimplemented.CallMcp(w, r)
		return
	}
	if p, ok := auth.FromContext(r.Context()); !ok || p.KeyID == "" {
		httpx.WriteError(w, http.StatusForbidden, "forbidden", "the MCP endpoint needs a service account key")
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooBig *http.MaxBytesError
		msg := "could not read the request body"
		if errors.As(err, &tooBig) {
			msg = "request body too large"
		}
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", msg)
		return
	}
	srv := &mcp.Server{
		Name: "homecore", Title: "HomeCore", Version: version.Version,
		Instructions: mcpInstructions, Tools: h.mcpTools(r),
	}
	status, out := srv.Handle(r.Context(), body)
	w.Header().Set("Cache-Control", "no-store")
	if out == nil {
		w.WriteHeader(status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(out)
}

// mcpDevice is how a device is shown to an agent: what it is, whether it is on, and what can be
// done with it. It leaves out network internals (MAC, ports, agent binding).
type mcpDevice struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Description     string     `json:"description,omitempty"`
	DeviceType      string     `json:"device_type,omitempty"`
	OS              string     `json:"os,omitempty"`
	Enabled         bool       `json:"enabled"`
	Status          string     `json:"status"`           // online | offline | unknown
	PowerTransition string     `json:"power_transition"` // none | waking | shutting_down
	LastSeenAt      *time.Time `json:"last_seen_at,omitempty"`
	LastCheckedAt   *time.Time `json:"last_checked_at,omitempty"`
	CanTurnOn       bool       `json:"can_turn_on"`  // has a MAC address for Wake-on-LAN
	CanTurnOff      bool       `json:"can_turn_off"` // has an agent that can be asked to shut down
}

func toMCPDevice(d gen.Device) mcpDevice {
	out := mcpDevice{
		ID: d.Id.String(), Name: d.Name, Enabled: d.Enabled, Status: string(d.Status),
		PowerTransition: string(d.PowerTransition), LastSeenAt: d.LastSeenAt, LastCheckedAt: d.LastCheckedAt,
		CanTurnOn: d.MacAddress != nil, CanTurnOff: d.Agent != nil,
	}
	if d.Description != nil {
		out.Description = *d.Description
	}
	if d.DeviceType != nil {
		out.DeviceType = string(*d.DeviceType)
	}
	if d.Os != nil {
		out.OS = string(*d.Os)
	}
	return out
}

// mcpTools builds the tools for one request: they close over r for the caller's identity, the client
// address and the audit entry.
func (h *Handlers) mcpTools(r *http.Request) []mcp.Tool {
	deviceProp := map[string]any{
		"type": "string", "minLength": 1,
		"description": "The device's id (UUID) or its name (not case-sensitive), as shown by list_devices.",
	}
	return []mcp.Tool{
		{
			Name: "list_devices", Title: "List devices",
			Description: "List every device with its current status (online, offline or unknown), whether it " +
				"is being turned on or off right now, and whether it can be turned on and off.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
			Hints:       mcp.Hints{ReadOnly: true},
			Call:        func(ctx context.Context, _ json.RawMessage) mcp.Result { return h.mcpListDevices(ctx, r) },
		},
		{
			Name: "get_devices", Title: "Get a device",
			Description: "Get one device by its id or name, with its current status.",
			InputSchema: map[string]any{
				"type": "object", "properties": map[string]any{"device": deviceProp},
				"required": []string{"device"}, "additionalProperties": false,
			},
			Hints: mcp.Hints{ReadOnly: true},
			Call: func(ctx context.Context, args json.RawMessage) mcp.Result {
				var a struct {
					Device string `json:"device"`
				}
				if res, bad := mcpArgs(args, &a); bad {
					return res
				}
				d, err := h.Devices.Resolve(ctx, a.Device)
				if err != nil {
					return h.mcpFailure(err, a.Device)
				}
				return h.mcpOneDevice(r, d)
			},
		},
		{
			Name: "control_device", Title: "Control a device",
			Description: "Turn a device on, turn it off, or check its status. " +
				"turn_on sends Wake-on-LAN; turn_off asks the device's agent to shut it down. Both only " +
				"request the change and return immediately (repeating one is harmless, and more than one " +
				"request per device per second is refused). check_status probes the device now and reports " +
				"whether it is online; use it to follow up on turn_on or turn_off.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"device": deviceProp,
					"action": map[string]any{
						"type": "string", "enum": []string{"turn_on", "turn_off", "check_status"},
						"description": "turn_on: wake the device. turn_off: shut it down. check_status: probe it now.",
					},
				},
				"required": []string{"device", "action"}, "additionalProperties": false,
			},
			Hints: mcp.Hints{Destructive: true, Idempotent: true},
			Call: func(ctx context.Context, args json.RawMessage) mcp.Result {
				var a struct {
					Device string `json:"device"`
					Action string `json:"action"`
				}
				if res, bad := mcpArgs(args, &a); bad {
					return res
				}
				return h.mcpControl(ctx, r, a.Device, a.Action)
			},
		},
	}
}

// mcpArgs decodes a tool's arguments; a failure is an error Result the model can act on.
func mcpArgs(raw json.RawMessage, dst any) (mcp.Result, bool) {
	if err := json.Unmarshal(raw, dst); err != nil {
		return mcp.Errorf("the arguments are not valid: " + err.Error()), true
	}
	return mcp.Result{}, false
}

func (h *Handlers) mcpListDevices(ctx context.Context, r *http.Request) mcp.Result {
	var all []devices.Device
	cursor := ""
	for {
		res, err := h.Devices.List(ctx, 100, cursor)
		if err != nil {
			return h.mcpFailure(err, "")
		}
		all = append(all, res.Items...)
		if res.NextCursor == "" || len(all) >= mcpListCap {
			break
		}
		cursor = res.NextCursor
	}
	list, err := h.apiDevices(r, all)
	if err != nil {
		return h.mcpFailure(err, "")
	}
	out := make([]mcpDevice, len(list))
	for i, d := range list {
		out[i] = toMCPDevice(d)
	}
	return mcp.JSON(map[string]any{"devices": out, "count": len(out)})
}

func (h *Handlers) mcpOneDevice(r *http.Request, d devices.Device) mcp.Result {
	list, err := h.apiDevices(r, []devices.Device{d})
	if err != nil {
		return h.mcpFailure(err, "")
	}
	return mcp.JSON(map[string]any{"device": toMCPDevice(list[0])})
}

func (h *Handlers) mcpControl(ctx context.Context, r *http.Request, ref, action string) mcp.Result {
	switch action {
	case "turn_on", "turn_off", "check_status":
	default:
		return mcp.Errorf(fmt.Sprintf("unknown action %q: use turn_on, turn_off or check_status", action))
	}
	d, err := h.Devices.Resolve(ctx, ref)
	if err != nil {
		return h.mcpFailure(err, ref)
	}

	if action == "check_status" {
		if h.Monitor != nil {
			out, err := h.Monitor.ProbeNow(ctx, d.ID)
			if err != nil {
				return h.mcpFailure(err, ref)
			}
			if d, err = h.Devices.Get(ctx, d.ID); err != nil { // the probe updated the stored status
				return h.mcpFailure(err, ref)
			}
			res := h.mcpOneDevice(r, d)
			if m, ok := res.Data.(map[string]any); ok {
				m["reachable"] = out.Result.Reachable
				res.Text = marshalText(m)
			}
			return res
		}
		return h.mcpOneDevice(r, d) // no monitor: the last stored status
	}

	if wait := h.Throttle.Admit(d.ID); wait > 0 {
		return mcp.Errorf(fmt.Sprintf("%s already received a power request a moment ago: wait %d second(s) and retry",
			d.Name, int(math.Ceil(wait.Seconds()))))
	}
	var c commands.Command
	if action == "turn_on" {
		c, err = h.requestWake(r, d.ID, mcpVia)
	} else {
		c, err = h.requestShutdown(r, d.ID, mcpVia)
	}
	if err != nil {
		return h.mcpFailure(err, ref)
	}
	cmd := map[string]any{"id": c.ID, "type": string(c.Type), "status": string(c.Status)}
	if c.ErrorCode != "" {
		cmd["error_code"] = c.ErrorCode
		cmd["error"] = c.Error
	}
	note := "The request was accepted; the machine has not necessarily changed state yet. " +
		"Call control_device with check_status to follow up."
	switch { // a command born completed or failed has already been decided (ADR-012)
	case c.Status == commands.StatusFailed:
		note = "The request could not be carried out; see command.error."
	case c.Status == commands.StatusCompleted && action == "turn_on":
		note = "Nothing to do: the device is already online."
	case c.Status == commands.StatusCompleted:
		note = "Nothing to do: the device is already offline."
	}
	return mcp.JSON(map[string]any{
		"device":  map[string]any{"id": d.ID, "name": d.Name},
		"action":  action,
		"command": cmd,
		"note":    note,
	})
}

func marshalText(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "internal error"
	}
	return string(b)
}

// mcpFailure turns a service error into a message for the model. Expected conditions are described;
// anything else is logged and reported only as an internal error.
func (h *Handlers) mcpFailure(err error, ref string) mcp.Result {
	switch {
	case errors.Is(err, devices.ErrNotFound):
		if strings.TrimSpace(ref) == "" {
			return mcp.Errorf("device not found")
		}
		return mcp.Errorf(fmt.Sprintf("no device with the id or name %q; call list_devices to see the names", ref))
	case errors.Is(err, commands.ErrPrecondition):
		return mcp.Errorf(err.Error())
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return mcp.Errorf("the request was interrupted; try again")
	}
	h.Log.Error("mcp tool failed", "err", err)
	return mcp.Errorf("internal error")
}
