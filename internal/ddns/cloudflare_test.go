package ddns

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/thanhtunguet/homecore/internal/ddns/ddnstest"
)

const testToken = "tok-SECRET-0123456789abcdef"

func newCF(t *testing.T) (*ddnstest.Cloudflare, *Cloudflare) {
	t.Helper()
	f := ddnstest.New(testToken)
	t.Cleanup(f.Close)
	c, err := NewCloudflare(f.URL(), true, nil)
	if err != nil {
		t.Fatal(err)
	}
	return f, c
}

// REQ-DDNS-002: zones are paginated and ordered by name.
func TestListZonesPaginatesAndOrders(t *testing.T) {
	f, c := newCF(t)
	f.MaxPerPage = 2
	for _, n := range []string{"zeta.org", "Alpha.com", "mid.net", "beta.io", "omega.dev"} {
		f.Zones = append(f.Zones, ddnstest.Zone{ID: "z" + strings.ToLower(n[:2]), Name: n})
	}
	zs, err := c.ListZones(context.Background(), testToken)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, z := range zs {
		names = append(names, z.Name)
	}
	if got := strings.Join(names, ","); got != "Alpha.com,beta.io,mid.net,omega.dev,zeta.org" {
		t.Fatalf("zones: %s", got)
	}
	if n := f.Count("GET", "/zones"); n != 3 {
		t.Fatalf("expected 3 pages, made %d requests", n)
	}
	for _, r := range f.Requests() {
		if r.Authorization != "Bearer "+testToken {
			t.Fatalf("authorization header: %q", r.Authorization)
		}
		if strings.Contains(r.Path+r.Query, testToken) {
			t.Fatalf("the token is in the URL: %s?%s", r.Path, r.Query)
		}
	}
}

// REQ-DDNS-002: only A and AAAA records, from both type queries, paginated, ordered by name.
func TestListRecordsMergesTypesAndOrders(t *testing.T) {
	f, c := newCF(t)
	f.MaxPerPage = 2
	f.Zones = []ddnstest.Zone{{ID: "z1", Name: "example.com"}}
	f.Records["z1"] = []ddnstest.Record{
		{ID: "r1", Name: "www.example.com", Type: "A", Content: "8.8.8.8", TTL: 1, Proxied: true},
		{ID: "r2", Name: "alias.example.com", Type: "CNAME", Content: "www.example.com", TTL: 1},
		{ID: "r3", Name: "home.example.com", Type: "AAAA", Content: "2606:4700::1", TTL: 300},
		{ID: "r4", Name: "home.example.com", Type: "A", Content: "8.8.4.4", TTL: 300},
		{ID: "r5", Name: "Api.Example.com.", Type: "A", Content: "1.1.1.1", TTL: 1},
		{ID: "r6", Name: "txt.example.com", Type: "TXT", Content: "hello", TTL: 1},
		{ID: "r7", Name: "zzz.example.com", Type: "A", Content: "9.9.9.9", TTL: 60},
	}
	rs, err := c.ListRecords(context.Background(), testToken, "z1")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range rs {
		got = append(got, fmt.Sprintf("%s/%s", r.Name, r.Type))
	}
	want := "api.example.com/A,home.example.com/A,home.example.com/AAAA,www.example.com/A,zzz.example.com/A"
	if strings.Join(got, ",") != want {
		t.Fatalf("records:\n got %v\nwant %s", got, want)
	}
	if rs[3].ID != "r1" || !rs[3].Proxied || rs[3].TTL != 1 || rs[3].Content != "8.8.8.8" {
		t.Fatalf("record fields: %+v", rs[3])
	}
}

func TestGetAndUpdateRecord(t *testing.T) {
	f, c := newCF(t)
	f.Zones = []ddnstest.Zone{{ID: "z1", Name: "example.com"}}
	f.Records["z1"] = []ddnstest.Record{{ID: "r1", Name: "home.example.com", Type: "A", Content: "8.8.4.4", TTL: 300}}
	ctx := context.Background()

	rec, err := c.GetRecord(ctx, testToken, "z1", "r1")
	if err != nil || rec.Content != "8.8.4.4" || rec.Name != "home.example.com" || rec.Type != RecordA || rec.TTL != 300 {
		t.Fatalf("get: %+v %v", rec, err)
	}
	upd, err := c.UpdateRecord(ctx, testToken, "z1", "r1", RecordUpdate{Content: netip.MustParseAddr("8.8.8.8"), Proxied: true, TTL: 1})
	if err != nil || upd.Content != "8.8.8.8" || !upd.Proxied || upd.TTL != 1 {
		t.Fatalf("update: %+v %v", upd, err)
	}
	stored, _ := f.Record("z1", "r1")
	if stored.Content != "8.8.8.8" || stored.Name != "home.example.com" || stored.Type != "A" {
		t.Fatalf("the fake record after PATCH: %+v", stored)
	}
	reqs := f.Requests()
	last := reqs[len(reqs)-1]
	if last.Method != "PATCH" || strings.Contains(last.Body, `"name"`) || strings.Contains(last.Body, `"type"`) {
		t.Fatalf("PATCH must send content, proxied and ttl only: %+v", last)
	}
}

// REQ-DDNS-002: failures are typed, carry Cloudflare's message and never the token.
func TestCloudflareErrors(t *testing.T) {
	f, c := newCF(t)
	f.Zones = []ddnstest.Zone{{ID: "z1", Name: "example.com"}}
	ctx := context.Background()

	respond := func(status int, body string, h http.Header) {
		f.SetFail(func(*http.Request) (int, string, http.Header) { return status, body, h })
	}
	call := func() error { _, err := c.ListZones(ctx, testToken); return err }
	asCF := func(t *testing.T, err error) *CloudflareError {
		t.Helper()
		var ce *CloudflareError
		if !errors.As(err, &ce) {
			t.Fatalf("not a CloudflareError: %v", err)
		}
		if strings.Contains(err.Error(), testToken) || strings.Contains(ce.Message, testToken) {
			t.Fatalf("the token leaked into the error: %v", err)
		}
		return ce
	}

	t.Run("wrong token is 403 with Cloudflare's reason", func(t *testing.T) {
		f.SetFail(nil)
		bad := NewCloudflareForTest(t, f.URL())
		_, err := bad.ListZones(ctx, "not-the-token")
		ce := asCF(t, err)
		if ce.Status != 403 || !ce.AuthFailure() || ce.Transient() || !strings.Contains(ce.Message, "Invalid access token") || ce.Code != 9109 {
			t.Fatalf("%+v", ce)
		}
	})
	t.Run("401", func(t *testing.T) {
		respond(401, `{"success":false,"errors":[{"code":10000,"message":"Authentication error"}],"result":null}`, nil)
		ce := asCF(t, call())
		if !ce.AuthFailure() || ce.Transient() || ce.Message != "Authentication error" {
			t.Fatalf("%+v", ce)
		}
	})
	t.Run("429 with Retry-After", func(t *testing.T) {
		respond(429, `{"success":false,"errors":[{"code":971,"message":"Rate limited"}]}`, http.Header{"Retry-After": {"37"}})
		ce := asCF(t, call())
		if !ce.RateLimited() || !ce.Transient() || ce.RetryAfter != 37*time.Second {
			t.Fatalf("%+v", ce)
		}
	})
	t.Run("429 without Retry-After and an absurd one", func(t *testing.T) {
		respond(429, `{}`, nil)
		if ce := asCF(t, call()); ce.RetryAfter != defaultRateLimitFor {
			t.Fatalf("%+v", ce)
		}
		respond(429, `{}`, http.Header{"Retry-After": {"999999"}})
		if ce := asCF(t, call()); ce.RetryAfter != maxRetryAfter {
			t.Fatalf("%+v", ce)
		}
	})
	t.Run("5xx with an HTML body is transient with a generic message", func(t *testing.T) {
		respond(502, `<html>bad gateway</html>`, nil)
		ce := asCF(t, call())
		if !ce.Transient() || ce.Message != "Bad Gateway" {
			t.Fatalf("%+v", ce)
		}
	})
	t.Run("success:false inside a 200", func(t *testing.T) {
		respond(200, `{"success":false,"errors":[{"code":1,"message":"Something is off"},{"code":2,"message":"and again"}],"result":null}`, nil)
		ce := asCF(t, call())
		if ce.Message != "Something is off; and again" || ce.Code != 1 || ce.Transient() {
			t.Fatalf("%+v", ce)
		}
	})
	t.Run("2xx that is not JSON", func(t *testing.T) {
		respond(200, `<html>captive portal</html>`, nil)
		ce := asCF(t, call())
		if ce.Message != "unexpected Cloudflare response" {
			t.Fatalf("%+v", ce)
		}
	})
	t.Run("redirects are not followed", func(t *testing.T) {
		respond(302, ``, http.Header{"Location": {"http://evil.example/steal"}})
		ce := asCF(t, call())
		if ce.Status != 302 || ce.Message != "unexpected redirect" {
			t.Fatalf("%+v", ce)
		}
	})
	t.Run("a message that echoes the token is redacted", func(t *testing.T) {
		respond(400, `{"success":false,"errors":[{"code":6003,"message":"Invalid request headers: Authorization: Bearer `+testToken+`"}]}`, nil)
		ce := asCF(t, call())
		if strings.Contains(ce.Message, testToken) || !strings.Contains(ce.Message, "[redacted]") {
			t.Fatalf("%+v", ce)
		}
	})
	t.Run("an overlong message is cut", func(t *testing.T) {
		respond(400, `{"success":false,"errors":[{"code":1,"message":"`+strings.Repeat("é", 1000)+`"}]}`, nil)
		if ce := asCF(t, call()); len(ce.Message) > maxMessageLen+3 {
			t.Fatalf("message length %d", len(ce.Message))
		}
	})
	t.Run("unreachable server is a transient network error without the URL", func(t *testing.T) {
		dead := ddnstest.New(testToken)
		url := dead.URL()
		dead.Close()
		cl := NewCloudflareForTest(t, url)
		_, err := cl.ListZones(ctx, testToken)
		ce := asCF(t, err)
		if ce.Status != 0 || !ce.Transient() || strings.Contains(err.Error(), "127.0.0.1") {
			t.Fatalf("%+v / %v", ce, err)
		}
	})
	t.Run("cancelled context is returned as is", func(t *testing.T) {
		f.SetFail(nil)
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := c.ListZones(cctx, testToken); !errors.Is(err, context.Canceled) {
			t.Fatalf("%v", err)
		}
	})
	t.Run("unknown record is a 404", func(t *testing.T) {
		f.SetFail(nil)
		_, err := c.GetRecord(ctx, testToken, "z1", "nope")
		if ce := asCF(t, err); !ce.NotFound() || ce.Transient() || ce.Message != "Record does not exist." {
			t.Fatalf("%+v", ce)
		}
	})
}

// NewCloudflareForTest returns a client for a fake server's URL.
func NewCloudflareForTest(t *testing.T, url string) *Cloudflare {
	t.Helper()
	c, err := NewCloudflare(url, true, &http.Client{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Ids end up in URL paths: anything but plain identifiers is refused before a request is built.
func TestClientRefusesPathTraversalInIDs(t *testing.T) {
	f, c := newCF(t)
	for _, id := range []string{"", "../user/tokens", "a/b", "a b", "a?x=1", "z1%2f..", strings.Repeat("a", 65)} {
		if _, err := c.GetRecord(context.Background(), testToken, id, "r1"); err == nil {
			t.Errorf("zone id %q accepted", id)
		}
		if _, err := c.GetRecord(context.Background(), testToken, "z1", id); err == nil {
			t.Errorf("record id %q accepted", id)
		}
		if _, err := c.ListRecords(context.Background(), testToken, id); err == nil {
			t.Errorf("list: zone id %q accepted", id)
		}
	}
	if n := len(f.Requests()); n != 0 {
		t.Fatalf("%d requests reached the server", n)
	}
}

func TestNewCloudflareValidatesTheURL(t *testing.T) {
	for _, u := range []string{"http://api.example.com", "ftp://x", "https://u:p@api.example.com", "https://api.example.com/v4?x=1", "https://", "://bad"} {
		if _, err := NewCloudflare(u, false, nil); err == nil {
			t.Errorf("accepted %q", u)
		}
	}
	if c, err := NewCloudflare("", false, nil); err != nil || c.base.String() != DefaultCloudflareURL {
		t.Errorf("default: %v", err)
	}
	if _, err := NewCloudflare("http://127.0.0.1:1/client/v4/", true, nil); err != nil {
		t.Errorf("http with allow: %v", err)
	}
}

func TestEmptyTokenIsRefusedLocally(t *testing.T) {
	f, c := newCF(t)
	if _, err := c.ListZones(context.Background(), ""); err == nil {
		t.Fatal("empty token accepted")
	}
	if len(f.Requests()) != 0 {
		t.Fatal("a request without a token was sent")
	}
}
