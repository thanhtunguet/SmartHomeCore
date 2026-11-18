package handlers

import (
	"net"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/thanhtunguet/homecore/internal/api/gen"
	"github.com/thanhtunguet/homecore/internal/audit"
	"github.com/thanhtunguet/homecore/internal/httpx"
	"github.com/thanhtunguet/homecore/internal/portforward"
)

// ListPortForwards returns every rule ordered by name, with its runtime status.
func (h *Handlers) ListPortForwards(w http.ResponseWriter, r *http.Request) {
	if h.PortForward == nil {
		h.Unimplemented.ListPortForwards(w, r)
		return
	}
	views, err := h.PortForward.List(r.Context())
	if err != nil {
		h.apiError(w, r, "list port forwards", err)
		return
	}
	items := make([]gen.PortForward, len(views))
	for i, v := range views {
		items[i] = toAPIPortForward(v)
	}
	httpx.WriteJSON(w, http.StatusOK, struct {
		Items []gen.PortForward `json:"items"`
	}{items})
}

// CreatePortForward stores a rule and, when enabled, starts it. A rule that cannot listen is still
// created (201 with status "error") and retried.
func (h *Handlers) CreatePortForward(w http.ResponseWriter, r *http.Request) {
	if h.PortForward == nil {
		h.Unimplemented.CreatePortForward(w, r)
		return
	}
	var req gen.PortForwardCreate
	if !decodeStrict(w, r, &req) {
		return
	}
	v, err := h.PortForward.Create(r.Context(), portforward.CreateInput{
		Name: req.Name, Protocol: portforward.Protocol(req.Protocol), ListenAddress: derefStr(req.ListenAddress),
		ListenPort: req.ListenPort, TargetHost: req.TargetHost, TargetPort: req.TargetPort, Enabled: req.Enabled,
	})
	if err != nil {
		h.apiError(w, r, "create port forward", err)
		return
	}
	h.auditTarget(r, "port_forward.create", "port_forward", v.ID, audit.ResultOK, forwardDetails(v))
	httpx.WriteJSON(w, http.StatusCreated, toAPIPortForward(v))
}

// GetPortForward returns one rule.
func (h *Handlers) GetPortForward(w http.ResponseWriter, r *http.Request, id gen.PortForwardId) {
	if h.PortForward == nil {
		h.Unimplemented.GetPortForward(w, r, id)
		return
	}
	v, err := h.PortForward.Get(r.Context(), id.String())
	if err != nil {
		h.apiError(w, r, "get port forward", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toAPIPortForward(v))
}

// UpdatePortForward applies a partial update; `enabled` is the on/off switch.
func (h *Handlers) UpdatePortForward(w http.ResponseWriter, r *http.Request, id gen.PortForwardId) {
	if h.PortForward == nil {
		h.Unimplemented.UpdatePortForward(w, r, id)
		return
	}
	var req gen.PortForwardUpdate
	if !decodeStrict(w, r, &req) {
		return
	}
	in := portforward.UpdateInput{Name: req.Name, ListenAddress: req.ListenAddress, ListenPort: req.ListenPort,
		TargetHost: req.TargetHost, TargetPort: req.TargetPort, Enabled: req.Enabled}
	if req.Protocol != nil {
		p := portforward.Protocol(*req.Protocol)
		in.Protocol = &p
	}
	v, err := h.PortForward.Update(r.Context(), id.String(), in)
	if err != nil {
		h.apiError(w, r, "update port forward", err)
		return
	}
	if !in.IsEmpty() {
		details := forwardDetails(v)
		action := "port_forward.update"
		// A request that only flips the switch is the common case and reads better as its own action.
		if onlySwitch := in.Enabled != nil && len(changedFields(req)) == 1; onlySwitch {
			action = "port_forward.disable"
			if *in.Enabled {
				action = "port_forward.enable"
			}
		} else {
			details["fields"] = changedFields(req)
		}
		h.auditTarget(r, action, "port_forward", v.ID, audit.ResultOK, details)
	}
	httpx.WriteJSON(w, http.StatusOK, toAPIPortForward(v))
}

// DeletePortForward closes the rule's listeners and removes it.
func (h *Handlers) DeletePortForward(w http.ResponseWriter, r *http.Request, id gen.PortForwardId) {
	if h.PortForward == nil {
		h.Unimplemented.DeletePortForward(w, r, id)
		return
	}
	v, err := h.PortForward.Get(r.Context(), id.String())
	if err == nil {
		err = h.PortForward.Delete(r.Context(), id.String())
	}
	if err != nil {
		h.apiError(w, r, "delete port forward", err)
		return
	}
	h.auditTarget(r, "port_forward.delete", "port_forward", v.ID, audit.ResultOK, forwardDetails(v))
	w.WriteHeader(http.StatusNoContent)
}

// forwardDetails is what the audit log records about a rule: where it listens and where it goes.
func forwardDetails(v portforward.View) map[string]any {
	return map[string]any{
		"name": v.Name, "protocol": string(v.Protocol),
		"listen": joinHostPort(v.ListenAddress, v.ListenPort), "target": joinHostPort(v.TargetHost, v.TargetPort),
	}
}

func toAPIPortForward(v portforward.View) gen.PortForward {
	return gen.PortForward{
		Id: uuid.MustParse(v.ID), Name: v.Name, Protocol: gen.PortForwardProtocol(v.Protocol),
		ListenAddress: v.ListenAddress, ListenPort: v.ListenPort, TargetHost: v.TargetHost, TargetPort: v.TargetPort,
		Enabled: v.Enabled, Status: gen.PortForwardStatus(v.Status), LastError: nonEmpty(v.LastError),
		CreatedAt: v.CreatedAt.UTC(), UpdatedAt: v.UpdatedAt.UTC(),
	}
}

func joinHostPort(host string, port int) string { return net.JoinHostPort(host, strconv.Itoa(port)) }
