package handlers

import (
	"errors"
	"net/http"
	"slices"

	"github.com/google/uuid"

	"github.com/thanhtunguet/homecore/internal/agentctl"
	"github.com/thanhtunguet/homecore/internal/api/gen"
	"github.com/thanhtunguet/homecore/internal/devices"
	"github.com/thanhtunguet/homecore/internal/httpx"
)

// InspectAgent asks the agent at host:port who it is (GET /agent/v1/healthz) so the device form can
// reuse the id of an installed agent and warn when this Controller's key is not trusted by it.
// The request is made by the Controller on the user's behalf; agentctl.CheckTarget limits it to
// private addresses (docs/requirements/shutdown-agent.md REQ-AG-020).
func (h *Handlers) InspectAgent(w http.ResponseWriter, r *http.Request) {
	if h.AgentInspector == nil {
		h.Unimplemented.InspectAgent(w, r)
		return
	}
	var req gen.AgentInspectRequest
	if !decodeStrict(w, r, &req) {
		return
	}
	port, scheme := devices.DefaultAgentPort, devices.DefaultAgentScheme
	if req.Port != nil {
		port = *req.Port
	}
	if req.Scheme != nil {
		scheme = string(*req.Scheme)
	}
	res, err := h.AgentInspector.Inspect(r.Context(), scheme, req.Host, port)
	if errors.Is(err, agentctl.ErrInvalidTarget) {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err != nil {
		h.apiError(w, r, "inspect agent", err)
		return
	}
	out := gen.AgentInspection{Reachable: res.Reachable}
	if res.Error != "" {
		out.Error = ptr(res.Error)
	}
	if res.Reachable {
		out.AgentId = ptr(uuid.MustParse(res.AgentID)) // canonicalised by the Inspector
		out.Version = nonEmpty(res.Version)
		if res.TrustedKeyIDs != nil {
			out.TrustedKeyIds = ptr(res.TrustedKeyIDs)
			if h.ControllerKey != nil {
				out.ControllerTrusted = ptr(slices.Contains(res.TrustedKeyIDs, h.ControllerKey.Public().KeyID))
			}
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}
