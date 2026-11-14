package ddns

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Cloudflare client limits.
const (
	// DefaultCloudflareURL is the Cloudflare v4 API root.
	DefaultCloudflareURL = "https://api.cloudflare.com/client/v4"

	cloudflareTimeout   = 10 * time.Second
	maxCloudflareBody   = 4 << 20 // a page of 100 records is about 40 KB
	maxMessageLen       = 300
	zonesPerPage        = 50
	recordsPerPage      = 100
	maxPages            = 100 // upper bound on pagination loops, whatever result_info claims
	maxRetryAfter       = time.Hour
	defaultRateLimitFor = 60 * time.Second
)

// CloudflareError is a failed Cloudflare call. Message is Cloudflare's own reason (or a short
// description of a network failure), cut to a sane length and scrubbed of the API token; the token
// is never part of any error.
type CloudflareError struct {
	Status     int           // HTTP status; 0 for a network failure
	Code       int           // first Cloudflare error code, if any
	Message    string        // safe to show to users
	RetryAfter time.Duration // set on 429 when Cloudflare says how long to wait
	network    bool
}

func (e *CloudflareError) Error() string {
	if e.Status > 299 {
		return fmt.Sprintf("cloudflare: %s (HTTP %d)", e.Message, e.Status)
	}
	return "cloudflare: " + e.Message
}

// Transient reports whether retrying later can succeed: network failures, 429 and 5xx.
func (e *CloudflareError) Transient() bool {
	return e.network || e.Status == http.StatusTooManyRequests || e.Status >= 500
}

// RateLimited reports a 429.
func (e *CloudflareError) RateLimited() bool { return e.Status == http.StatusTooManyRequests }

// AuthFailure reports that Cloudflare rejected the token (401/403).
func (e *CloudflareError) AuthFailure() bool {
	return e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden
}

// NotFound reports a 404 (unknown zone or record, or no access to it).
func (e *CloudflareError) NotFound() bool { return e.Status == http.StatusNotFound }

// Cloudflare is a net/http client for the parts of the Cloudflare v4 API DDNS uses. No SDK.
type Cloudflare struct {
	base *url.URL
	http *http.Client
}

var _ DNSProvider = (*Cloudflare)(nil)

// NewCloudflare returns a client for the API root baseURL ("" = DefaultCloudflareURL). The URL must
// be https unless allowHTTP (tests and the development smoke test with a fake server). hc may be
// nil; the client never follows redirects, whatever hc says.
func NewCloudflare(baseURL string, allowHTTP bool, hc *http.Client) (*Cloudflare, error) {
	if baseURL == "" {
		baseURL = DefaultCloudflareURL
	}
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("ddns: invalid Cloudflare API URL")
	}
	if u.Scheme != "https" && !(allowHTTP && u.Scheme == "http") {
		return nil, errors.New("ddns: the Cloudflare API URL must use https")
	}
	if hc == nil {
		hc = &http.Client{Timeout: cloudflareTimeout, Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: cloudflareTimeout,
			MaxIdleConns:          2,
			IdleConnTimeout:       30 * time.Second,
		}}
	}
	c := *hc
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Cloudflare{base: u, http: &c}, nil
}

// ---- wire types -----------------------------------------------------------------------------------

type envelope struct {
	Success    bool            `json:"success"`
	Errors     []cfMessage     `json:"errors"`
	Result     json.RawMessage `json:"result"`
	ResultInfo struct {
		Page       int `json:"page"`
		TotalPages int `json:"total_pages"`
	} `json:"result_info"`
}

type cfMessage struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type cfZone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type cfRecord struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
	TTL     int    `json:"ttl"`
}

func (r cfRecord) record() Record {
	return Record{ID: r.ID, Name: strings.ToLower(strings.TrimSuffix(r.Name, ".")), Type: RecordType(r.Type),
		Content: r.Content, Proxied: r.Proxied, TTL: r.TTL}
}

// ---- API ------------------------------------------------------------------------------------------

// ListZones returns every zone the token can see, ordered by name.
func (c *Cloudflare) ListZones(ctx context.Context, token string) ([]Zone, error) {
	var out []Zone
	for page := 1; page <= maxPages; page++ {
		q := url.Values{"page": {strconv.Itoa(page)}, "per_page": {strconv.Itoa(zonesPerPage)},
			"order": {"name"}, "direction": {"asc"}}
		var zs []cfZone
		info, err := c.do(ctx, token, http.MethodGet, "/zones", q, nil, &zs)
		if err != nil {
			return nil, err
		}
		for _, z := range zs {
			out = append(out, Zone{ID: z.ID, Name: z.Name})
		}
		if page >= info.ResultInfo.TotalPages || len(zs) == 0 {
			break
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

// ListRecords returns the A and AAAA records of a zone, ordered by name (then type).
func (c *Cloudflare) ListRecords(ctx context.Context, token, zoneID string) ([]Record, error) {
	if !ValidID(zoneID) {
		return nil, badID()
	}
	var out []Record
	for _, typ := range []RecordType{RecordA, RecordAAAA} {
		for page := 1; page <= maxPages; page++ {
			q := url.Values{"type": {string(typ)}, "page": {strconv.Itoa(page)},
				"per_page": {strconv.Itoa(recordsPerPage)}, "order": {"name"}, "direction": {"asc"}}
			var rs []cfRecord
			info, err := c.do(ctx, token, http.MethodGet, "/zones/"+zoneID+"/dns_records", q, nil, &rs)
			if err != nil {
				return nil, err
			}
			for _, r := range rs {
				if rec := r.record(); rec.Type.Valid() { // the server-side filter is trusted, not relied on
					out = append(out, rec)
				}
			}
			if page >= info.ResultInfo.TotalPages || len(rs) == 0 {
				break
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Type < out[j].Type
	})
	return out, nil
}

// GetRecord reads one record by id. It may be of any type; callers check Type and Name.
func (c *Cloudflare) GetRecord(ctx context.Context, token, zoneID, recordID string) (Record, error) {
	if !ValidID(zoneID) || !ValidID(recordID) {
		return Record{}, badID()
	}
	var r cfRecord
	if _, err := c.do(ctx, token, http.MethodGet, recordPath(zoneID, recordID), nil, nil, &r); err != nil {
		return Record{}, err
	}
	return r.record(), nil
}

// UpdateRecord sets content, proxied and ttl of a record with PATCH, which cannot change its name
// or type, and returns the record as Cloudflare now has it.
func (c *Cloudflare) UpdateRecord(ctx context.Context, token, zoneID, recordID string, u RecordUpdate) (Record, error) {
	if !ValidID(zoneID) || !ValidID(recordID) {
		return Record{}, badID()
	}
	body, err := json.Marshal(map[string]any{"content": u.Content.String(), "proxied": u.Proxied, "ttl": u.TTL})
	if err != nil {
		return Record{}, err
	}
	var r cfRecord
	if _, err := c.do(ctx, token, http.MethodPatch, recordPath(zoneID, recordID), nil, body, &r); err != nil {
		return Record{}, err
	}
	return r.record(), nil
}

func recordPath(zoneID, recordID string) string {
	return "/zones/" + zoneID + "/dns_records/" + recordID
}

// ValidID reports whether id looks like a Cloudflare identifier (hex in practice). Ids end up in
// URL paths, so anything with a slash, dot or other punctuation is refused before a request is
// built: a crafted id must not reach another endpoint with the user's token.
func ValidID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func badID() error {
	return &CloudflareError{Status: http.StatusBadRequest, Message: "invalid zone or record id"}
}

// do performs one request and decodes envelope.result into dst. Every failure is a
// *CloudflareError.
func (c *Cloudflare) do(ctx context.Context, token, method, path string, q url.Values, body []byte, dst any) (envelope, error) {
	if token == "" {
		return envelope{}, &CloudflareError{Message: "no API token"}
	}
	u := *c.base
	u.Path += path
	u.RawPath = ""
	if q != nil {
		u.RawQuery = q.Encode()
	}
	var rdr io.Reader = http.NoBody
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rdr)
	if err != nil {
		return envelope{}, &CloudflareError{Message: "could not build the request"}
	}
	req.Header.Set("Authorization", "Bearer "+token) // the only place the token is used
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "homecore-ddns")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return envelope{}, ctx.Err()
		}
		return envelope{}, &CloudflareError{Message: "could not reach Cloudflare: " + describeTransportError(ctx, err), network: true}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxCloudflareBody+1))
	if err != nil {
		if ctx.Err() != nil {
			return envelope{}, ctx.Err()
		}
		return envelope{}, &CloudflareError{Status: resp.StatusCode, Message: "could not read the Cloudflare response", network: true}
	}
	if len(raw) > maxCloudflareBody {
		return envelope{}, &CloudflareError{Status: resp.StatusCode, Message: "Cloudflare response too large"}
	}

	var env envelope
	jsonErr := json.Unmarshal(raw, &env)
	if resp.StatusCode < 200 || resp.StatusCode > 299 || jsonErr != nil || !env.Success {
		return envelope{}, c.failure(resp, env, jsonErr, token)
	}
	if dst != nil {
		if err := json.Unmarshal(env.Result, dst); err != nil {
			return envelope{}, &CloudflareError{Status: resp.StatusCode, Message: "unexpected Cloudflare response"}
		}
	}
	return env, nil
}

// failure builds the typed error for an HTTP error status or a `success: false` envelope.
func (c *Cloudflare) failure(resp *http.Response, env envelope, jsonErr error, token string) error {
	e := &CloudflareError{Status: resp.StatusCode}
	var msgs []string
	for _, m := range env.Errors {
		if e.Code == 0 {
			e.Code = m.Code
		}
		if m.Message != "" {
			msgs = append(msgs, m.Message)
		}
	}
	switch {
	case len(msgs) > 0:
		e.Message = strings.Join(msgs, "; ")
	case jsonErr != nil && resp.StatusCode >= 200 && resp.StatusCode <= 299:
		e.Message = "unexpected Cloudflare response"
	case resp.StatusCode >= 300 && resp.StatusCode <= 399:
		e.Message = "unexpected redirect"
	default:
		e.Message = http.StatusText(resp.StatusCode)
		if e.Message == "" {
			e.Message = "request failed"
		}
	}
	e.Message = truncate(redact(e.Message, token), maxMessageLen)
	if e.Status == http.StatusTooManyRequests {
		e.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"))
	}
	return e
}

// parseRetryAfter reads delta-seconds (what Cloudflare sends); an absent or unusable value falls
// back to a minute, and nothing waits longer than an hour.
func parseRetryAfter(v string) time.Duration {
	secs, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || secs <= 0 {
		return defaultRateLimitFor
	}
	return min(time.Duration(secs)*time.Second, maxRetryAfter)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// Cut on a rune boundary.
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n] + "..."
}

// parseContent parses a record's content as an address of the record's family.
func parseContent(content string) (netip.Addr, bool) {
	a, err := netip.ParseAddr(strings.TrimSpace(content))
	if err != nil {
		return netip.Addr{}, false
	}
	return a.Unmap(), true
}
