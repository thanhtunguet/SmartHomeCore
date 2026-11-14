package handlers

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/thanhtunguet/homecore/internal/api/gen"
	"github.com/thanhtunguet/homecore/internal/audit"
	"github.com/thanhtunguet/homecore/internal/ddns"
	"github.com/thanhtunguet/homecore/internal/httpx"
)

// ListDdnsConfigs returns every DDNS configuration ordered by name.
func (h *Handlers) ListDdnsConfigs(w http.ResponseWriter, r *http.Request) {
	if h.DDNS == nil {
		h.Unimplemented.ListDdnsConfigs(w, r)
		return
	}
	cfgs, err := h.DDNS.List(r.Context())
	if err != nil {
		h.apiError(w, r, "list ddns", err)
		return
	}
	items := make([]gen.DdnsConfig, len(cfgs))
	for i, c := range cfgs {
		items[i] = toAPIDdns(c)
	}
	httpx.WriteJSON(w, http.StatusOK, struct {
		Items []gen.DdnsConfig `json:"items"`
	}{items})
}

// CreateDdnsConfig stores a configuration for a record chosen from the Cloudflare lookups.
func (h *Handlers) CreateDdnsConfig(w http.ResponseWriter, r *http.Request) {
	if h.DDNS == nil {
		h.Unimplemented.CreateDdnsConfig(w, r)
		return
	}
	var req gen.DdnsConfigCreate
	if !decodeStrict(w, r, &req) {
		return
	}
	c, err := h.DDNS.Create(r.Context(), ddns.CreateInput{
		Name: req.Name, ZoneID: req.ZoneId, RecordID: req.RecordId, RecordName: req.RecordName,
		RecordType: ddns.RecordType(req.RecordType), Proxied: req.Proxied, TTL: req.Ttl,
		IntervalSeconds: req.IntervalSeconds, Enabled: req.Enabled, APIToken: derefStr(req.ApiToken),
	})
	if err != nil {
		h.apiError(w, r, "create ddns", err)
		return
	}
	h.auditTarget(r, "ddns.create", "ddns_config", c.ID, audit.ResultOK,
		map[string]any{"name": c.Name, "record_name": c.RecordName})
	httpx.WriteJSON(w, http.StatusCreated, toAPIDdns(c))
}

// UpdateDdnsConfig applies a partial update (record_id is required when the target changes).
func (h *Handlers) UpdateDdnsConfig(w http.ResponseWriter, r *http.Request, id gen.DdnsId) {
	if h.DDNS == nil {
		h.Unimplemented.UpdateDdnsConfig(w, r, id)
		return
	}
	var req gen.DdnsConfigUpdate
	if !decodeStrict(w, r, &req) {
		return
	}
	in := ddns.UpdateInput{
		Name: req.Name, ZoneID: req.ZoneId, RecordID: req.RecordId, RecordName: req.RecordName,
		Proxied: req.Proxied, TTL: req.Ttl, IntervalSeconds: req.IntervalSeconds, Enabled: req.Enabled,
		APIToken: req.ApiToken,
	}
	if req.RecordType != nil {
		t := ddns.RecordType(*req.RecordType)
		in.RecordType = &t
	}
	c, err := h.DDNS.Update(r.Context(), id.String(), in)
	if err != nil {
		h.apiError(w, r, "update ddns", err)
		return
	}
	if !in.IsEmpty() {
		// Field names only: the token (or any other value) never goes into the audit log.
		h.auditTarget(r, "ddns.update", "ddns_config", c.ID, audit.ResultOK,
			map[string]any{"name": c.Name, "record_name": c.RecordName, "fields": changedFields(req)})
	}
	httpx.WriteJSON(w, http.StatusOK, toAPIDdns(c))
}

// DeleteDdnsConfig removes a configuration; its sync history is deleted with it.
func (h *Handlers) DeleteDdnsConfig(w http.ResponseWriter, r *http.Request, id gen.DdnsId) {
	if h.DDNS == nil {
		h.Unimplemented.DeleteDdnsConfig(w, r, id)
		return
	}
	c, err := h.DDNS.Get(r.Context(), id.String())
	if err == nil {
		err = h.DDNS.Delete(r.Context(), id.String())
	}
	if err != nil {
		h.apiError(w, r, "delete ddns", err)
		return
	}
	h.auditTarget(r, "ddns.delete", "ddns_config", c.ID, audit.ResultOK,
		map[string]any{"name": c.Name, "record_name": c.RecordName})
	w.WriteHeader(http.StatusNoContent)
}

// SyncDdnsConfig runs one sync now through the same code path as the scheduled job. A sync that
// fails is a 200 with result "failed" and the reason in `error`, because the request itself worked.
func (h *Handlers) SyncDdnsConfig(w http.ResponseWriter, r *http.Request, id gen.DdnsId) {
	if h.DDNS == nil {
		h.Unimplemented.SyncDdnsConfig(w, r, id)
		return
	}
	res, err := h.DDNS.Sync(r.Context(), id.String())
	if err != nil {
		h.apiError(w, r, "sync ddns", err)
		return
	}
	details := map[string]any{"sync_result": string(res.Result), "trigger": "manual"}
	if c, err := h.DDNS.Get(r.Context(), id.String()); err == nil {
		details["name"], details["record_name"] = c.Name, c.RecordName
	}
	result := audit.ResultOK
	if res.Result == ddns.ResultFailed {
		result = audit.ResultFailed
		details["error"] = res.Error
	}
	if res.IP != "" {
		details["ip"] = res.IP
	}
	h.auditTarget(r, "ddns.sync", "ddns_config", res.ConfigID, result, details)

	out := gen.DdnsSyncResult{
		ConfigId: id, Result: gen.DdnsSyncResultResult(res.Result), SyncedAt: res.At.UTC(),
		Ip: nonEmpty(res.IP), Error: nonEmpty(res.Error),
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// ListCloudflareZones feeds the zone dropdown. The token (or a config id) comes in the body and is
// not stored. Cloudflare failures are 502 cloudflare_error.
func (h *Handlers) ListCloudflareZones(w http.ResponseWriter, r *http.Request) {
	if h.DDNS == nil {
		h.Unimplemented.ListCloudflareZones(w, r)
		return
	}
	cr, ok := lookupCredentials(w, r)
	if !ok {
		return
	}
	zones, err := h.DDNS.LookupZones(r.Context(), cr)
	if err != nil {
		h.apiError(w, r, "cloudflare zones", err)
		return
	}
	items := make([]gen.CloudflareZone, len(zones))
	for i, z := range zones {
		items[i] = gen.CloudflareZone{Id: z.ID, Name: z.Name}
	}
	httpx.WriteJSON(w, http.StatusOK, gen.CloudflareZoneList{Items: items})
}

// ListCloudflareRecords feeds the record dropdown: the A and AAAA records of one zone.
func (h *Handlers) ListCloudflareRecords(w http.ResponseWriter, r *http.Request, zoneID string) {
	if h.DDNS == nil {
		h.Unimplemented.ListCloudflareRecords(w, r, zoneID)
		return
	}
	cr, ok := lookupCredentials(w, r)
	if !ok {
		return
	}
	recs, err := h.DDNS.LookupRecords(r.Context(), zoneID, cr)
	if err != nil {
		h.apiError(w, r, "cloudflare records", err)
		return
	}
	items := make([]gen.CloudflareRecord, len(recs))
	for i, rec := range recs {
		items[i] = gen.CloudflareRecord{
			Id: rec.ID, Name: rec.Name, Type: gen.DnsRecordType(rec.Type), Content: rec.Content,
			Proxied: rec.Proxied, Ttl: rec.TTL,
		}
	}
	httpx.WriteJSON(w, http.StatusOK, gen.CloudflareRecordList{Items: items})
}

// lookupCredentials reads the CloudflareLookup body. Whether exactly one of the two is present is
// checked by the service (400).
func lookupCredentials(w http.ResponseWriter, r *http.Request) (ddns.Credentials, bool) {
	var req gen.CloudflareLookup
	if !decodeStrict(w, r, &req) {
		return ddns.Credentials{}, false
	}
	cr := ddns.Credentials{Token: derefStr(req.ApiToken)}
	if req.ConfigId != nil {
		cr.ConfigID = uuid.UUID(*req.ConfigId).String()
	}
	return cr, true
}

func toAPIDdns(c ddns.Config) gen.DdnsConfig {
	out := gen.DdnsConfig{
		Id: uuid.MustParse(c.ID), Name: c.Name, ZoneId: c.ZoneID, RecordId: c.RecordID,
		RecordName: c.RecordName, RecordType: gen.DnsRecordType(c.RecordType), Proxied: c.Proxied, Ttl: c.TTL,
		IntervalSeconds: int(c.Interval.Seconds()), Enabled: c.Enabled, HasApiToken: c.HasToken,
		LastIp: nonEmpty(c.LastIP), DetectedIp: nonEmpty(c.DetectedIP), DnsValue: nonEmpty(c.DNSValue),
		LastError: nonEmpty(c.LastError), CreatedAt: c.CreatedAt.UTC(), UpdatedAt: c.UpdatedAt.UTC(),
	}
	if c.LastCheckedAt != nil {
		out.LastCheckedAt = ptr(c.LastCheckedAt.UTC())
	}
	if c.LastSyncedAt != nil {
		out.LastSyncedAt = ptr(c.LastSyncedAt.UTC())
	}
	if c.LastErrorAt != nil {
		out.LastErrorAt = ptr(c.LastErrorAt.UTC())
	}
	return out
}
