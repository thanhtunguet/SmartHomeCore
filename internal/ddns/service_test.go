package ddns

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thanhtunguet/homecore/internal/ddns/ddnstest"
)

func isValidation(err error) bool {
	var v *ValidationError
	return errors.As(err, &v)
}

// REQ-DDNS-004: the token is sealed at rest, has_api_token is the only trace, record_id is stored.
func TestCreateSealsTheTokenAndStoresTheRecordID(t *testing.T) {
	e := newEnv(t)
	c := e.mk("Home", "r1", func(in *CreateInput) { in.RecordName = "Home.Example.com." })
	if c.ID == "" || !c.HasToken || c.RecordID != "r1" || c.RecordName != "home.example.com" || c.RecordType != RecordA ||
		c.Interval != 300*time.Second || !c.Enabled || c.Proxied || c.TTL != 300 || c.LastIP != "" || c.LastCheckedAt != nil {
		t.Fatalf("created: %+v", c)
	}
	var sealed []byte
	var recID string
	if err := e.db.QueryRow(`SELECT api_token_sealed, cf_record_id FROM ddns_configs WHERE id = ?`, c.ID).Scan(&sealed, &recID); err != nil {
		t.Fatal(err)
	}
	if recID != "r1" || len(sealed) == 0 || bytes.Contains(sealed, []byte(testToken)) {
		t.Fatalf("stored token: %d bytes, plaintext inside: %v, record id %q", len(sealed), bytes.Contains(sealed, []byte(testToken)), recID)
	}
	// Round trip through the same AAD works; any other AAD does not.
	st, err := e.repo.Get(context.Background(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := e.svc.openToken(st.TokenSealed); err != nil || got != testToken {
		t.Fatalf("open: %q %v", got, err)
	}
	if _, err := e.svc.sealer.Open(st.TokenSealed, "ddns_configs.something_else"); err == nil {
		t.Fatal("the sealed token opens under a different AAD")
	}
	// The database file never contains the plaintext token.
	if n := e.count("ddns_configs"); n != 1 {
		t.Fatalf("rows: %d", n)
	}
	var hits int
	if err := e.db.QueryRow(`SELECT count(*) FROM ddns_configs WHERE CAST(api_token_sealed AS TEXT) LIKE '%' || ? || '%'`, testToken).Scan(&hits); err != nil || hits != 0 {
		t.Fatalf("plaintext token found in the table: %d %v", hits, err)
	}
}

func TestCreateNormalisesProxiedTTLAndPaddedToken(t *testing.T) {
	e := newEnv(t)
	c := e.mk("Home", "r1", func(in *CreateInput) {
		in.Proxied, in.TTL, in.APIToken = ptrOf(true), ptrOf(600), "  "+testToken+"\n"
	})
	if !c.Proxied || c.TTL != 1 {
		t.Fatalf("a proxied record is always ttl 1 (automatic): %+v", c)
	}
	if res := e.mustSync(c.ID); res.Result == ResultFailed {
		t.Fatalf("the trimmed token must work: %+v", res)
	}
}

func TestCreateValidation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	base := CreateInput{Name: "Home", ZoneID: zone, RecordID: "r1", RecordName: "home.example.com", RecordType: RecordA, APIToken: testToken}
	cases := map[string]func(*CreateInput){
		"empty name":       func(in *CreateInput) { in.Name = "  " },
		"name too long":    func(in *CreateInput) { in.Name = strings.Repeat("n", 101) },
		"no zone":          func(in *CreateInput) { in.ZoneID = "" },
		"zone with slash":  func(in *CreateInput) { in.ZoneID = "../x" },
		"no record id":     func(in *CreateInput) { in.RecordID = "" },
		"record id path":   func(in *CreateInput) { in.RecordID = "r1/../../x" },
		"no record name":   func(in *CreateInput) { in.RecordName = "" },
		"record name long": func(in *CreateInput) { in.RecordName = strings.Repeat("a", 254) },
		"CNAME":            func(in *CreateInput) { in.RecordType = "CNAME" },
		"no type":          func(in *CreateInput) { in.RecordType = "" },
		"ttl 0":            func(in *CreateInput) { in.TTL = ptrOf(0) },
		"ttl too big":      func(in *CreateInput) { in.TTL = ptrOf(86401) },
		"interval 59":      func(in *CreateInput) { in.IntervalSeconds = ptrOf(59) },
		"interval too big": func(in *CreateInput) { in.IntervalSeconds = ptrOf(86401) },
		"no token":         func(in *CreateInput) { in.APIToken = "" },
		"blank token":      func(in *CreateInput) { in.APIToken = "   " },
		"token with space": func(in *CreateInput) { in.APIToken = "abc def" },
		"token with ctl":   func(in *CreateInput) { in.APIToken = "abc\x00def" },
		"token non ascii":  func(in *CreateInput) { in.APIToken = "tökén" },
		"token huge":       func(in *CreateInput) { in.APIToken = strings.Repeat("t", 513) },
	}
	for name, mod := range cases {
		in := base
		mod(&in)
		if _, err := e.svc.Create(ctx, in); !isValidation(err) {
			t.Errorf("%s: got %v, want a ValidationError", name, err)
		}
	}
	for _, ok := range []func(*CreateInput){
		func(in *CreateInput) { in.IntervalSeconds, in.TTL = ptrOf(60), ptrOf(1) },
		func(in *CreateInput) { in.IntervalSeconds, in.TTL = ptrOf(86400), ptrOf(86400) },
		func(in *CreateInput) { in.Name = strings.Repeat("é", 100) },
	} {
		in := base
		in.RecordName = "home.example.com"
		ok(&in)
		if _, err := e.svc.Create(ctx, in); err != nil && !errors.Is(err, ErrDuplicate) {
			t.Errorf("boundary value rejected: %v", err)
		}
		_ = e.repo.Delete(ctx, mustID(t, e))
	}
	if n := len(e.cf.Requests()); n != e.cf.Count("GET", "/r1") {
		t.Fatalf("validation failures must not reach Cloudflare: %d requests, %d for r1", n, e.cf.Count("GET", "/r1"))
	}
}

func mustID(t *testing.T, e *env) string {
	t.Helper()
	cs, err := e.svc.List(context.Background())
	if err != nil || len(cs) == 0 {
		return ""
	}
	return cs[0].ID
}

// REQ-DDNS-004: the target is checked with Cloudflare when saved.
func TestCreateVerifiesTheRecordWithCloudflare(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	base := CreateInput{Name: "Home", ZoneID: zone, RecordID: "r1", RecordName: "home.example.com", RecordType: RecordA, APIToken: testToken}
	reject := func(label string, mod func(*CreateInput), wantInMsg string) {
		t.Helper()
		in := base
		mod(&in)
		_, err := e.svc.Create(ctx, in)
		if !isValidation(err) || !strings.Contains(err.Error(), wantInMsg) {
			t.Errorf("%s: got %v, want a ValidationError containing %q", label, err, wantInMsg)
		}
		if strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), "SECRET-OTHER") {
			t.Errorf("%s: a token is in the error: %v", label, err)
		}
	}
	reject("wrong token", func(in *CreateInput) { in.APIToken = "tok-SECRET-OTHER" }, "rejected the API token")
	reject("unknown record", func(in *CreateInput) { in.RecordID = "nope" }, "does not have this record")
	reject("unknown zone", func(in *CreateInput) { in.ZoneID = "zzz" }, "does not have this record")
	reject("renamed record", func(in *CreateInput) { in.RecordName = "other.example.com" }, "choose the record again")
	reject("retyped record", func(in *CreateInput) { in.RecordType = RecordAAAA }, "choose the record again")
	reject("a CNAME id", func(in *CreateInput) { in.RecordID, in.RecordName = "r4", "www.example.com" }, "choose the record again")
	if e.count("ddns_configs") != 0 {
		t.Fatal("a rejected configuration was stored")
	}

	// Cloudflare that cannot answer does not block saving: the first sync reports the problem.
	for label, status := range map[string]int{"503": 503, "429": 429} {
		e.cf.SetFail(func(*http.Request) (int, string, http.Header) { return status, `{"success":false,"errors":[]}`, nil })
		in := base
		in.RecordName = "home.example.com"
		in.RecordID = "r1"
		c, err := e.svc.Create(ctx, in)
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		if !strings.Contains(e.logs.String(), "could not verify") {
			t.Errorf("%s: the skipped verification is not logged", label)
		}
		_ = e.svc.Delete(ctx, c.ID)
	}
}

func TestCreateDuplicates(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.mk("Home", "r1")
	in := CreateInput{Name: "Again", ZoneID: zone, RecordID: "r1", RecordName: "home.example.com", RecordType: RecordA, APIToken: testToken}
	if _, err := e.svc.Create(ctx, in); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("same record twice: %v", err)
	}
	// Two names pointing at different records are fine, and the display name need not be unique.
	e.mk("Home", "r3")
	// The same record id with another (unverified) name is caught by the partial unique index.
	e.cf.SetFail(func(*http.Request) (int, string, http.Header) { return 503, `{}`, nil })
	in.RecordName = "elsewhere.example.com"
	if _, err := e.svc.Create(ctx, in); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("same record id, other name: %v", err)
	}
}

// REQ-DDNS-005: PATCH rules.
func TestUpdateRules(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c := e.mk("Home", "r1")
	e.mustSync(c.ID) // gives it some observed state

	upd := func(in UpdateInput) (Config, error) { return e.svc.Update(ctx, c.ID, in) }
	s := func(v string) *string { return &v }

	t.Run("simple fields keep the observed state", func(t *testing.T) {
		got, err := upd(UpdateInput{Name: s("Renamed"), IntervalSeconds: ptrOf(120), Enabled: ptrOf(false)})
		if err != nil || got.Name != "Renamed" || got.Interval != 2*time.Minute || got.Enabled || got.LastIP != "8.8.8.8" || got.LastSyncedAt == nil {
			t.Fatalf("%+v %v", got, err)
		}
		if !got.UpdatedAt.After(c.UpdatedAt) {
			t.Fatal("updated_at did not move")
		}
	})
	t.Run("an empty update changes nothing", func(t *testing.T) {
		before := e.get(c.ID)
		got, err := upd(UpdateInput{})
		if err != nil || !got.UpdatedAt.Equal(before.UpdatedAt) {
			t.Fatalf("%+v %v", got, err)
		}
	})
	t.Run("zone_id change without record_id is rejected", func(t *testing.T) {
		if _, err := upd(UpdateInput{ZoneID: s("z2")}); !isValidation(err) || !strings.Contains(err.Error(), "record_id is required") {
			t.Fatalf("%v", err)
		}
	})
	t.Run("record_name and record_type changes need record_id too", func(t *testing.T) {
		if _, err := upd(UpdateInput{RecordName: s("nas.example.com")}); !isValidation(err) || !strings.Contains(err.Error(), "record_id is required") {
			t.Fatalf("name: %v", err)
		}
		at := RecordAAAA
		if _, err := upd(UpdateInput{RecordType: &at}); !isValidation(err) || !strings.Contains(err.Error(), "record_id is required") {
			t.Fatalf("type: %v", err)
		}
	})
	t.Run("the same zone_id is not a change", func(t *testing.T) {
		if _, err := upd(UpdateInput{ZoneID: s(zone), RecordName: s("HOME.example.com.")}); err != nil {
			t.Fatalf("%v", err)
		}
	})
	t.Run("a stale remaining field is caught by the verification", func(t *testing.T) {
		// New record id (nas) but the old name: Cloudflare says that record is nas, not home.
		if _, err := upd(UpdateInput{RecordID: s("r3")}); !isValidation(err) || !strings.Contains(err.Error(), "choose the record again") {
			t.Fatalf("%v", err)
		}
		if got := e.get(c.ID); got.RecordID != "r1" {
			t.Fatalf("a rejected update was applied: %+v", got)
		}
	})
	t.Run("a new token is verified before it replaces the old one", func(t *testing.T) {
		before := e.cf.Count("GET", "/r1")
		if _, err := upd(UpdateInput{APIToken: s("tok-SECRET-NEW")}); !isValidation(err) || !strings.Contains(err.Error(), "rejected the API token") {
			t.Fatalf("%v", err)
		}
		if e.cf.Count("GET", "/r1") != before+1 {
			t.Fatal("the new token was not checked with Cloudflare")
		}
		if res := e.mustSync(c.ID); res.Result == ResultFailed {
			t.Fatalf("the old token must still work: %+v", res)
		}
		for _, bad := range []string{"", "  ", "a b"} {
			if _, err := upd(UpdateInput{APIToken: s(bad)}); !isValidation(err) {
				t.Fatalf("token %q: %v", bad, err)
			}
		}
	})
	t.Run("retargeting to another record resets the observed state", func(t *testing.T) {
		got, err := upd(UpdateInput{RecordID: s("r3"), RecordName: s("nas.example.com")})
		if err != nil || got.RecordID != "r3" || got.RecordName != "nas.example.com" {
			t.Fatalf("%+v %v", got, err)
		}
		if got.LastIP != "" || got.DetectedIP != "" || got.DNSValue != "" || got.LastCheckedAt != nil || got.LastSyncedAt != nil || got.LastError != "" {
			t.Fatalf("the state of the old record survived: %+v", got)
		}
		if res := e.mustSync(c.ID); res.Result != ResultUpdated {
			t.Fatalf("%+v", res)
		}
		if rec, _ := e.cf.Record(zone, "r3"); rec.Content != "8.8.8.8" {
			t.Fatalf("the new record was not updated: %+v", rec)
		}
		if rec, _ := e.cf.Record(zone, "r1"); rec.Content != "8.8.8.8" { // updated by the first sync, untouched since
			t.Fatalf("the old record was touched: %+v", rec)
		}
	})
	t.Run("validation and duplicates", func(t *testing.T) {
		for name, in := range map[string]UpdateInput{
			"name":     {Name: s(" ")},
			"ttl":      {TTL: ptrOf(0)},
			"interval": {IntervalSeconds: ptrOf(30)},
			"type":     {RecordID: s("r3"), RecordType: typePtr("MX")},
		} {
			if _, err := upd(in); !isValidation(err) {
				t.Errorf("%s: %v", name, err)
			}
		}
		other := e.mk("Cam", "r5")
		if _, err := e.svc.Update(ctx, other.ID, UpdateInput{RecordID: s("r3"), RecordName: s("nas.example.com")}); !errors.Is(err, ErrDuplicate) {
			t.Fatalf("a record already managed by another configuration: %v", err)
		}
	})
	t.Run("unknown id", func(t *testing.T) {
		if _, err := e.svc.Update(ctx, "0193a0b2-7c4e-7f00-8000-000000000001", UpdateInput{Name: s("x")}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%v", err)
		}
	})
}

func typePtr(s string) *RecordType { t := RecordType(s); return &t }

func TestProxiedForcesAutomaticTTLOnUpdate(t *testing.T) {
	e := newEnv(t)
	c := e.mk("Home", "r1")
	got, err := e.svc.Update(context.Background(), c.ID, UpdateInput{Proxied: ptrOf(true), TTL: ptrOf(600)})
	if err != nil || !got.Proxied || got.TTL != 1 {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestDeleteCascadesHistory(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a, b := e.mk("A", "r1"), e.mk("B", "r3")
	e.mustSync(a.ID)
	e.mustSync(b.ID)
	e.clock.Advance(time.Second)
	e.mustSync(a.ID)
	if e.count("ddns_sync_history") != 3 {
		t.Fatalf("history rows: %d", e.count("ddns_sync_history"))
	}
	if err := e.svc.Delete(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if got := e.count("ddns_sync_history"); got != 1 {
		t.Fatalf("history rows after delete: %d (want only B's)", got)
	}
	if _, err := e.svc.Get(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v", err)
	}
	if err := e.svc.Delete(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	if _, err := e.svc.Sync(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("sync of a deleted config: %v", err)
	}
}

func TestListIsOrderedByNameIgnoringCase(t *testing.T) {
	e := newEnv(t)
	e.mk("zeta", "r1")
	e.mk("Alpha", "r3")
	e.mk("beta", "r5")
	cs, err := e.svc.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range cs {
		names = append(names, c.Name)
	}
	if strings.Join(names, ",") != "Alpha,beta,zeta" {
		t.Fatalf("order: %v", names)
	}
}

// REQ-API-020: lookups take exactly one credential source and store nothing.
func TestLookups(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c := e.mk("Home", "r1")
	before := e.count("ddns_configs")

	zs, err := e.svc.LookupZones(ctx, Credentials{Token: testToken})
	if err != nil || len(zs) != 2 || zs[0].Name != "example.com" {
		t.Fatalf("zones by token: %+v %v", zs, err)
	}
	if zs, err = e.svc.LookupZones(ctx, Credentials{ConfigID: c.ID}); err != nil || len(zs) != 2 {
		t.Fatalf("zones by config: %+v %v", zs, err)
	}
	rs, err := e.svc.LookupRecords(ctx, zone, Credentials{ConfigID: c.ID})
	if err != nil || len(rs) != 4 { // r1, r2, r3, r5; the CNAME is not offered
		t.Fatalf("records: %+v %v", rs, err)
	}
	for _, r := range e.cf.Requests() {
		if r.Authorization != "Bearer "+testToken {
			t.Fatalf("a lookup used the wrong token: %q", r.Authorization)
		}
	}

	for name, cr := range map[string]Credentials{
		"neither": {}, "both": {Token: testToken, ConfigID: c.ID}, "bad token chars": {Token: "a b"},
	} {
		if _, err := e.svc.LookupZones(ctx, cr); !isValidation(err) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := e.svc.LookupZones(ctx, Credentials{ConfigID: "0193a0b2-7c4e-7f00-8000-000000000001"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown config: %v", err)
	}
	if _, err := e.svc.LookupRecords(ctx, "../zones", Credentials{Token: testToken}); !isValidation(err) {
		t.Errorf("bad zone id: %v", err)
	}
	// A wrong token is Cloudflare's 403, typed, with its reason.
	_, err = e.svc.LookupZones(ctx, Credentials{Token: "tok-SECRET-OTHER"})
	var ce *CloudflareError
	if !errors.As(err, &ce) || ce.Status != 403 || strings.Contains(err.Error(), "SECRET-OTHER") {
		t.Errorf("wrong token: %v", err)
	}
	// An unknown zone is Cloudflare's 404.
	if _, err = e.svc.LookupRecords(ctx, "nozone", Credentials{Token: testToken}); !errors.As(err, &ce) || !ce.NotFound() {
		t.Errorf("unknown zone: %v", err)
	}
	if e.count("ddns_configs") != before || e.count("ddns_sync_history") != 0 {
		t.Fatal("a lookup stored something")
	}
}

// ---- sync -------------------------------------------------------------------------------------

// REQ-DDNS-006: update only when different, persist everything, one history row per attempt.
func TestSyncUpdatesThenReportsUnchanged(t *testing.T) {
	e := newEnv(t)
	c := e.mk("Home", "r1")

	res := e.mustSync(c.ID)
	if res.Result != ResultUpdated || res.IP != "8.8.8.8" || res.Error != "" || !res.At.Equal(e.clock.Now()) {
		t.Fatalf("first sync: %+v", res)
	}
	if rec, _ := e.cf.Record(zone, "r1"); rec.Content != "8.8.8.8" || rec.Name != "home.example.com" || rec.Type != "A" || rec.TTL != 300 {
		t.Fatalf("record after update: %+v", rec)
	}
	got := e.get(c.ID)
	if got.LastIP != "8.8.8.8" || got.DetectedIP != "8.8.8.8" || got.DNSValue != "8.8.8.8" || got.LastError != "" ||
		got.LastCheckedAt == nil || got.LastSyncedAt == nil || got.LastErrorAt != nil || !got.UpdatedAt.Equal(c.UpdatedAt) {
		t.Fatalf("state: %+v", got)
	}

	e.clock.Advance(5 * time.Minute)
	patches := e.cf.Count("PATCH", "/r1")
	res = e.mustSync(c.ID)
	if res.Result != ResultUnchanged || e.cf.Count("PATCH", "/r1") != patches {
		t.Fatalf("second sync: %+v (patches %d -> %d)", res, patches, e.cf.Count("PATCH", "/r1"))
	}
	if got = e.get(c.ID); !got.LastCheckedAt.Equal(e.clock.Now()) || !got.LastSyncedAt.Equal(e.clock.Now()) {
		t.Fatalf("an unchanged sync still stamps last_checked_at and last_synced_at: %+v", got)
	}

	e.ip.set("8.8.4.5", nil)
	e.clock.Advance(time.Minute)
	if res = e.mustSync(c.ID); res.Result != ResultUpdated || res.IP != "8.8.4.5" {
		t.Fatalf("third sync: %+v", res)
	}
	if rec, _ := e.cf.Record(zone, "r1"); rec.Content != "8.8.4.5" {
		t.Fatalf("%+v", rec)
	}
	h := e.history(c.ID)
	if len(h) != 3 || h[0].Result != ResultUpdated || h[1].Result != ResultUnchanged || h[2].Result != ResultUpdated || h[0].IP != "8.8.4.5" {
		t.Fatalf("history (newest first): %+v", h)
	}
}

func TestSyncDetectsDriftAtCloudflare(t *testing.T) {
	e := newEnv(t)
	c := e.mk("Home", "r1")
	e.mustSync(c.ID)
	// Someone edits the record in the dashboard: the next sync corrects it even though last_ip is unchanged.
	rec, _ := e.cf.Record(zone, "r1")
	rec.Content = "1.2.3.4"
	e.cf.Set(zone, rec)
	res := e.mustSync(c.ID)
	if res.Result != ResultUpdated {
		t.Fatalf("%+v", res)
	}
	if got := e.get(c.ID); got.DNSValue != "8.8.8.8" {
		t.Fatalf("%+v", got)
	}
}

func TestSyncProxiedAndTTLAreDesiredState(t *testing.T) {
	e := newEnv(t)
	c := e.mk("Home", "r1", func(in *CreateInput) { in.Proxied, in.TTL = ptrOf(true), ptrOf(1) })
	if res := e.mustSync(c.ID); res.Result != ResultUpdated {
		t.Fatalf("proxied flag differs at Cloudflare: %+v", res)
	}
	if rec, _ := e.cf.Record(zone, "r1"); !rec.Proxied || rec.TTL != 1 || rec.Content != "8.8.8.8" {
		t.Fatalf("%+v", rec)
	}
	patches := e.cf.Count("PATCH", "/r1")
	e.clock.Advance(time.Minute)
	if res := e.mustSync(c.ID); res.Result != ResultUnchanged || e.cf.Count("PATCH", "/r1") != patches {
		t.Fatalf("a proxied record must not be patched every cycle: %+v", res)
	}
}

func TestSyncAAAAUsesTheIPv6Address(t *testing.T) {
	e := newEnv(t)
	c := e.mk("Home6", "r2")
	if res := e.mustSync(c.ID); res.Result != ResultUpdated || res.IP != "2606:4700:4700::1111" {
		t.Fatalf("%+v", res)
	}
	if rec, _ := e.cf.Record(zone, "r2"); rec.Content != "2606:4700:4700::1111" {
		t.Fatalf("%+v", rec)
	}
	// The same address written differently is not a change.
	rec, _ := e.cf.Record(zone, "r2")
	rec.Content = "2606:4700:4700:0:0:0:0:1111"
	e.cf.Set(zone, rec)
	patches := e.cf.Count("PATCH", "")
	e.clock.Advance(time.Minute)
	if res := e.mustSync(c.ID); res.Result != ResultUnchanged || e.cf.Count("PATCH", "") != patches {
		t.Fatalf("%+v", res)
	}
}

// REQ-DDNS-006: the record fetched by id must still be the configured type and name; otherwise it is
// an error and nothing is written.
func TestSyncNeverUpdatesTheWrongRecord(t *testing.T) {
	e := newEnv(t)
	c := e.mk("Home", "r1")
	e.mustSync(c.ID)
	patches := e.cf.Count("PATCH", "")

	for label, edit := range map[string]func(*ddnstest.Record){
		"renamed":  func(r *ddnstest.Record) { r.Name = "someone-else.example.com" },
		"retyped":  func(r *ddnstest.Record) { r.Type, r.Content = "CNAME", "elsewhere.example.com" },
		"re-typed": func(r *ddnstest.Record) { r.Type, r.Content = "AAAA", "2606:4700::1" },
	} {
		rec, _ := e.cf.Record(zone, "r1")
		orig := rec
		edit(&rec)
		e.cf.Set(zone, rec)
		e.clock.Advance(time.Minute)
		res := e.mustSync(c.ID)
		if res.Result != ResultFailed || !strings.Contains(res.Error, "does not match") || IsTransient(res.Cause) || !errors.Is(res.Cause, ErrRecordMismatch) {
			t.Errorf("%s: %+v", label, res)
		}
		if e.cf.Count("PATCH", "") != patches {
			t.Fatalf("%s: the record was written to", label)
		}
		got := e.get(c.ID)
		if got.LastError == "" || got.LastErrorAt == nil || got.LastIP != "8.8.8.8" {
			t.Errorf("%s: state %+v", label, got)
		}
		e.cf.Set(zone, orig)
	}
	// Once the record is right again the failure clears.
	e.clock.Advance(time.Minute)
	if res := e.mustSync(c.ID); res.Result != ResultUnchanged {
		t.Fatalf("%+v", res)
	}
	if got := e.get(c.ID); got.LastError != "" || got.LastErrorAt != nil {
		t.Fatalf("the error did not clear: %+v", got)
	}
}

func TestSyncFailures(t *testing.T) {
	e := newEnv(t)
	c := e.mk("Home", "r1")
	e.mustSync(c.ID)

	t.Run("providers disagree: transient, nothing written, previous state kept", func(t *testing.T) {
		e.ip.set("", quorumFailure())
		e.clock.Advance(time.Minute)
		patches := e.cf.Count("PATCH", "")
		res := e.mustSync(c.ID)
		if res.Result != ResultFailed || !IsTransient(res.Cause) || !strings.Contains(res.Error, "at least 2 must agree") || res.IP != "" {
			t.Fatalf("%+v", res)
		}
		got := e.get(c.ID)
		if e.cf.Count("PATCH", "") != patches || got.LastIP != "8.8.8.8" || got.DetectedIP != "8.8.8.8" || got.DNSValue != "8.8.8.8" ||
			got.LastError == "" || !got.LastErrorAt.Equal(e.clock.Now()) || !got.LastCheckedAt.Equal(e.clock.Now()) {
			t.Fatalf("state: %+v", got)
		}
		if h := e.history(c.ID); h[0].Result != ResultFailed || h[0].Error == "" || h[0].IP != "" {
			t.Fatalf("history: %+v", h[0])
		}
	})
	e.ip.set("8.8.8.8", nil)

	t.Run("token revoked: permanent", func(t *testing.T) {
		e.cf.Token = "tok-SECRET-ROTATED"
		e.clock.Advance(time.Minute)
		res := e.mustSync(c.ID)
		if res.Result != ResultFailed || IsTransient(res.Cause) || !strings.Contains(res.Error, "Invalid access token") || res.IP != "8.8.8.8" {
			t.Fatalf("%+v", res)
		}
		e.cf.Token = testToken
	})
	t.Run("record deleted at Cloudflare: permanent", func(t *testing.T) {
		rs := e.cf.Records[zone]
		e.cf.Records[zone] = nil
		res := e.mustSync(c.ID)
		if res.Result != ResultFailed || IsTransient(res.Cause) || !strings.Contains(res.Error, "Record does not exist") {
			t.Fatalf("%+v", res)
		}
		e.cf.Records[zone] = rs
	})
	t.Run("Cloudflare down: transient", func(t *testing.T) {
		e.cf.SetFail(func(*http.Request) (int, string, http.Header) { return 503, `<html>`, nil })
		res := e.mustSync(c.ID)
		if res.Result != ResultFailed || !IsTransient(res.Cause) {
			t.Fatalf("%+v", res)
		}
		e.cf.SetFail(nil)
	})
	t.Run("update refused after the read succeeded", func(t *testing.T) {
		e.ip.set("8.8.4.9", nil)
		e.cf.SetFail(func(r *http.Request) (int, string, http.Header) {
			if r.Method == "PATCH" {
				return 400, `{"success":false,"errors":[{"code":9005,"message":"Content for A record is invalid"}]}`, nil
			}
			return 0, "", nil
		})
		res := e.mustSync(c.ID)
		if res.Result != ResultFailed || IsTransient(res.Cause) || !strings.Contains(res.Error, "Content for A record is invalid") || res.IP != "8.8.4.9" {
			t.Fatalf("%+v", res)
		}
		e.cf.SetFail(nil)
	})
	t.Run("a config without record_id (legacy row) says what to do", func(t *testing.T) {
		if _, err := e.db.Exec(`UPDATE ddns_configs SET cf_record_id = '' WHERE id = ?`, c.ID); err != nil {
			t.Fatal(err)
		}
		res := e.mustSync(c.ID)
		if res.Result != ResultFailed || IsTransient(res.Cause) || !strings.Contains(res.Error, "choose the record") {
			t.Fatalf("%+v", res)
		}
	})

	// No token anywhere: not in the stored error, the history, the logs.
	var errs, hist string
	_ = e.db.QueryRow(`SELECT group_concat(last_error) FROM ddns_configs`).Scan(&errs)
	_ = e.db.QueryRow(`SELECT group_concat(error) FROM ddns_sync_history`).Scan(&hist)
	for _, secret := range []string{testToken, "SECRET-ROTATED"} {
		if strings.Contains(errs, secret) || strings.Contains(hist, secret) || strings.Contains(e.logs.String(), secret) {
			t.Fatalf("secret %q leaked (last_error=%q history=%q)", secret, errs, hist)
		}
	}
}

func TestSyncCancelledRecordsNothing(t *testing.T) {
	e := newEnv(t)
	c := e.mk("Home", "r1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.svc.Sync(ctx, c.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
	if e.count("ddns_sync_history") != 0 {
		t.Fatal("a cancelled sync wrote history")
	}
}

// REQ-DDNS-006: a sync that raced with an edit is dropped instead of describing the wrong record.
func TestSyncResultIsDroppedWhenTheConfigChangedMeanwhile(t *testing.T) {
	e := newEnv(t)
	c := e.mk("Home", "r1")
	e.ip.gate = make(chan struct{})
	done := make(chan SyncResult, 1)
	go func() { done <- func() SyncResult { r, _ := e.svc.Sync(context.Background(), c.ID); return r }() }()
	for i := 0; ; i++ { // wait until the sync is inside discovery
		e.ip.mu.Lock()
		n := e.ip.calls
		e.ip.mu.Unlock()
		if n > 0 {
			break
		}
		if i > 500 {
			t.Fatal("the sync never started")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := e.svc.Update(context.Background(), c.ID, UpdateInput{Name: ptrOf("Edited")}); err != nil {
		t.Fatal(err)
	}
	close(e.ip.gate)
	<-done
	if e.count("ddns_sync_history") != 0 || e.get(c.ID).LastCheckedAt != nil {
		t.Fatal("the stale sync result was stored")
	}
	if !strings.Contains(e.logs.String(), "not stored") {
		t.Fatal("the dropped result is not logged")
	}
}

// REQ-DDNS-006: the manual endpoint and the scheduler share one code path and are serialised per config.
func TestSyncsOfOneConfigAreSerialised(t *testing.T) {
	e := newEnv(t)
	c := e.mk("Home", "r1")
	other := e.mk("Cam", "r5")
	var inflight, maxInflight atomic.Int32
	var otherMax atomic.Int32
	e.cf.SetFail(func(r *http.Request) (int, string, http.Header) {
		if r.Method != "GET" || !strings.HasSuffix(r.URL.Path, "/dns_records/r1") {
			return 0, "", nil
		}
		n := inflight.Add(1)
		for {
			m := maxInflight.Load()
			if n <= m || maxInflight.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
		inflight.Add(-1)
		return 0, "", nil
	})
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() { defer wg.Done(); e.mustSync(c.ID) }()
	}
	wg.Add(1)
	go func() { defer wg.Done(); e.mustSync(other.ID); otherMax.Store(1) }()
	wg.Wait()
	if maxInflight.Load() != 1 {
		t.Fatalf("%d syncs of the same configuration ran at once", maxInflight.Load())
	}
	if e.count("ddns_sync_history") != 5 {
		t.Fatalf("history rows: %d", e.count("ddns_sync_history"))
	}
	if len(e.svc.locks.m) != 0 {
		t.Fatalf("idle lock entries leaked: %d", len(e.svc.locks.m))
	}
}

func TestIsTransient(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"quorum", quorumFailure(), true},
		{"timeout", context.DeadlineExceeded, true},
		{"cf 503", &CloudflareError{Status: 503}, true},
		{"cf 429", &CloudflareError{Status: 429}, true},
		{"cf network", &CloudflareError{network: true}, true},
		{"cf 403", &CloudflareError{Status: 403}, false},
		{"cf 404", &CloudflareError{Status: 404}, false},
		{"cf 400", &CloudflareError{Status: 400}, false},
		{"wrapped cf 403", errors.Join(errors.New("x"), &CloudflareError{Status: 403}), false},
		{"mismatch", &permanentError{ErrRecordMismatch}, false},
		{"unknown", errBoom, true},
	}
	for _, tc := range cases {
		if got := IsTransient(tc.err); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestBackoffDelay(t *testing.T) {
	// jitter 1 gives the full delay, jitter 0 half of it.
	want := []time.Duration{1, 2, 4, 8, 16, 32, 64, 128, 256, 300, 300}
	for i, w := range want {
		if got := BackoffDelay(i+1, 1); got != w*time.Second {
			t.Errorf("attempt %d: %v, want %v", i+1, got, w*time.Second)
		}
		if got := BackoffDelay(i+1, 0); got != w*time.Second/2 {
			t.Errorf("attempt %d, min jitter: %v, want %v", i+1, got, w*time.Second/2)
		}
	}
	for _, n := range []int{0, 21, 64, 1 << 30} {
		if got := BackoffDelay(n, 1); got > BackoffMax || got <= 0 {
			t.Errorf("attempt %d: %v", n, got)
		}
	}
}

// REQ-RET-001: the retention SQL for ddns_sync_history, now that the syncer fills the table.
func TestPruneSyncHistory(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c := e.mk("Home", "r1")
	other := e.mk("Cam", "r5")
	base := e.clock.Now()
	for i := range 7 { // 5 old rows and 2 recent ones for c; 1 old row for the other config
		o := Outcome{At: base.Add(-time.Duration(60-i*7) * 24 * time.Hour), Result: ResultUnchanged, IP: "8.8.8.8"}
		if i >= 5 {
			o.At = base.Add(-time.Hour)
		}
		if _, err := e.repo.SaveOutcome(ctx, c.ID, c.UpdatedAt, o); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.repo.SaveOutcome(ctx, other.ID, other.UpdatedAt, Outcome{At: base.Add(-60 * 24 * time.Hour), Result: ResultFailed, Error: "x"}); err != nil {
		t.Fatal(err)
	}
	cutoff := base.Add(-30 * 24 * time.Hour)
	n, err := PruneSyncHistory(ctx, e.db, cutoff, 3) // batches: 3, then 3 (limit respected)
	if err != nil || n != 3 {
		t.Fatalf("first batch: %d %v", n, err)
	}
	if n, err = PruneSyncHistory(ctx, e.db, cutoff, 100); err != nil || n != 3 {
		t.Fatalf("second batch: %d %v", n, err)
	}
	if n, err = PruneSyncHistory(ctx, e.db, cutoff, 100); err != nil || n != 0 {
		t.Fatalf("nothing left: %d %v", n, err)
	}
	if got := len(e.history(c.ID)); got != 2 {
		t.Fatalf("recent rows kept: %d", got)
	}
	if got := e.count("ddns_configs"); got != 2 {
		t.Fatalf("pruning history must not touch configurations: %d", got)
	}
}
