package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/thanhtunguet/homecore/internal/activity"
	"github.com/thanhtunguet/homecore/internal/api/gen"
	"github.com/thanhtunguet/homecore/internal/api/middleware"
	"github.com/thanhtunguet/homecore/internal/audit"
	"github.com/thanhtunguet/homecore/internal/auth"
	"github.com/thanhtunguet/homecore/internal/commands"
	"github.com/thanhtunguet/homecore/internal/ddns"
	"github.com/thanhtunguet/homecore/internal/devices"
	"github.com/thanhtunguet/homecore/internal/httpx"
	"github.com/thanhtunguet/homecore/internal/page"
)

// ListDevices returns a page of devices ordered by name (case-insensitive).
func (h *Handlers) ListDevices(w http.ResponseWriter, r *http.Request, params gen.ListDevicesParams) {
	if h.Devices == nil {
		h.Unimplemented.ListDevices(w, r, params)
		return
	}
	limit, err := limitParam(params.Limit)
	if err != nil {
		h.apiError(w, r, "list devices", err)
		return
	}
	res, err := h.Devices.List(r.Context(), limit, derefStr(params.Cursor))
	if err != nil {
		h.apiError(w, r, "list devices", err)
		return
	}
	items, err := h.apiDevices(r, res.Items)
	if err != nil {
		h.apiError(w, r, "list devices", err)
		return
	}
	out := gen.DeviceList{Items: items}
	if res.NextCursor != "" {
		out.NextCursor = &res.NextCursor
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// CreateDevice registers a device (and its optional agent binding).
func (h *Handlers) CreateDevice(w http.ResponseWriter, r *http.Request) {
	if h.Devices == nil {
		h.Unimplemented.CreateDevice(w, r)
		return
	}
	var req gen.DeviceCreate
	if !decodeStrict(w, r, &req) {
		return
	}
	d, err := h.Devices.Create(r.Context(), devices.CreateInput{
		Name: req.Name, Description: derefStr(req.Description), Hostname: derefStr(req.Hostname),
		IPAddress: req.IpAddress, MACAddress: derefStr(req.MacAddress),
		WOLBroadcastAddress: derefStr(req.WolBroadcastAddress), WOLPort: req.WolPort,
		DeviceType: derefStr(req.DeviceType), OS: derefStr(req.Os),
		MonitorPort: req.MonitorPort, MonitorIntervalSeconds: req.MonitorIntervalSeconds,
		MonitorTimeoutMs: req.MonitorTimeoutMs, Enabled: req.Enabled, Agent: agentInput(req.Agent),
	})
	if err != nil {
		h.apiError(w, r, "create device", err)
		return
	}
	h.audit(r, "device.create", d.ID, map[string]any{"name": d.Name})
	h.writeDevice(w, r, http.StatusCreated, d)
}

// GetDevice returns one device.
func (h *Handlers) GetDevice(w http.ResponseWriter, r *http.Request, id gen.DeviceId) {
	if h.Devices == nil {
		h.Unimplemented.GetDevice(w, r, id)
		return
	}
	d, err := h.Devices.Get(r.Context(), id.String())
	if err != nil {
		h.apiError(w, r, "get device", err)
		return
	}
	h.writeDevice(w, r, http.StatusOK, d)
}

// UpdateDevice applies a partial update. Omitted fields are unchanged; "" clears description,
// hostname, mac_address and wol_broadcast_address.
func (h *Handlers) UpdateDevice(w http.ResponseWriter, r *http.Request, id gen.DeviceId) {
	if h.Devices == nil {
		h.Unimplemented.UpdateDevice(w, r, id)
		return
	}
	var req gen.DeviceUpdate
	if !decodeStrict(w, r, &req) {
		return
	}
	in := devices.UpdateInput{
		Name: req.Name, Description: req.Description, Hostname: req.Hostname, IPAddress: req.IpAddress,
		MACAddress: req.MacAddress, WOLBroadcastAddress: req.WolBroadcastAddress, WOLPort: req.WolPort,
		DeviceType: enumStr(req.DeviceType), OS: enumStr(req.Os), MonitorPort: req.MonitorPort,
		MonitorIntervalSeconds: req.MonitorIntervalSeconds, MonitorTimeoutMs: req.MonitorTimeoutMs,
		Enabled: req.Enabled, Agent: agentInput(req.Agent),
	}
	d, err := h.Devices.Update(r.Context(), id.String(), in)
	if err != nil {
		h.apiError(w, r, "update device", err)
		return
	}
	if !in.IsEmpty() {
		h.audit(r, "device.update", d.ID, map[string]any{"name": d.Name, "fields": changedFields(req)})
	}
	h.writeDevice(w, r, http.StatusOK, d)
}

// DeleteDevice removes a device. Its agent binding, commands and status history are deleted with it
// (ON DELETE CASCADE), including a command that is still in flight.
func (h *Handlers) DeleteDevice(w http.ResponseWriter, r *http.Request, id gen.DeviceId) {
	if h.Devices == nil {
		h.Unimplemented.DeleteDevice(w, r, id)
		return
	}
	d, err := h.Devices.Get(r.Context(), id.String())
	if err == nil {
		err = h.Devices.Delete(r.Context(), id.String())
	}
	if err != nil {
		h.apiError(w, r, "delete device", err)
		return
	}
	h.audit(r, "device.delete", d.ID, map[string]any{"name": d.Name})
	w.WriteHeader(http.StatusNoContent)
}

// RemoveDeviceAgent deletes the agent binding. Idempotent: 204 whether or not a binding existed;
// 404 only for an unknown device.
func (h *Handlers) RemoveDeviceAgent(w http.ResponseWriter, r *http.Request, id gen.DeviceId) {
	if h.Devices == nil {
		h.Unimplemented.RemoveDeviceAgent(w, r, id)
		return
	}
	if err := h.Devices.RemoveAgent(r.Context(), id.String()); err != nil {
		h.apiError(w, r, "remove agent binding", err)
		return
	}
	h.audit(r, "device.agent_remove", id.String(), nil)
	w.WriteHeader(http.StatusNoContent)
}

// ProbeDevice makes a TCP connect attempt now. The result is recorded like any observation.
func (h *Handlers) ProbeDevice(w http.ResponseWriter, r *http.Request, id gen.DeviceId) {
	if h.Monitor == nil {
		h.Unimplemented.ProbeDevice(w, r, id)
		return
	}
	out, err := h.Monitor.ProbeNow(r.Context(), id.String())
	if err != nil {
		h.apiError(w, r, "probe device", err)
		return
	}
	res := gen.ProbeResult{
		DeviceId: id, Reachable: out.Result.Reachable, Status: gen.DeviceStatus(out.Status),
		CheckedAt: out.Result.CheckedAt.UTC(),
	}
	if out.Result.Reachable {
		res.LatencyMs = ptr(int(out.Result.Latency.Milliseconds()))
	} else if reason := out.Result.FailureReason(); reason != "" {
		res.Error = &reason
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// GetDeviceHistory returns the status transitions of a device, newest first.
func (h *Handlers) GetDeviceHistory(w http.ResponseWriter, r *http.Request, id gen.DeviceId, params gen.GetDeviceHistoryParams) {
	if h.Devices == nil {
		h.Unimplemented.GetDeviceHistory(w, r, id, params)
		return
	}
	limit, err := limitParam(params.Limit)
	if err != nil {
		h.apiError(w, r, "device history", err)
		return
	}
	res, err := h.Devices.History(r.Context(), id.String(), limit, derefStr(params.Cursor))
	if err != nil {
		h.apiError(w, r, "device history", err)
		return
	}
	items := make([]gen.StatusChange, 0, len(res.Items))
	for _, c := range res.Items {
		sc := gen.StatusChange{Status: gen.DeviceStatus(c.Status), ChangedAt: c.ChangedAt.UTC()}
		if c.PreviousStatus != "" {
			sc.PreviousStatus = ptr(gen.DeviceStatus(c.PreviousStatus))
		}
		if c.Reason != "" {
			sc.Reason = ptr(c.Reason)
		}
		items = append(items, sc)
	}
	out := gen.StatusHistory{Items: items}
	if res.NextCursor != "" {
		out.NextCursor = &res.NextCursor
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// ---- helpers ----------------------------------------------------------------------------------

func (h *Handlers) writeDevice(w http.ResponseWriter, r *http.Request, status int, d devices.Device) {
	items, err := h.apiDevices(r, []devices.Device{d})
	if err != nil {
		h.apiError(w, r, "device response", err)
		return
	}
	httpx.WriteJSON(w, status, items[0])
}

// apiDevices converts devices to their API shape, deriving power_transition from the active
// commands with one query for the whole page.
func (h *Handlers) apiDevices(r *http.Request, ds []devices.Device) ([]gen.Device, error) {
	var transitions map[string]devices.PowerTransition
	if h.Commands != nil && len(ds) > 0 {
		ids := make([]string, len(ds))
		for i, d := range ds {
			ids[i] = d.ID
		}
		var err error
		if transitions, err = h.Commands.PowerTransitions(r.Context(), ids); err != nil {
			return nil, err
		}
	}
	out := make([]gen.Device, len(ds))
	for i, d := range ds {
		pt, ok := transitions[d.ID]
		if !ok {
			pt = devices.TransitionNone
		}
		out[i] = toAPIDevice(d, pt)
	}
	return out, nil
}

func toAPIDevice(d devices.Device, pt devices.PowerTransition) gen.Device {
	out := gen.Device{
		Id:                     uuid.MustParse(d.ID),
		Name:                   d.Name,
		Description:            nonEmpty(d.Description),
		Hostname:               nonEmpty(d.Hostname),
		IpAddress:              d.IPAddress,
		MacAddress:             nonEmpty(d.MACAddress),
		WolBroadcastAddress:    nonEmpty(d.WOLBroadcastAddress),
		WolPort:                ptr(d.WOLPort),
		DeviceType:             ptr(gen.DeviceType(d.Type)),
		Os:                     ptr(gen.OperatingSystem(d.OS)),
		MonitorPort:            d.MonitorPort,
		MonitorIntervalSeconds: int(d.MonitorInterval.Seconds()),
		MonitorTimeoutMs:       int(d.MonitorTimeout.Milliseconds()),
		Enabled:                d.Enabled,
		Status:                 gen.DeviceStatus(d.Status),
		PowerTransition:        gen.PowerTransition(pt),
		LastLatencyMs:          d.LastLatencyMs,
		CreatedAt:              d.CreatedAt.UTC(),
		UpdatedAt:              d.UpdatedAt.UTC(),
	}
	if d.LastSeenAt != nil {
		out.LastSeenAt = ptr(d.LastSeenAt.UTC())
	}
	if d.LastCheckedAt != nil {
		out.LastCheckedAt = ptr(d.LastCheckedAt.UTC())
	}
	if d.Agent != nil {
		out.Agent = &gen.AgentBinding{
			AgentId: uuid.MustParse(d.Agent.AgentID), Port: d.Agent.Port,
			Scheme:  ptr(gen.AgentBindingScheme(d.Agent.Scheme)),
			Status:  ptr(gen.AgentBindingStatus(d.Agent.Status)),
			Version: nonEmpty(d.Agent.Version),
		}
		if d.Agent.LastOKAt != nil {
			out.Agent.LastOkAt = ptr(d.Agent.LastOKAt.UTC())
		}
	}
	return out
}

func agentInput(a *gen.AgentBinding) *devices.AgentInput {
	if a == nil {
		return nil
	}
	return &devices.AgentInput{AgentID: a.AgentId.String(), Port: a.Port, Scheme: derefStr(a.Scheme)}
}

// changedFields names the fields present in a PATCH body (never their values) for the audit log.
func changedFields(u any) []string {
	b, _ := json.Marshal(u)
	var m map[string]json.RawMessage
	_ = json.Unmarshal(b, &m)
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	slices.Sort(names)
	return names
}

func (h *Handlers) audit(r *http.Request, action, deviceID string, details map[string]any) {
	h.auditResult(r, action, deviceID, audit.ResultOK, details)
}

// auditResult writes an audit entry about a device with the acting user and client address.
func (h *Handlers) auditResult(r *http.Request, action, deviceID, result string, details map[string]any) {
	h.auditTarget(r, action, "device", deviceID, result, details)
}

// auditTarget writes an audit entry about any target with the acting user and client address.
func (h *Handlers) auditTarget(r *http.Request, action, targetType, targetID, result string, details map[string]any) {
	if h.Audit == nil {
		return
	}
	e := audit.Entry{
		Action: action, TargetType: targetType, TargetID: targetID, Result: result,
		RemoteAddr: middleware.ClientIP(r), Details: details,
	}
	if p, ok := auth.FromContext(r.Context()); ok {
		e.ActorUserID, e.Actor = p.UserID, p.Username
	}
	if err := h.Audit.Write(r.Context(), e); err != nil {
		h.Log.Warn("audit write failed", "action", action, "err", err)
	}
}

// apiError maps domain errors to the HTTP error envelope; anything else is a logged 500.
func (h *Handlers) apiError(w http.ResponseWriter, r *http.Request, op string, err error) {
	var verr *devices.ValidationError
	var dverr *ddns.ValidationError
	var cferr *ddns.CloudflareError
	switch {
	case errors.As(err, &cferr):
		// Cloudflare refused or failed a lookup; its own reason is the message (never the token).
		httpx.WriteError(w, http.StatusBadGateway, "cloudflare_error", cferr.Error())
	case errors.As(err, &dverr):
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", dverr.Error())
	case errors.Is(err, ddns.ErrDuplicate):
		// The spec declares no 409 for DDNS, so a duplicate target is a 400.
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", err.Error())
	case errors.Is(err, ddns.ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "not_found", "ddns configuration not found")
	case errors.Is(err, devices.ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "not_found", "device not found")
	case errors.Is(err, commands.ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "not_found", "command not found")
	case errors.Is(err, devices.ErrNameTaken), errors.Is(err, devices.ErrAgentTaken), errors.Is(err, commands.ErrPrecondition):
		httpx.WriteError(w, http.StatusConflict, "conflict", err.Error())
	case errors.As(err, &verr):
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", verr.Error())
	case errors.Is(err, page.ErrBadCursor), errors.Is(err, page.ErrBadLimit), errors.Is(err, commands.ErrInvalid),
		errors.Is(err, activity.ErrInvalid):
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", err.Error())
	default:
		if errors.Is(err, context.Canceled) && r.Context().Err() != nil {
			return // the client went away
		}
		h.Log.Error("handler failed", "op", op, "err", err, "path", r.URL.Path)
		httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
	}
}

// decodeStrict reads a JSON object, rejecting unknown fields and trailing data with a 400.
func decodeStrict(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var tooBig *http.MaxBytesError
		msg := "request body must be a JSON object: " + err.Error()
		if errors.As(err, &tooBig) {
			msg = "request body too large"
		}
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", msg)
		return false
	}
	if dec.More() {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "request body must be a single JSON object")
		return false
	}
	return true
}

// limitParam checks the optional `limit` query parameter (1..200). Absent means "use the default"
// (0 for the services); an explicit 0 is out of range and rejected.
func limitParam(p *int) (int, error) {
	if p == nil {
		return 0, nil
	}
	if *p < 1 || *p > page.MaxLimit {
		return 0, page.ErrBadLimit
	}
	return *p, nil
}

func derefStr[T ~string](p *T) string {
	if p == nil {
		return ""
	}
	return string(*p)
}

// enumStr turns an optional enum into the optional string the domain validates.
func enumStr[T ~string](p *T) *string {
	if p == nil {
		return nil
	}
	s := string(*p)
	return &s
}

func nonEmpty(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &s
}
