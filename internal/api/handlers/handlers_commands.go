package handlers

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/thanhtunguet/homecore/internal/api/gen"
	"github.com/thanhtunguet/homecore/internal/auth"
	"github.com/thanhtunguet/homecore/internal/commands"
	"github.com/thanhtunguet/homecore/internal/httpx"
)

// WakeDevice sends Wake-on-LAN. Asynchronous and idempotent (ADR-012): it answers 202 with the
// command, which is the already active wake command, or one born completed when the device is
// already online. A missing MAC address is 409; the per-device 1 req/s limit (429) is applied by
// middleware.DeviceThrottle before this handler.
func (h *Handlers) WakeDevice(w http.ResponseWriter, r *http.Request, id gen.DeviceId) {
	if h.Commands == nil {
		h.Unimplemented.WakeDevice(w, r, id)
		return
	}
	p, ok := auth.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing bearer token")
		return
	}
	c, err := h.Commands.Request(r.Context(), commands.Request{
		DeviceID: id.String(), Type: commands.TypeWake, RequestedBy: p.UserID,
	})
	if err != nil {
		h.apiError(w, r, "wake device", err)
		return
	}
	h.audit(r, "device.wake", id.String(), map[string]any{"command_id": c.ID, "status": string(c.Status)})
	httpx.WriteJSON(w, http.StatusAccepted, toAPICommand(c))
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
