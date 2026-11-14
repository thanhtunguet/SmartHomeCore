// Package ddnstest is test support for the DDNS packages: an in-memory fake of the parts of the
// Cloudflare v4 API the Controller uses, served over httptest. It is imported by tests only.
package ddnstest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Zone is a fake zone.
type Zone struct{ ID, Name string }

// Record is a fake DNS record. Type may be anything, so tests can plant a CNAME or a TXT record.
type Record struct {
	ID, Name, Type, Content string
	Proxied                 bool
	TTL                     int
}

// Request is one request the fake received.
type Request struct {
	Method, Path, Query, Authorization, Body string
}

// Cloudflare is the fake. Create it with New, point a client at URL(), and mutate the exported
// state under Lock/Unlock (or with the helper methods) while the server runs.
type Cloudflare struct {
	mu sync.Mutex

	// Token is the only bearer token the fake accepts; others get 403 like Cloudflare's invalid-token answer.
	Token string
	// Zones are served ordered as given (the client must order them); Records are keyed by zone id.
	Zones   []Zone
	Records map[string][]Record
	// MaxPerPage caps per_page, so a test can force pagination with few records. 0 = 100.
	MaxPerPage int
	// Fail, if set, is asked first for every request and may answer instead (status 0 = carry on).
	Fail func(r *http.Request) (status int, body string, header http.Header)

	requests []Request
	srv      *httptest.Server
}

// New starts a fake accepting token. Close it with Close.
func New(token string) *Cloudflare {
	f := &Cloudflare{Token: token, Records: map[string][]Record{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /client/v4/zones", f.zones)
	mux.HandleFunc("GET /client/v4/zones/{zone}/dns_records", f.listRecords)
	mux.HandleFunc("GET /client/v4/zones/{zone}/dns_records/{id}", f.getRecord)
	mux.HandleFunc("PATCH /client/v4/zones/{zone}/dns_records/{id}", f.patchRecord)
	f.srv = httptest.NewServer(f.log(mux))
	return f
}

// URL is the API root to give to the client (".../client/v4").
func (f *Cloudflare) URL() string { return f.srv.URL + "/client/v4" }

// Close stops the server.
func (f *Cloudflare) Close() { f.srv.Close() }

// Requests returns a copy of the requests received so far.
func (f *Cloudflare) Requests() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Request(nil), f.requests...)
}

// Count returns how many requests matched method and a path prefix-free suffix ("" = any).
func (f *Cloudflare) Count(method, pathSuffix string) int {
	n := 0
	for _, r := range f.Requests() {
		if (method == "" || r.Method == method) && strings.HasSuffix(r.Path, pathSuffix) {
			n++
		}
	}
	return n
}

// Record returns a copy of a record by id.
func (f *Cloudflare) Record(zone, id string) (Record, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.Records[zone] {
		if r.ID == id {
			return r, true
		}
	}
	return Record{}, false
}

// Set replaces a record's mutable fields (as if edited in the Cloudflare dashboard).
func (f *Cloudflare) Set(zone string, rec Record) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rs := f.Records[zone]
	for i := range rs {
		if rs[i].ID == rec.ID {
			rs[i] = rec
			return
		}
	}
	f.Records[zone] = append(rs, rec)
}

// SetFail installs or clears the failure injector.
func (f *Cloudflare) SetFail(fn func(r *http.Request) (int, string, http.Header)) {
	f.mu.Lock()
	f.Fail = fn
	f.mu.Unlock()
}

func (f *Cloudflare) log(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		f.mu.Lock()
		f.requests = append(f.requests, Request{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization"), string(body)})
		fail := f.Fail
		token := f.Token
		f.mu.Unlock()
		if fail != nil {
			if status, b, h := fail(r); status != 0 {
				for k, v := range h {
					w.Header()[k] = v
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, b)
				return
			}
		}
		if r.Header.Get("Authorization") != "Bearer "+token {
			writeJSON(w, http.StatusForbidden, envelope(false, nil, errs(9109, "Invalid access token")))
			return
		}
		next.ServeHTTP(w, r)
	})
}

type info struct {
	Page       int `json:"page"`
	PerPage    int `json:"per_page"`
	Count      int `json:"count"`
	TotalCount int `json:"total_count"`
	TotalPages int `json:"total_pages"`
}

func errs(code int, msg string) []map[string]any {
	return []map[string]any{{"code": code, "message": msg}}
}

func envelope(ok bool, result any, e []map[string]any) map[string]any {
	if e == nil {
		e = []map[string]any{}
	}
	return map[string]any{"success": ok, "errors": e, "messages": []any{}, "result": result}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *Cloudflare) paging(r *http.Request, total int) (from, to int, in info) {
	per := 20
	if v, err := strconv.Atoi(r.URL.Query().Get("per_page")); err == nil && v > 0 {
		per = v
	}
	max := f.MaxPerPage
	if max <= 0 {
		max = 100
	}
	per = min(per, max)
	page := 1
	if v, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && v > 0 {
		page = v
	}
	pages := (total + per - 1) / per
	from = min((page-1)*per, total)
	to = min(from+per, total)
	return from, to, info{Page: page, PerPage: per, Count: to - from, TotalCount: total, TotalPages: pages}
}

func (f *Cloudflare) zones(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	from, to, in := f.paging(r, len(f.Zones))
	out := []map[string]any{}
	for _, z := range f.Zones[from:to] {
		out = append(out, map[string]any{"id": z.ID, "name": z.Name, "status": "active"})
	}
	e := envelope(true, out, nil)
	e["result_info"] = in
	writeJSON(w, http.StatusOK, e)
}

func recJSON(zone string, r Record) map[string]any {
	return map[string]any{"id": r.ID, "zone_id": zone, "name": r.Name, "type": r.Type, "content": r.Content,
		"proxied": r.Proxied, "ttl": r.TTL}
}

func (f *Cloudflare) listRecords(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	zone := r.PathValue("zone")
	if !f.zoneExists(zone) {
		writeJSON(w, http.StatusNotFound, envelope(false, nil, errs(7003, "Could not route to /zones/"+zone+"/dns_records, perhaps your object identifier is invalid?")))
		return
	}
	typ := r.URL.Query().Get("type")
	var rs []Record
	for _, rec := range f.Records[zone] {
		if typ == "" || rec.Type == typ {
			rs = append(rs, rec)
		}
	}
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].Name < rs[j].Name })
	from, to, in := f.paging(r, len(rs))
	out := []map[string]any{}
	for _, rec := range rs[from:to] {
		out = append(out, recJSON(zone, rec))
	}
	e := envelope(true, out, nil)
	e["result_info"] = in
	writeJSON(w, http.StatusOK, e)
}

func (f *Cloudflare) zoneExists(id string) bool {
	for _, z := range f.Zones {
		if z.ID == id {
			return true
		}
	}
	return false
}

func (f *Cloudflare) find(zone, id string) (int, bool) {
	for i, r := range f.Records[zone] {
		if r.ID == id {
			return i, true
		}
	}
	return 0, false
}

func (f *Cloudflare) getRecord(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	zone, id := r.PathValue("zone"), r.PathValue("id")
	i, ok := f.find(zone, id)
	if !ok {
		writeJSON(w, http.StatusNotFound, envelope(false, nil, errs(81044, "Record does not exist.")))
		return
	}
	writeJSON(w, http.StatusOK, envelope(true, recJSON(zone, f.Records[zone][i]), nil))
}

func (f *Cloudflare) patchRecord(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	zone, id := r.PathValue("zone"), r.PathValue("id")
	i, ok := f.find(zone, id)
	if !ok {
		writeJSON(w, http.StatusNotFound, envelope(false, nil, errs(81044, "Record does not exist.")))
		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, envelope(false, nil, errs(9207, "Invalid request body")))
		return
	}
	rec := &f.Records[zone][i]
	if v, ok := body["content"].(string); ok {
		rec.Content = v
	}
	if v, ok := body["proxied"].(bool); ok {
		rec.Proxied = v
	}
	if v, ok := body["ttl"].(float64); ok {
		rec.TTL = int(v)
	}
	for _, forbidden := range []string{"name", "type"} {
		if _, present := body[forbidden]; present {
			writeJSON(w, http.StatusBadRequest, envelope(false, nil, errs(9208, fmt.Sprintf("%s must not be sent by DDNS", forbidden))))
			return
		}
	}
	writeJSON(w, http.StatusOK, envelope(true, recJSON(zone, *rec), nil))
}
