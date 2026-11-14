package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thanhtunguet/homecore/internal/api/gen"
	"github.com/thanhtunguet/homecore/internal/ddns"
	"github.com/thanhtunguet/homecore/internal/platform"
	"github.com/thanhtunguet/homecore/internal/system"
)

const cfTestToken = "cf-API-TOKEN-0123456789-do-not-leak"

// scriptedIP is the public-IP discoverer of the API tests: the providers' quorum is covered in
// internal/ddns; here the address (or the quorum failure) is whatever the test says.
type scriptedIP struct {
	mu     sync.Mutex
	v4, v6 string
	err    error
}

func (s *scriptedIP) PublicIP(_ context.Context, family ddns.RecordType) (netip.Addr, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return netip.Addr{}, s.err
	}
	if family == ddns.RecordAAAA {
		return netip.MustParseAddr(s.v6), nil
	}
	return netip.MustParseAddr(s.v4), nil
}

func (s *scriptedIP) set(v4 string, err error) { s.mu.Lock(); s.v4, s.err = v4, err; s.mu.Unlock() }

func (e *devEnv) errBody(rec *httptest.ResponseRecorder) gen.Error {
	return decode[gen.Error](e.t, rec)
}

func ddnsCreateBody(over map[string]any) map[string]any {
	b := map[string]any{"name": "Home", "zone_id": "zone1", "record_id": "rec1", "record_name": "home.example.com",
		"record_type": "A", "ttl": 300, "interval_seconds": 120, "api_token": cfTestToken}
	for k, v := range over {
		if v == nil {
			delete(b, k)
		} else {
			b[k] = v
		}
	}
	return b
}

// REQ-API-020: the dropdown lookups, from the token typed in the form or from a saved configuration.
func TestCloudflareLookupsOverHTTP(t *testing.T) {
	e := newDevEnv(t)

	zones := decode[gen.CloudflareZoneList](t, ok(t, e.as("POST", "/api/v1/ddns/cloudflare/zones", map[string]any{"api_token": cfTestToken}), 200))
	if len(zones.Items) != 2 || zones.Items[0].Name != "Another.org" || zones.Items[1].Id != "zone1" {
		t.Fatalf("zones (ordered by name): %+v", zones.Items)
	}
	recs := decode[gen.CloudflareRecordList](t, ok(t, e.as("POST", "/api/v1/ddns/cloudflare/zones/zone1/records", map[string]any{"api_token": cfTestToken}), 200))
	if len(recs.Items) != 3 {
		t.Fatalf("records (A and AAAA only): %+v", recs.Items)
	}
	r := recs.Items[0]
	if r.Id != "rec4" || r.Name != "cam.example.com" || r.Type != "A" || r.Content != "8.8.4.4" || r.Ttl != 120 || r.Proxied {
		t.Fatalf("first record: %+v", r)
	}
	if recs.Items[2].Type != "AAAA" || !recs.Items[2].Proxied {
		t.Fatalf("AAAA record: %+v", recs.Items[2])
	}
	if n := e.count("ddns_configs"); n != 0 {
		t.Fatalf("a lookup stored %d configurations", n)
	}

	// By config_id the sealed token of the saved configuration is used.
	c := decode[gen.DdnsConfig](t, ok(t, e.as("POST", "/api/v1/ddns", ddnsCreateBody(nil)), 201))
	if rec := e.as("POST", "/api/v1/ddns/cloudflare/zones", map[string]any{"config_id": c.Id}); rec.Code != 200 {
		t.Fatalf("by config id: %d %s", rec.Code, rec.Body)
	}
	for _, rq := range e.cf.Requests() {
		if rq.Authorization != "Bearer "+cfTestToken {
			t.Fatalf("Cloudflare received %q", rq.Authorization)
		}
		if strings.Contains(rq.Path+rq.Query, cfTestToken) {
			t.Fatal("the token is in a URL")
		}
	}

	// Exactly one source, otherwise 400; unknown configuration 404.
	for name, body := range map[string]any{
		"neither":          map[string]any{},
		"both":             map[string]any{"api_token": cfTestToken, "config_id": c.Id},
		"blank token":      map[string]any{"api_token": "  "},
		"token with space": map[string]any{"api_token": "a b"},
		"bad config id":    map[string]any{"config_id": "not-a-uuid"},
		"unknown field":    map[string]any{"api_token": cfTestToken, "zone": "x"},
	} {
		rec := e.as("POST", "/api/v1/ddns/cloudflare/zones", body)
		if rec.Code != 400 {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
		if rec = e.as("POST", "/api/v1/ddns/cloudflare/zones/zone1/records", body); rec.Code != 400 {
			t.Errorf("records, %s: %d %s", name, rec.Code, rec.Body)
		}
	}
	unknown := "0193a0b2-7c4e-7f00-8000-000000000001"
	for _, path := range []string{"/api/v1/ddns/cloudflare/zones", "/api/v1/ddns/cloudflare/zones/zone1/records"} {
		if rec := e.as("POST", path, map[string]any{"config_id": unknown}); rec.Code != 404 {
			t.Errorf("%s unknown config: %d %s", path, rec.Code, rec.Body)
		}
	}
	if rec := e.as("POST", "/api/v1/ddns/cloudflare/zones/..%2Fuser/records", map[string]any{"api_token": cfTestToken}); rec.Code != 400 {
		t.Errorf("zone id with a slash: %d %s", rec.Code, rec.Body)
	}
}

// REQ-API-020: Cloudflare failures are 502 cloudflare_error with Cloudflare's reason and never the token.
func TestCloudflareLookupFailuresAre502(t *testing.T) {
	e := newDevEnv(t)
	check := func(label, wantMsg string, rec *httptest.ResponseRecorder) {
		t.Helper()
		if rec.Code != 502 {
			t.Fatalf("%s: %d %s", label, rec.Code, rec.Body)
		}
		er := e.errBody(rec)
		if er.Error.Code != "cloudflare_error" || !strings.Contains(er.Error.Message, wantMsg) {
			t.Fatalf("%s: %+v", label, er)
		}
		if strings.Contains(rec.Body.String(), "SECRET-WRONG") || strings.Contains(rec.Body.String(), cfTestToken) {
			t.Fatalf("%s: token in the response: %s", label, rec.Body)
		}
	}
	check("wrong token", "Invalid access token", e.as("POST", "/api/v1/ddns/cloudflare/zones", map[string]any{"api_token": "tok-SECRET-WRONG"}))
	check("wrong token, records", "Invalid access token", e.as("POST", "/api/v1/ddns/cloudflare/zones/zone1/records", map[string]any{"api_token": "tok-SECRET-WRONG"}))
	check("unknown zone", "Could not route", e.as("POST", "/api/v1/ddns/cloudflare/zones/nozone/records", map[string]any{"api_token": cfTestToken}))
	e.cf.SetFail(func(*http.Request) (int, string, http.Header) {
		return 429, `{"success":false,"errors":[{"code":971,"message":"Too many requests"}]}`, http.Header{"Retry-After": {"30"}}
	})
	check("rate limited", "Too many requests", e.as("POST", "/api/v1/ddns/cloudflare/zones", map[string]any{"api_token": cfTestToken}))
	e.cf.SetFail(func(*http.Request) (int, string, http.Header) { return 503, `<html>maintenance</html>`, nil })
	check("down", "Service Unavailable", e.as("POST", "/api/v1/ddns/cloudflare/zones", map[string]any{"api_token": cfTestToken}))
	e.cf.SetFail(nil)
	if strings.Contains(e.logs.String(), cfTestToken) || strings.Contains(e.logs.String(), "SECRET-WRONG") {
		t.Fatal("a token reached the log")
	}
}

// REQ-API-020, REQ-DDNS-004..006: the whole story over HTTP with every response checked against the spec.
func TestDDNSLifecycleOverHTTP(t *testing.T) {
	e := newDevEnv(t)
	var bodies []string
	keep := func(rec *httptest.ResponseRecorder) *httptest.ResponseRecorder {
		bodies = append(bodies, rec.Body.String())
		return rec
	}

	// Empty list first.
	if l := decode[struct{ Items []gen.DdnsConfig }](t, keep(ok(t, e.as("GET", "/api/v1/ddns", nil), 200))); len(l.Items) != 0 {
		t.Fatalf("%+v", l)
	}

	// Create from lookup results.
	rec := keep(ok(t, e.as("POST", "/api/v1/ddns", ddnsCreateBody(nil)), 201))
	c := decode[gen.DdnsConfig](t, rec)
	if c.Name != "Home" || c.ZoneId != "zone1" || c.RecordId != "rec1" || c.RecordName != "home.example.com" || c.RecordType != "A" ||
		c.Ttl != 300 || c.IntervalSeconds != 120 || !c.Enabled || c.Proxied || !c.HasApiToken ||
		c.LastIp != nil || c.DetectedIp != nil || c.DnsValue != nil || c.LastCheckedAt != nil || c.LastError != nil {
		t.Fatalf("created: %+v", c)
	}
	if strings.Contains(rec.Body.String(), "api_token") && !strings.Contains(rec.Body.String(), "has_api_token") {
		t.Fatal("the response mentions api_token")
	}

	// First sync updates the record at Cloudflare and fills the observed state.
	res := decode[gen.DdnsSyncResult](t, keep(ok(t, e.as("POST", "/api/v1/ddns/"+c.Id.String()+"/sync", nil), 200)))
	if res.ConfigId != c.Id || res.Result != "updated" || res.Ip == nil || *res.Ip != "8.8.8.8" || res.Error != nil || time.Since(res.SyncedAt) > time.Minute {
		t.Fatalf("sync: %+v", res)
	}
	if r, _ := e.cf.Record("zone1", "rec1"); r.Content != "8.8.8.8" || r.Name != "home.example.com" || r.TTL != 300 {
		t.Fatalf("record at Cloudflare: %+v", r)
	}
	l := decode[struct{ Items []gen.DdnsConfig }](t, keep(ok(t, e.as("GET", "/api/v1/ddns", nil), 200)))
	got := l.Items[0]
	if got.LastIp == nil || *got.LastIp != "8.8.8.8" || got.DetectedIp == nil || *got.DetectedIp != "8.8.8.8" || got.DnsValue == nil || *got.DnsValue != "8.8.8.8" ||
		got.LastCheckedAt == nil || got.LastSyncedAt == nil || got.LastError != nil || got.LastErrorAt != nil {
		t.Fatalf("state after sync: %+v", got)
	}

	// Unchanged, then the address changes.
	if res = decode[gen.DdnsSyncResult](t, keep(ok(t, e.as("POST", "/api/v1/ddns/"+c.Id.String()+"/sync", nil), 200))); res.Result != "unchanged" {
		t.Fatalf("%+v", res)
	}
	e.ip.set("8.8.4.5", nil)
	if res = decode[gen.DdnsSyncResult](t, keep(ok(t, e.as("POST", "/api/v1/ddns/"+c.Id.String()+"/sync", nil), 200))); res.Result != "updated" || *res.Ip != "8.8.4.5" {
		t.Fatalf("%+v", res)
	}

	// Providers disagree: 200 with result failed, the error text and no change at Cloudflare.
	e.ip.set("", errors.New("public IPv4 address not confirmed: 2 of 2 providers gave a usable answer and at least 2 must agree (a: 8.8.8.8; b: 1.1.1.1)"))
	patches := e.cf.Count("PATCH", "")
	res = decode[gen.DdnsSyncResult](t, keep(ok(t, e.as("POST", "/api/v1/ddns/"+c.Id.String()+"/sync", nil), 200)))
	if res.Result != "failed" || res.Error == nil || !strings.Contains(*res.Error, "must agree") || res.Ip != nil || e.cf.Count("PATCH", "") != patches {
		t.Fatalf("%+v", res)
	}
	got = decode[struct{ Items []gen.DdnsConfig }](t, keep(ok(t, e.as("GET", "/api/v1/ddns", nil), 200))).Items[0]
	if got.LastError == nil || got.LastErrorAt == nil || got.LastIp == nil || *got.LastIp != "8.8.4.5" {
		t.Fatalf("state after a failed sync: %+v", got)
	}
	e.ip.set("8.8.4.5", nil)

	// PATCH variants.
	patch := func(body map[string]any) *httptest.ResponseRecorder {
		return keep(e.as("PATCH", "/api/v1/ddns/"+c.Id.String(), body))
	}
	if rec := patch(map[string]any{"name": "Renamed", "interval_seconds": 600, "enabled": false}); rec.Code != 200 {
		t.Fatalf("simple patch: %d %s", rec.Code, rec.Body)
	} else if p := decode[gen.DdnsConfig](t, rec); p.Name != "Renamed" || p.IntervalSeconds != 600 || p.Enabled || p.LastIp == nil {
		t.Fatalf("%+v", p)
	}
	for name, body := range map[string]map[string]any{
		"zone_id without record_id":     {"zone_id": "zone2"},
		"record_name without record_id": {"record_name": "cam.example.com"},
		"record_type without record_id": {"record_type": "AAAA"},
		"zone with record but old name": {"zone_id": "zone2", "record_id": "rec9"},
		"record of another name":        {"record_id": "rec4"},
		"unknown record":                {"record_id": "nope", "record_name": "x.example.com"},
		"a CNAME":                       {"record_id": "rec3", "record_name": "alias.example.com"},
		"ttl 0":                         {"ttl": 0},
		"interval 30":                   {"interval_seconds": 30},
		"empty name":                    {"name": ""},
		"empty token":                   {"api_token": ""},
		"wrong token":                   {"api_token": "tok-SECRET-WRONG"},
		"record_type CNAME":             {"record_type": "CNAME", "record_id": "rec3"},
	} {
		rec := patch(body)
		if rec.Code != 400 {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	// Retarget to another record: record_id with name and type, state reset.
	rec = patch(map[string]any{"record_id": "rec4", "record_name": "cam.example.com", "enabled": true})
	if rec.Code != 200 {
		t.Fatalf("retarget: %d %s", rec.Code, rec.Body)
	}
	if p := decode[gen.DdnsConfig](t, rec); p.RecordId != "rec4" || p.RecordName != "cam.example.com" || p.LastIp != nil || p.DnsValue != nil || p.LastCheckedAt != nil {
		t.Fatalf("retargeted: %+v", p)
	}
	// Rotating the token to a valid one works; the old record stays untouched.
	e.cf.Token = "cf-ROTATED-TOKEN-xyz"
	if rec := patch(map[string]any{"api_token": "cf-ROTATED-TOKEN-xyz"}); rec.Code != 200 {
		t.Fatalf("token rotation: %d %s", rec.Code, rec.Body)
	}
	if res = decode[gen.DdnsSyncResult](t, keep(ok(t, e.as("POST", "/api/v1/ddns/"+c.Id.String()+"/sync", nil), 200))); res.Result != "updated" {
		t.Fatalf("sync with the rotated token: %+v", res)
	}
	if r, _ := e.cf.Record("zone1", "rec4"); r.Content != "8.8.4.5" {
		t.Fatalf("%+v", r)
	}
	// Duplicate target: the spec has no 409, so 400.
	if rec := keep(e.as("POST", "/api/v1/ddns", ddnsCreateBody(map[string]any{"record_id": "rec4", "record_name": "cam.example.com", "ttl": 120, "api_token": "cf-ROTATED-TOKEN-xyz"}))); rec.Code != 400 {
		t.Fatalf("duplicate: %d %s", rec.Code, rec.Body)
	}

	// Delete, then 404s everywhere; history goes with it.
	if n := e.count("ddns_sync_history"); n == 0 {
		t.Fatal("no history rows")
	}
	ok(t, keep(e.as("DELETE", "/api/v1/ddns/"+c.Id.String(), nil)), 204)
	for _, rq := range [][2]string{{"DELETE", ""}, {"PATCH", ""}, {"POST", "/sync"}} {
		if rec := keep(e.as(rq[0], "/api/v1/ddns/"+c.Id.String()+rq[1], map[string]any{"name": "x"})); rec.Code != 404 {
			t.Errorf("%s after delete: %d %s", rq[0], rec.Code, rec.Body)
		}
	}
	if e.count("ddns_sync_history") != 0 || e.count("ddns_configs") != 0 {
		t.Fatal("history survived the delete")
	}

	// Nothing the API said or logged contains a token.
	for _, b := range bodies {
		for _, secret := range []string{cfTestToken, "cf-ROTATED-TOKEN-xyz", "SECRET-WRONG"} {
			if strings.Contains(b, secret) {
				t.Fatalf("token %q in a response: %s", secret, b)
			}
		}
	}
	for _, secret := range []string{cfTestToken, "cf-ROTATED-TOKEN-xyz", "SECRET-WRONG"} {
		if strings.Contains(e.logs.String(), secret) {
			t.Fatalf("token %q in the log", secret)
		}
		if n := e.countLike("audit_log", "details", secret); n != 0 {
			t.Fatalf("token %q in the audit log", secret)
		}
	}
}

func (e *devEnv) count(table string) int {
	e.t.Helper()
	var n int
	if err := e.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *devEnv) countLike(table, col, sub string) int {
	e.t.Helper()
	var n int
	if err := e.db.QueryRow(`SELECT count(*) FROM `+table+` WHERE `+col+` LIKE '%' || ? || '%'`, sub).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// REQ-API-020: creating needs a record id and a token the Controller can verify.
func TestCreateDdnsConfigValidationOverHTTP(t *testing.T) {
	e := newDevEnv(t)
	for name, over := range map[string]map[string]any{
		"no record_id":     {"record_id": nil},
		"empty record_id":  {"record_id": ""},
		"no api_token":     {"api_token": nil},
		"no zone_id":       {"zone_id": nil},
		"no name":          {"name": nil},
		"no record_type":   {"record_type": nil},
		"bad record_type":  {"record_type": "TXT"},
		"ttl 86401":        {"ttl": 86401},
		"interval 59":      {"interval_seconds": 59},
		"name 101 chars":   {"name": strings.Repeat("n", 101)},
		"wrong token":      {"api_token": "tok-SECRET-WRONG"},
		"record not found": {"record_id": "nope"},
		"name mismatch":    {"record_name": "other.example.com"},
		"type mismatch":    {"record_type": "AAAA"},
		"unknown property": {"extra": 1},
		"id with slash":    {"record_id": "rec1/../x"},
	} {
		rec := e.as("POST", "/api/v1/ddns", ddnsCreateBody(over))
		if rec.Code != 400 {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		} else if strings.Contains(rec.Body.String(), "SECRET-WRONG") || strings.Contains(rec.Body.String(), cfTestToken) {
			t.Errorf("%s: token in the error: %s", name, rec.Body)
		}
	}
	if e.count("ddns_configs") != 0 {
		t.Fatal("a rejected configuration was stored")
	}
	// Malformed bodies.
	for _, body := range []string{"", "[]", "{", `{"name":"a"} {"name":"b"}`} {
		if rec := e.as("POST", "/api/v1/ddns", body); rec.Code != 400 {
			t.Errorf("body %q: %d", body, rec.Code)
		}
	}
	// Cloudflare down while saving: the configuration is stored and the first sync reports the problem.
	e.cf.SetFail(func(*http.Request) (int, string, http.Header) { return 503, `{}`, nil })
	c := decode[gen.DdnsConfig](t, ok(t, e.as("POST", "/api/v1/ddns", ddnsCreateBody(nil)), 201))
	res := decode[gen.DdnsSyncResult](t, ok(t, e.as("POST", "/api/v1/ddns/"+c.Id.String()+"/sync", nil), 200))
	if res.Result != "failed" || res.Error == nil || !strings.Contains(*res.Error, "Service Unavailable") {
		t.Fatalf("%+v", res)
	}
}

// REQ-API-020: every DDNS and metrics operation needs a valid access token.
func TestDDNSAndMetricsRequireAuthentication(t *testing.T) {
	e := newDevEnv(t)
	id := "0193a0b2-7c4e-7f00-8000-000000000001"
	for _, rq := range [][2]string{
		{"GET", "/api/v1/ddns"}, {"POST", "/api/v1/ddns"}, {"PATCH", "/api/v1/ddns/" + id}, {"DELETE", "/api/v1/ddns/" + id},
		{"POST", "/api/v1/ddns/" + id + "/sync"}, {"POST", "/api/v1/ddns/cloudflare/zones"},
		{"POST", "/api/v1/ddns/cloudflare/zones/z/records"}, {"GET", "/api/v1/system/metrics"},
	} {
		if rec := e.call(rq[0], rq[1], map[string]any{}, ""); rec.Code != 401 {
			t.Errorf("%s %s without a token: %d", rq[0], rq[1], rec.Code)
		}
		if rec := e.call(rq[0], rq[1], map[string]any{}, "garbage"); rec.Code != 401 {
			t.Errorf("%s %s with a bad token: %d", rq[0], rq[1], rec.Code)
		}
	}
}

// REQ-API-023, REQ-DDNS-008: DDNS changes appear in the activity feed, readable and without secrets.
func TestDDNSActivityFeed(t *testing.T) {
	e := newDevEnv(t)
	c := decode[gen.DdnsConfig](t, ok(t, e.as("POST", "/api/v1/ddns", ddnsCreateBody(nil)), 201))
	id := c.Id.String()
	ok(t, e.as("POST", "/api/v1/ddns/"+id+"/sync", nil), 200) // updated
	ok(t, e.as("PATCH", "/api/v1/ddns/"+id, map[string]any{"name": "Renamed", "api_token": cfTestToken}), 200)
	e.ip.set("", errors.New("providers disagree"))
	ok(t, e.as("POST", "/api/v1/ddns/"+id+"/sync", nil), 200) // failed
	ok(t, e.as("DELETE", "/api/v1/ddns/"+id, nil), 204)

	feed := decode[gen.ActivityList](t, ok(t, e.as("GET", "/api/v1/activity?category=ddns", nil), 200))
	var got []string
	for _, it := range feed.Items {
		if it.Category != "ddns" || it.Actor == nil || *it.Actor != "alice" || it.DeviceId != nil {
			t.Fatalf("entry: %+v", it)
		}
		got = append(got, it.Action+" | "+it.Result+" | "+it.Message)
	}
	want := []string{
		"ddns.delete | ok | DDNS configuration Renamed (home.example.com) deleted",
		"ddns.sync | failed | DDNS sync of home.example.com failed",
		"ddns.update | ok | DDNS configuration Renamed (home.example.com) updated",
		"ddns.sync | ok | home.example.com updated to 8.8.8.8",
		"ddns.create | ok | DDNS configuration Home (home.example.com) created",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("feed:\n got %q\nwant %q", got, want)
	}
	raw := e.as("GET", "/api/v1/activity?category=ddns", nil).Body.String()
	for _, banned := range []string{cfTestToken, "api_token", "providers disagree", "192.0.2."} {
		if strings.Contains(raw, banned) {
			t.Fatalf("the feed exposes %q: %s", banned, raw)
		}
	}
	// The audit row for the update names the fields, never the values.
	var details string
	if err := e.db.QueryRow(`SELECT details FROM audit_log WHERE action = 'ddns.update'`).Scan(&details); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(details, `"api_token"`) || strings.Contains(details, cfTestToken) {
		t.Fatalf("update audit details: %s", details)
	}
}

// REQ-SYS-004: GET /system/metrics returns a snapshot that conforms to the spec; any user may read it.
func TestSystemMetricsOverHTTP(t *testing.T) {
	e := newDevEnv(t)
	m := decode[gen.SystemMetrics](t, ok(t, e.as("GET", "/api/v1/system/metrics", nil), 200))
	if m.Platform == "" || time.Since(m.CollectedAt) > time.Minute || m.Hostname == nil || m.CpuCores == nil || *m.CpuCores < 1 ||
		m.Process == nil || m.Process.Pid == nil || m.Process.Goroutines == nil || m.Process.HeapAllocBytes == nil || m.Restricted == nil {
		t.Fatalf("metrics: %+v", m)
	}
	restricted := func(name string) bool { return slices.Contains(*m.Restricted, name) }
	// Every optional block is either present or listed as restricted.
	if (m.Memory == nil) != restricted("memory") || (m.Disk == nil) != restricted("disk") || (m.UptimeSeconds == nil) != restricted("uptime_seconds") ||
		(m.LoadAverage == nil) != restricted("load_average") || (m.Kernel == nil) != restricted("kernel") ||
		(m.Process.RssBytes == nil) != restricted("process.rss_bytes") {
		t.Fatalf("a metric is neither present nor restricted: %+v restricted=%v", m, *m.Restricted)
	}
	if m.Memory != nil && (*m.Memory.TotalBytes <= 0 || *m.Memory.UsedBytes > *m.Memory.TotalBytes) {
		t.Fatalf("memory: %+v", m.Memory)
	}
	if m.Disk != nil && (*m.Disk.TotalBytes <= 0 || *m.Disk.FreeBytes > *m.Disk.TotalBytes || m.Disk.Path == nil) {
		t.Fatalf("disk: %+v", m.Disk)
	}
	if m.LoadAverage != nil && len(*m.LoadAverage) != 3 {
		t.Fatalf("load: %+v", m.LoadAverage)
	}
	// A repeated read inside the cache TTL is the same snapshot.
	again := decode[gen.SystemMetrics](t, ok(t, e.as("GET", "/api/v1/system/metrics", nil), 200))
	if !again.CollectedAt.Equal(m.CollectedAt) {
		t.Fatalf("the snapshot was not cached: %v vs %v", m.CollectedAt, again.CollectedAt)
	}

	// The runtime fallback (gopsutil unavailable): what it cannot know is restricted, not zero.
	e.metrics.set(system.RuntimeProvider{})
	fb := decode[gen.SystemMetrics](t, ok(t, e.as("GET", "/api/v1/system/metrics", nil), 200))
	if fb.CpuPercent != nil || fb.Memory != nil || fb.Disk != nil || fb.UptimeSeconds != nil || fb.LoadAverage != nil || fb.Kernel != nil ||
		fb.Process == nil || fb.Process.RssBytes != nil || fb.Process.Goroutines == nil || fb.Restricted == nil ||
		!slices.Contains(*fb.Restricted, "cpu_percent") || !slices.Contains(*fb.Restricted, "memory") {
		t.Fatalf("fallback: %+v restricted=%v", fb, fb.Restricted)
	}

	// A provider failure is an internal error in the envelope, not a stack trace.
	e.metrics.set(failingMetrics{})
	if rec := e.callRaw("GET", "/api/v1/system/metrics", nil, e.token); rec.Code != 500 || strings.Contains(rec.Body.String(), "disk on fire") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

type failingMetrics struct{}

func (failingMetrics) Collect(context.Context) (platform.Metrics, error) {
	return platform.Metrics{}, errors.New("disk on fire")
}
