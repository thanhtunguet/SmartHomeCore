package handlers

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/thanhtunguet/homecore/internal/alerts"
	"github.com/thanhtunguet/homecore/internal/api/gen"
	"github.com/thanhtunguet/homecore/internal/audit"
	"github.com/thanhtunguet/homecore/internal/httpx"
)

// ListAlertChannels returns every alert channel ordered by name.
func (h *Handlers) ListAlertChannels(w http.ResponseWriter, r *http.Request) {
	if h.Alerts == nil {
		h.Unimplemented.ListAlertChannels(w, r)
		return
	}
	chans, err := h.Alerts.List(r.Context())
	if err != nil {
		h.apiError(w, r, "list alert channels", err)
		return
	}
	items := make([]gen.AlertChannel, len(chans))
	for i, c := range chans {
		items[i] = toAPIAlertChannel(c)
	}
	httpx.WriteJSON(w, http.StatusOK, struct {
		Items []gen.AlertChannel `json:"items"`
	}{items})
}

// CreateAlertChannel stores a channel after the adapter accepted its settings and secret.
func (h *Handlers) CreateAlertChannel(w http.ResponseWriter, r *http.Request) {
	if h.Alerts == nil {
		h.Unimplemented.CreateAlertChannel(w, r)
		return
	}
	var req gen.AlertChannelCreate
	if !decodeStrict(w, r, &req) {
		return
	}
	in := alerts.CreateInput{
		Name: req.Name, Type: string(req.Type), Enabled: req.Enabled, Settings: req.Settings,
		Secret: derefStr(req.Secret),
	}
	if req.Events != nil {
		in.Events = toKinds(*req.Events)
	}
	c, err := h.Alerts.Create(r.Context(), in)
	if err != nil {
		h.apiError(w, r, "create alert channel", err)
		return
	}
	h.auditTarget(r, "alert_channel.create", "alert_channel", c.ID, audit.ResultOK,
		map[string]any{"name": c.Name, "type": c.Type})
	httpx.WriteJSON(w, http.StatusCreated, toAPIAlertChannel(c))
}

// GetAlertChannel returns one channel.
func (h *Handlers) GetAlertChannel(w http.ResponseWriter, r *http.Request, id gen.AlertChannelId) {
	if h.Alerts == nil {
		h.Unimplemented.GetAlertChannel(w, r, id)
		return
	}
	c, err := h.Alerts.Get(r.Context(), id.String())
	if err != nil {
		h.apiError(w, r, "get alert channel", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toAPIAlertChannel(c))
}

// UpdateAlertChannel applies a partial update.
func (h *Handlers) UpdateAlertChannel(w http.ResponseWriter, r *http.Request, id gen.AlertChannelId) {
	if h.Alerts == nil {
		h.Unimplemented.UpdateAlertChannel(w, r, id)
		return
	}
	var req gen.AlertChannelUpdate
	if !decodeStrict(w, r, &req) {
		return
	}
	in := alerts.UpdateInput{Name: req.Name, Enabled: req.Enabled, Settings: req.Settings, Secret: req.Secret}
	if req.Events != nil {
		ev := toKinds(*req.Events)
		in.Events = &ev
	}
	c, err := h.Alerts.Update(r.Context(), id.String(), in)
	if err != nil {
		h.apiError(w, r, "update alert channel", err)
		return
	}
	if !in.IsEmpty() {
		// Field names only: the secret (or any other value) never goes into the audit log.
		h.auditTarget(r, "alert_channel.update", "alert_channel", c.ID, audit.ResultOK,
			map[string]any{"name": c.Name, "type": c.Type, "fields": changedFields(req)})
	}
	httpx.WriteJSON(w, http.StatusOK, toAPIAlertChannel(c))
}

// DeleteAlertChannel removes a channel.
func (h *Handlers) DeleteAlertChannel(w http.ResponseWriter, r *http.Request, id gen.AlertChannelId) {
	if h.Alerts == nil {
		h.Unimplemented.DeleteAlertChannel(w, r, id)
		return
	}
	c, err := h.Alerts.Get(r.Context(), id.String())
	if err == nil {
		err = h.Alerts.Delete(r.Context(), id.String())
	}
	if err != nil {
		h.apiError(w, r, "delete alert channel", err)
		return
	}
	h.auditTarget(r, "alert_channel.delete", "alert_channel", c.ID, audit.ResultOK,
		map[string]any{"name": c.Name, "type": c.Type})
	w.WriteHeader(http.StatusNoContent)
}

// TestAlertChannel sends a test message now. A failed delivery is a 200 with result "failed".
func (h *Handlers) TestAlertChannel(w http.ResponseWriter, r *http.Request, id gen.AlertChannelId) {
	if h.Alerts == nil {
		h.Unimplemented.TestAlertChannel(w, r, id)
		return
	}
	res, err := h.Alerts.Test(r.Context(), id.String())
	if err != nil {
		h.apiError(w, r, "test alert channel", err)
		return
	}
	out := gen.AlertTestResult{ChannelId: id, Result: gen.AlertTestResultResultSent, SentAt: res.At.UTC()}
	details := map[string]any{}
	result := audit.ResultOK
	if res.Error != "" {
		out.Result, out.Error = gen.AlertTestResultResultFailed, ptr(res.Error)
		result, details["error"] = audit.ResultFailed, res.Error
	}
	h.auditTarget(r, "alert_channel.test", "alert_channel", res.ChannelID, result, details)
	httpx.WriteJSON(w, http.StatusOK, out)
}

func toKinds(in []gen.AlertEvent) []alerts.Kind {
	out := make([]alerts.Kind, len(in))
	for i, e := range in {
		out[i] = alerts.Kind(e)
	}
	return out
}

func toAPIAlertChannel(c alerts.Channel) gen.AlertChannel {
	events := make([]gen.AlertEvent, len(c.Events))
	for i, k := range c.Events {
		events[i] = gen.AlertEvent(k)
	}
	out := gen.AlertChannel{
		Id: uuid.MustParse(c.ID), Name: c.Name, Type: gen.AlertChannelType(c.Type), Enabled: c.Enabled,
		Events: events, Settings: c.Settings, HasSecret: c.HasSecret, LastError: nonEmpty(c.LastError),
		CreatedAt: c.CreatedAt.UTC(), UpdatedAt: c.UpdatedAt.UTC(),
	}
	if c.LastSentAt != nil {
		out.LastSentAt = ptr(c.LastSentAt.UTC())
	}
	if c.LastErrorAt != nil {
		out.LastErrorAt = ptr(c.LastErrorAt.UTC())
	}
	return out
}
