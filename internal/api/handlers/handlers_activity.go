package handlers

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/thanhtunguet/homecore/internal/activity"
	"github.com/thanhtunguet/homecore/internal/api/gen"
	"github.com/thanhtunguet/homecore/internal/httpx"
)

// ListActivity returns the activity feed (audit_log), newest first.
func (h *Handlers) ListActivity(w http.ResponseWriter, r *http.Request, params gen.ListActivityParams) {
	if h.Activity == nil {
		h.Unimplemented.ListActivity(w, r, params)
		return
	}
	limit, err := limitParam(params.Limit)
	if err != nil {
		h.apiError(w, r, "list activity", err)
		return
	}
	p := activity.Params{Limit: limit, Cursor: derefStr(params.Cursor), Category: activity.Category(derefStr(params.Category))}
	if params.DeviceId != nil {
		p.DeviceID = params.DeviceId.String()
	}
	res, err := h.Activity.List(r.Context(), p)
	if err != nil {
		h.apiError(w, r, "list activity", err)
		return
	}
	items := make([]gen.ActivityItem, len(res.Items))
	for i, it := range res.Items {
		items[i] = gen.ActivityItem{
			Id: it.ID, At: it.At.UTC(), Actor: nonEmpty(it.Actor), Category: gen.ActivityCategory(it.Category),
			Action: it.Action, Result: it.Result, Message: it.Message,
		}
		if id, err := uuid.Parse(it.DeviceID); err == nil {
			items[i].DeviceId = &id
		}
		if id, err := uuid.Parse(it.CommandID); err == nil {
			items[i].CommandId = &id
		}
	}
	out := gen.ActivityList{Items: items}
	if res.NextCursor != "" {
		out.NextCursor = &res.NextCursor
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}
