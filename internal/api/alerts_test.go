package api

import (
	"strings"
	"testing"
	"time"

	"github.com/thanhtunguet/homecore/internal/api/gen"
)

const (
	tgTestToken  = "123456789:AAH-test_token_0123456789abcdefghij"
	tgTestTarget = "424242"
)

func alertBody(over map[string]any) map[string]any {
	b := map[string]any{"name": "My phone", "type": "telegram", "settings": map[string]any{"target_user_id": tgTestTarget}, "secret": tgTestToken}
	for k, v := range over {
		if v == nil {
			delete(b, k)
		} else {
			b[k] = v
		}
	}
	return b
}

func (e *devEnv) mkAlert(over map[string]any) gen.AlertChannel {
	e.t.Helper()
	return decode[gen.AlertChannel](e.t, ok(e.t, e.as("POST", "/api/v1/alerts/channels", alertBody(over)), 201))
}

// REQ-ALERT-001, REQ-API-032: a Telegram channel is created from the two adapter parameters, and
// the bot token is never returned.
func TestAlertChannelCRUDOverHTTP(t *testing.T) {
	e := newDevEnv(t)

	rec := ok(t, e.as("POST", "/api/v1/alerts/channels", alertBody(nil)), 201)
	c := decode[gen.AlertChannel](t, rec)
	if c.Name != "My phone" || c.Type != "telegram" || !c.Enabled || !c.HasSecret || c.Settings["target_user_id"] != tgTestTarget ||
		len(c.Events) != 3 || c.LastSentAt != nil || c.LastError != nil {
		t.Fatalf("created: %s", rec.Body)
	}
	if strings.Contains(rec.Body.String(), tgTestToken) || strings.Contains(rec.Body.String(), "AAH-") {
		t.Fatalf("the response carries the token: %s", rec.Body)
	}

	list := decode[struct{ Items []gen.AlertChannel }](t, ok(t, e.as("GET", "/api/v1/alerts/channels", nil), 200))
	if len(list.Items) != 1 || list.Items[0].Id != c.Id {
		t.Fatalf("list: %+v", list)
	}
	path := "/api/v1/alerts/channels/" + c.Id.String()
	if got := decode[gen.AlertChannel](t, ok(t, e.as("GET", path, nil), 200)); got.Name != "My phone" {
		t.Fatalf("get: %+v", got)
	}

	// PATCH: rename, narrow the events, switch off; the secret stays.
	up := decode[gen.AlertChannel](t, ok(t, e.as("PATCH", path, map[string]any{
		"name": "Dad's phone", "enabled": false, "events": []string{"status_changed"}}), 200))
	if up.Name != "Dad's phone" || up.Enabled || len(up.Events) != 1 || up.Events[0] != gen.StatusChanged || !up.HasSecret || up.Settings["target_user_id"] != tgTestTarget {
		t.Fatalf("updated: %+v", up)
	}
	if up = decode[gen.AlertChannel](t, ok(t, e.as("PATCH", path, map[string]any{"settings": map[string]any{"target_user_id": "99"}}), 200)); up.Settings["target_user_id"] != "99" {
		t.Fatalf("settings: %+v", up)
	}

	// Errors.
	if rec := e.as("POST", "/api/v1/alerts/channels", alertBody(map[string]any{"name": "my PHONE"})); rec.Code != 201 {
		t.Fatalf("a second channel with a new name: %d %s", rec.Code, rec.Body) // "Dad's phone" was renamed
	}
	if rec := e.as("POST", "/api/v1/alerts/channels", alertBody(map[string]any{"name": "dad's PHONE"})); rec.Code != 409 {
		t.Fatalf("duplicate name: %d %s", rec.Code, rec.Body)
	}
	for name, over := range map[string]map[string]any{
		"no secret":     {"secret": nil},
		"bad token":     {"secret": "nope"},
		"bad target":    {"settings": map[string]any{"target_user_id": "@alice"}},
		"unknown field": {"settings": map[string]any{"target_user_id": tgTestTarget, "extra": "x"}},
		"unknown event": {"events": []string{"fire"}},
		"empty name":    {"name": ""},
	} {
		rec := e.as("POST", "/api/v1/alerts/channels", alertBody(over))
		if rec.Code != 400 {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		} else if strings.Contains(rec.Body.String(), tgTestToken) {
			t.Errorf("%s: the error echoes the token: %s", name, rec.Body)
		}
	}
	if rec := e.as("PATCH", path, map[string]any{"settings": map[string]any{"target_user_id": "x"}}); rec.Code != 400 {
		t.Errorf("bad settings on update: %d", rec.Code)
	}
	if rec := e.as("PATCH", path, map[string]any{"type": "smoke"}); rec.Code != 400 {
		t.Errorf("the type cannot change (unknown field): %d", rec.Code)
	}
	missing := "/api/v1/alerts/channels/0193a0b2-7c4e-7f00-8000-000000000001"
	for _, m := range []string{"GET", "PATCH", "DELETE"} {
		if rec := e.as(m, missing, map[string]any{"name": "x"}); rec.Code != 404 {
			t.Errorf("%s unknown channel: %d", m, rec.Code)
		}
	}
	if rec := e.as("POST", missing+"/test", nil); rec.Code != 404 {
		t.Errorf("test unknown channel: %d", rec.Code)
	}

	ok(t, e.as("DELETE", path, nil), 204)
	if rec := e.as("GET", path, nil); rec.Code != 404 {
		t.Fatalf("after delete: %d", rec.Code)
	}
}

// REQ-API-032: every alert operation needs a valid access token.
func TestAlertEndpointsRequireAuthentication(t *testing.T) {
	e := newDevEnv(t)
	id := "0193a0b2-7c4e-7f00-8000-000000000001"
	for _, rq := range [][2]string{
		{"GET", "/api/v1/alerts/channels"}, {"POST", "/api/v1/alerts/channels"}, {"GET", "/api/v1/alerts/channels/" + id},
		{"PATCH", "/api/v1/alerts/channels/" + id}, {"DELETE", "/api/v1/alerts/channels/" + id},
		{"POST", "/api/v1/alerts/channels/" + id + "/test"},
	} {
		if rec := e.call(rq[0], rq[1], map[string]any{}, ""); rec.Code != 401 {
			t.Errorf("%s %s without a token: %d", rq[0], rq[1], rec.Code)
		}
		if rec := e.call(rq[0], rq[1], map[string]any{}, "garbage"); rec.Code != 401 {
			t.Errorf("%s %s with a bad token: %d", rq[0], rq[1], rec.Code)
		}
	}
}

// REQ-API-032, REQ-ALERT-009: a service account key is not a session: alert channels are not for it.
func TestAlertEndpointsRefuseScopedKeys(t *testing.T) {
	e := newDevEnv(t)
	key := e.mkKey("mcp", 0).Key
	for _, rq := range [][2]string{{"GET", "/api/v1/alerts/channels"}, {"POST", "/api/v1/alerts/channels"}} {
		if rec := e.callRaw(rq[0], rq[1], alertBody(nil), key); rec.Code != 403 { // the spec does not declare 403 for session-only operations
			t.Errorf("%s %s with a scoped key: %d", rq[0], rq[1], rec.Code)
		}
	}
}

// REQ-ALERT-009: the test endpoint delivers a message; a failure is a 200 with the reason.
func TestAlertChannelTestEndpoint(t *testing.T) {
	e := newDevEnv(t)
	c := e.mkAlert(map[string]any{"enabled": false}) // a disabled channel can still be tested
	path := "/api/v1/alerts/channels/" + c.Id.String()

	res := decode[gen.AlertTestResult](t, ok(t, e.as("POST", path+"/test", nil), 200))
	if res.Result != gen.AlertTestResultResultSent || res.Error != nil || res.ChannelId != c.Id {
		t.Fatalf("result: %+v", res)
	}
	msgs := e.tg.WaitFor(t, 1)
	if msgs[0].ChatID != tgTestTarget || !strings.Contains(msgs[0].Text, "HomeCore test message") {
		t.Fatalf("telegram got %+v", msgs)
	}
	if got := decode[gen.AlertChannel](t, ok(t, e.as("GET", path, nil), 200)); got.LastSentAt == nil || got.LastError != nil {
		t.Fatalf("channel after a good test: %+v", got)
	}

	e.tg.FailWith(403, "Forbidden: bot was blocked by the user")
	res = decode[gen.AlertTestResult](t, ok(t, e.as("POST", path+"/test", nil), 200))
	if res.Result != gen.AlertTestResultResultFailed || res.Error == nil || !strings.Contains(*res.Error, "blocked by the user") {
		t.Fatalf("failed result: %+v", res)
	}
	got := decode[gen.AlertChannel](t, ok(t, e.as("GET", path, nil), 200))
	if got.LastError == nil || !strings.Contains(*got.LastError, "blocked") || got.LastErrorAt == nil {
		t.Fatalf("channel after a failed test: %+v", got)
	}
	for _, table := range [][2]string{{"audit_log", "details"}, {"alert_channels", "last_error"}, {"alert_channels", "settings"}} {
		if n := e.countLike(table[0], table[1], tgTestToken); n != 0 {
			t.Errorf("the token is in %s.%s", table[0], table[1])
		}
	}
	if strings.Contains(e.logs.String(), tgTestToken) {
		t.Errorf("the token is in the log:\n%s", e.logs.String())
	}
}

// REQ-ALERT-010: channel changes are audited without the secret.
func TestAlertChannelAudit(t *testing.T) {
	e := newDevEnv(t)
	c := e.mkAlert(nil)
	path := "/api/v1/alerts/channels/" + c.Id.String()
	// The fake Bot API only knows the first token, so the test after the change fails: audited as failed.
	ok(t, e.as("PATCH", path, map[string]any{"name": "Renamed", "secret": "987654321:BBB-another_token_0123456789abcd"}), 200)
	ok(t, e.as("POST", path+"/test", nil), 200)
	ok(t, e.as("DELETE", path, nil), 204)

	rows, err := e.db.Query(`SELECT action, actor, target_id, result, details FROM audit_log WHERE action LIKE 'alert_channel.%' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var action, actor, target, result, details string
		if err := rows.Scan(&action, &actor, &target, &result, &details); err != nil {
			t.Fatal(err)
		}
		wantResult := "ok"
		if action == "alert_channel.test" {
			wantResult = "failed"
		}
		if actor != "alice" || target != c.Id.String() || result != wantResult {
			t.Errorf("%s: actor=%q target=%q result=%q, want %q", action, actor, target, result, wantResult)
		}
		if strings.Contains(details, "BBB-") || strings.Contains(details, tgTestToken) {
			t.Errorf("%s: details carry a secret: %s", action, details)
		}
		got = append(got, action)
	}
	if want := "alert_channel.create alert_channel.update alert_channel.test alert_channel.delete"; strings.Join(got, " ") != want {
		t.Fatalf("audit actions: %v", got)
	}
	if n := e.countLike("audit_log", "details", `"fields":["name","secret"]`); n != 1 {
		t.Fatalf("the update names the fields it changed (%d rows)", n)
	}
}

// REQ-ALERT-003: wake and shutdown requests are announced, whoever asks; a channel that did not
// subscribe to the event hears nothing.
func TestPowerCommandAlerts(t *testing.T) {
	e := newDevEnv(t)
	e.mkAlert(nil)
	e.mkAlert(map[string]any{"name": "ip only", "events": []string{"ip_changed"}})
	d := e.mkDevice(map[string]any{"name": "Gaming PC", "mac_address": "aa:bb:cc:dd:ee:ff"})

	ok(t, e.as("POST", "/api/v1/devices/"+d.Id.String()+"/wake", nil), 202)
	msgs := e.tg.WaitFor(t, 1)
	if m := msgs[0]; m.ChatID != tgTestTarget || !strings.Contains(m.Text, "Wake requested: Gaming PC") || !strings.Contains(m.Text, "By alice") {
		t.Fatalf("telegram got %+v", msgs)
	}
	if n := len(e.tg.Settle()); n != 1 {
		t.Fatalf("%d messages: the ip-only channel must not get a power alert, and a request must be announced once", n)
	}

	// A request answered with the command that is already running is not news.
	time.Sleep(1100 * time.Millisecond) // past the one-request-per-second throttle
	ok(t, e.as("POST", "/api/v1/devices/"+d.Id.String()+"/wake", nil), 202)
	if n := len(e.tg.Settle()); n != 1 {
		t.Fatalf("the repeated request was announced again: %d messages", n)
	}
}

// REQ-ALERT-004: a status change HomeCore did not cause is announced; one it did cause is not.
func TestUnexpectedStatusChangeAlerts(t *testing.T) {
	e := newDevEnv(t)
	e.mkAlert(map[string]any{"events": []string{"status_changed"}})
	d := e.mkDevice(map[string]any{"name": "NAS", "mac_address": "aa:bb:cc:dd:ee:ff", "monitor_port": 22})
	probe := func() { ok(t, e.as("POST", "/api/v1/devices/"+d.Id.String()+"/probe", nil), 200) }

	e.setUp(22, true)
	probe() // unknown → online: a first sighting, not news
	if n := len(e.tg.Settle()); n != 0 {
		t.Fatalf("a first sighting was announced: %+v", e.tg.Messages())
	}

	e.setUp(22, false)
	probe() // one failure: hysteresis keeps it online
	if n := len(e.tg.Settle()); n != 0 {
		t.Fatalf("a single failed probe was announced: %+v", e.tg.Messages())
	}
	probe() // second failure → offline, and nobody asked for it
	msgs := e.tg.WaitFor(t, 1)
	if !strings.Contains(msgs[0].Text, "NAS went offline") || !strings.Contains(msgs[0].Text, "192.168.1.20:22") {
		t.Fatalf("telegram got %+v", msgs)
	}

	// Waking it is HomeCore's doing: coming online afterwards is not a surprise.
	ok(t, e.as("POST", "/api/v1/devices/"+d.Id.String()+"/wake", nil), 202)
	e.setUp(22, true)
	probe()
	if n := len(e.tg.Settle()); n != 1 {
		t.Fatalf("coming online after a wake was announced: %+v", e.tg.Messages())
	}

	// The command is done now; going offline again with nobody asking is news.
	e.setUp(22, false)
	probe()
	probe()
	if msgs = e.tg.WaitFor(t, 2); !strings.Contains(msgs[1].Text, "NAS went offline") {
		t.Fatalf("telegram got %+v", msgs)
	}
}

// REQ-ALERT-002: a DDNS sync that moves the record to a new address is announced, once.
func TestIPChangeAlert(t *testing.T) {
	e := newDevEnv(t)
	e.mkAlert(map[string]any{"events": []string{"ip_changed"}})
	c := decode[gen.DdnsConfig](t, ok(t, e.as("POST", "/api/v1/ddns", ddnsCreateBody(nil)), 201))
	sync := "/api/v1/ddns/" + c.Id.String() + "/sync"

	ok(t, e.as("POST", sync, nil), 200) // the record held 8.8.4.4, the Controller's address is 8.8.8.8
	msgs := e.tg.WaitFor(t, 1)
	if m := msgs[0]; !strings.Contains(m.Text, "Public IP changed") || !strings.Contains(m.Text, "home.example.com (A)") || !strings.Contains(m.Text, "8.8.4.4 → 8.8.8.8") {
		t.Fatalf("telegram got %+v", msgs)
	}
	ok(t, e.as("POST", sync, nil), 200) // unchanged
	if n := len(e.tg.Settle()); n != 1 {
		t.Fatalf("an unchanged record was announced: %+v", e.tg.Messages())
	}
	e.ip.set("8.8.4.5", nil)
	ok(t, e.as("POST", sync, nil), 200)
	if msgs = e.tg.WaitFor(t, 2); !strings.Contains(msgs[1].Text, "8.8.8.8 → 8.8.4.5") {
		t.Fatalf("telegram got %+v", msgs)
	}
}
