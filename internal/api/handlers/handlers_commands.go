package handlers

import (
	"errors"
	"maps"
	"net/http"

	"github.com/google/uuid"

	"github.com/thanhtunguet/homecore/internal/api/gen"
	"github.com/thanhtunguet/homecore/internal/audit"
	"github.com/thanhtunguet/homecore/internal/auth"
	"github.com/thanhtunguet/homecore/internal/commands"
	"github.com/thanhtunguet/homecore/internal/httpx"
)

// errUnauthenticated is returned when a handler runs without the principal Authorize stores; it
// cannot happen behind the middleware and is answered as 401.
var errUnauthenticated = errors.New("missing principal")

// WakeDevice sends Wake-on-LAN. Asynchronous and idempotent (ADR-012): it answers 202 with the
// command, which is the already active wake command, or one born completed when the device is
// already online. A missing MAC address is 409; the per-device 1 req/s limit (429) is applied by
// middleware.DeviceThrottle before this handler.
func (h *Handlers) WakeDevice(w http.ResponseWriter, r *http.Request, id gen.DeviceId) {
	if h.Commands == nil {
		h.Unimplemented.WakeDevice(w, r, id)
		return
	}
	c, err := h.requestWake(r, id.String(), nil)
	if err != nil {
		h.apiError(w, r, "wake device", err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, toAPICommand(c))
}

// ShutdownDevice requests a signed shutdown through the device's agent. Asynchronous and idempotent
// (ADR-012): 202 with the command, which is the already active shutdown command, or one born
// completed when the device is already offline (no agent contact). A device without an agent
// binding is 409; the 1 req/s per-device limit (429) is applied by middleware.DeviceThrottle.
// Every accepted request is audited (device.shutdown) with the acting user, the command id and
// how it went; dispatch failures (agent unreachable, rejected) are a failed *command*, not an
// HTTP error.
func (h *Handlers) ShutdownDevice(w http.ResponseWriter, r *http.Request, id gen.DeviceId) {
	if h.Commands == nil {
		h.Unimplemented.ShutdownDevice(w, r, id)
		return
	}
	c, err := h.requestShutdown(r, id.String(), nil)
	if err != nil {
		h.apiError(w, r, "shutdown device", err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, toAPICommand(c))
}

// requestWake issues the wake command for the authenticated caller and audits it. extra is added to
// the audit details (the legacy endpoints record how they were called).
func (h *Handlers) requestWake(r *http.Request, deviceID string, extra map[string]any) (commands.Command, error) {
	p, ok := auth.FromContext(r.Context())
	if !ok {
		return commands.Command{}, errUnauthenticated
	}
	c, err := h.Commands.Request(r.Context(), commands.Request{
		DeviceID: deviceID, Type: commands.TypeWake, RequestedBy: p.UserID,
	})
	if err != nil {
		return commands.Command{}, err
	}
	details := map[string]any{"command_id": c.ID, "status": string(c.Status)}
	maps.Copy(details, extra)
	h.audit(r, "device.wake", deviceID, details)
	return c, nil
}

// requestShutdown issues the shutdown command for the authenticated caller and audits it, with
// the failure code when dispatch already failed. extra is added to the audit details.
func (h *Handlers) requestShutdown(r *http.Request, deviceID string, extra map[string]any) (commands.Command, error) {
	p, ok := auth.FromContext(r.Context())
	if !ok {
		return commands.Command{}, errUnauthenticated
	}
	c, err := h.Commands.Request(r.Context(), commands.Request{
		DeviceID: deviceID, Type: commands.TypeShutdown, RequestedBy: p.UserID,
	})
	if err != nil {
		return commands.Command{}, err
	}
	details := map[string]any{"command_id": c.ID, "status": string(c.Status)}
	result := audit.ResultOK
	if c.Status == commands.StatusFailed {
		result = audit.ResultFailed
		details["error_code"] = c.ErrorCode
	}
	if h.Devices != nil {
		if d, err := h.Devices.Get(r.Context(), deviceID); err == nil {
			details["name"] = d.Name
		}
	}
	maps.Copy(details, extra)
	h.auditResult(r, "device.shutdown", deviceID, result, details)
	return c, nil
}

// ListCommands returns the command history, newest first, optionally filtered by device and status.
func (h *Handlers) ListCommands(w http.ResponseWriter, r *http.Request, params gen.ListCommandsParams) {
	if h.Commands == nil {
		h.Unimplemented.ListCommands(w, r, params)
		return
	}
	limit, err := limitParam(params.Limit)
	if err != nil {
		h.apiError(w, r, "list commands", err)
		return
	}
	p := commands.ListParams{Limit: limit, Cursor: derefStr(params.Cursor)}
	if params.DeviceId != nil {
		p.DeviceID = params.DeviceId.String()
	}
	if params.Status != nil {
		p.Status = commands.Status(*params.Status)
	}
	res, err := h.Commands.List(r.Context(), p)
	if err != nil {
		h.apiError(w, r, "list commands", err)
		return
	}
	items := make([]gen.Command, len(res.Items))
	for i, c := range res.Items {
		items[i] = toAPICommand(c)
	}
	out := gen.CommandList{Items: items}
	if res.NextCursor != "" {
		out.NextCursor = &res.NextCursor
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// GetCommand returns one command (the UI polls it after wake/shutdown).
func (h *Handlers) GetCommand(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	if h.Commands == nil {
		h.Unimplemented.GetCommand(w, r, id)
		return
	}
	c, err := h.Commands.Get(r.Context(), id.String())
	if err != nil {
		h.apiError(w, r, "get command", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toAPICommand(c))
}

func toAPICommand(c commands.Command) gen.Command {
	out := gen.Command{
		Id:        uuid.MustParse(c.ID),
		DeviceId:  uuid.MustParse(c.DeviceID),
		Type:      gen.CommandType(c.Type),
		Status:    gen.CommandStatus(c.Status),
		CreatedAt: c.CreatedAt.UTC(),
		UpdatedAt: c.UpdatedAt.UTC(),
		ErrorCode: nonEmpty(c.ErrorCode),
		Error:     nonEmpty(c.Error),
	}
	if id, err := uuid.Parse(c.RequestedBy); err == nil {
		out.RequestedBy = &id
	}
	if c.DeadlineAt != nil {
		out.DeadlineAt = ptr(c.DeadlineAt.UTC())
	}
	if c.CompletedAt != nil {
		out.CompletedAt = ptr(c.CompletedAt.UTC())
	}
	return out
}
