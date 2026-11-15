package activity

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/thanhtunguet/homecore/internal/audit"
	"github.com/thanhtunguet/homecore/internal/page"
	"github.com/thanhtunguet/homecore/internal/storage"
)

type env struct {
	t   *testing.T
	db  *storage.DB
	svc *Service
	w   *audit.Writer
	now time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, db: db, svc: NewService(db), now: time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)}
	e.w = audit.NewWriter(db, func() time.Time { return e.now })
	return e
}

func (e *env) write(entry audit.Entry) {
	e.t.Helper()
	if entry.Result == "" {
		entry.Result = audit.ResultOK
	}
	if err := e.w.Write(context.Background(), entry); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) list(p Params) Page {
	e.t.Helper()
	pg, err := e.svc.List(context.Background(), p)
	if err != nil {
		e.t.Fatal(err)
	}
	return pg
}

// REQ-API-023: newest first, paginated over entries with identical timestamps, filters compose.
func TestFeedOrderPaginationAndFilters(t *testing.T) {
	e := newEnv(t)
	devA, devB, cmd := uuid.NewString(), uuid.NewString(), uuid.NewString()
	e.write(audit.Entry{Actor: "alice", Action: "auth.login", TargetType: "user", TargetID: "u1", RemoteAddr: "192.168.1.77"})
	e.now = e.now.Add(time.Second)
	e.write(audit.Entry{Actor: "alice", Action: "device.create", TargetType: "device", TargetID: devA, Details: map[string]any{"name": "pc"}})
	e.write(audit.Entry{Actor: "alice", Action: "device.shutdown", TargetType: "device", TargetID: devA, Details: map[string]any{"command_id": cmd, "status": "acknowledged", "name": "pc"}}) // same second
	e.write(audit.Entry{Actor: "bob", Action: "device.wake", TargetType: "device", TargetID: devB, Details: map[string]any{"command_id": uuid.NewString()}})
	e.now = e.now.Add(time.Second)
	e.write(audit.Entry{Actor: "cli", Action: "user.create", TargetType: "user", TargetID: "u2", Details: map[string]any{"username": "carol"}})

	all := e.list(Params{})
	var actions []string
	for _, it := range all.Items {
		actions = append(actions, it.Action)
	}
	if got := strings.Join(actions, ","); got != "user.create,device.wake,device.shutdown,device.create,auth.login" || all.NextCursor != "" {
		t.Fatalf("order: %s next=%q", got, all.NextCursor)
	}

	// One item per page walks the same sequence, ties on `at` included, no duplicates, no gaps.
	var walked []int64
	cur := ""
	for i := 0; i < 10; i++ {
		pg := e.list(Params{Limit: 1, Cursor: cur})
		for _, it := range pg.Items {
			walked = append(walked, it.ID)
		}
		if pg.NextCursor == "" {
			break
		}
		cur = pg.NextCursor
	}
	if len(walked) != 5 {
		t.Fatalf("walked %v", walked)
	}
	for i, it := range all.Items {
		if walked[i] != it.ID {
			t.Fatalf("paging differs from the full list at %d: %v vs %v", i, walked, all.Items)
		}
	}

	// Filters.
	if pg := e.list(Params{DeviceID: devA}); len(pg.Items) != 2 || pg.Items[0].Action != "device.shutdown" {
		t.Fatalf("device filter: %+v", pg.Items)
	}
	if pg := e.list(Params{Category: CategoryCommand}); len(pg.Items) != 2 {
		t.Fatalf("command category: %+v", pg.Items)
	}
	if pg := e.list(Params{Category: CategoryDevice}); len(pg.Items) != 1 || pg.Items[0].Action != "device.create" {
		t.Fatalf("device category must exclude wake/shutdown: %+v", pg.Items)
	}
	if pg := e.list(Params{Category: CategoryCommand, DeviceID: devA}); len(pg.Items) != 1 || pg.Items[0].CommandID != cmd || pg.Items[0].DeviceID != devA {
		t.Fatalf("combined: %+v", pg.Items)
	}
	if pg := e.list(Params{Category: CategoryDDNS}); len(pg.Items) != 0 || pg.NextCursor != "" {
		t.Fatalf("empty category: %+v", pg)
	}
}

// The SQL category predicates and CategoryOf must agree for every action the system writes.
func TestCategorySQLMatchesCategoryOf(t *testing.T) {
	e := newEnv(t)
	actions := []string{"auth.login", "auth.logout", "auth.refresh_reuse", "user.create", "user.password_change",
		"device.create", "device.update", "device.delete", "device.agent_remove", "device.wake", "device.shutdown",
		"ddns.create", "ddns.sync", "controller_key.create", "something.new", "devices", "authx"}
	for _, a := range actions {
		e.write(audit.Entry{Action: a})
	}
	for _, c := range []Category{CategoryAuth, CategoryDevice, CategoryCommand, CategoryDDNS, CategoryUser, CategorySystem} {
		pg := e.list(Params{Category: c, Limit: 200})
		got := map[string]bool{}
		for _, it := range pg.Items {
			got[it.Action] = true
			if it.Category != c {
				t.Errorf("filter %s returned %s (%s)", c, it.Action, it.Category)
			}
		}
		for _, a := range actions {
			if (CategoryOf(a) == c) != got[a] {
				t.Errorf("category %s, action %s: CategoryOf says %v, SQL says %v", c, a, CategoryOf(a) == c, got[a])
			}
		}
	}
}

// Exposure rules: no client address, no failure reason, no arbitrary details; deleted devices keep
// the name that was recorded; live devices show their current name.
func TestFeedExposesOnlyWhitelistedData(t *testing.T) {
	e := newEnv(t)
	dev := uuid.NewString()
	if _, err := e.db.Exec(`INSERT INTO devices (id, name, ip_address, monitor_port, created_at, updated_at) VALUES (?, 'Living PC', '10.0.0.5', 22, 1, 1)`, dev); err != nil {
		t.Fatal(err)
	}
	e.write(audit.Entry{Action: "auth.login", Result: audit.ResultFailed, RemoteAddr: "203.0.113.9",
		Details: map[string]any{"reason": "unknown user", "attempted": "root", "password": "hunter2"}})
	e.write(audit.Entry{Actor: "alice", Action: "device.wake", TargetType: "device", TargetID: dev,
		RemoteAddr: "192.168.1.10", Details: map[string]any{"command_id": uuid.NewString(), "status": "sent", "secret": "s3cr3t"}})

	pg := e.list(Params{})
	if len(pg.Items) != 2 {
		t.Fatalf("%+v", pg.Items)
	}
	blob := fmt.Sprintf("%+v", pg.Items)
	for _, leak := range []string{"203.0.113.9", "192.168.1.10", "unknown user", "root", "hunter2", "s3cr3t", "reason", "remote"} {
		if strings.Contains(blob, leak) {
			t.Fatalf("the feed exposes %q: %s", leak, blob)
		}
	}
	login, wake := pg.Items[1], pg.Items[0]
	if login.Actor != "" || login.Message != "Failed sign-in attempt" || login.Result != "failed" || login.Category != CategoryAuth {
		t.Fatalf("failed login: %+v", login)
	}
	if wake.Message != "Wake requested for Living PC" || wake.DeviceID != dev || wake.Category != CategoryCommand {
		t.Fatalf("wake: %+v", wake)
	}
	// The device is deleted: its id stays, the name is gone unless the audit row recorded it.
	if _, err := e.db.Exec(`DELETE FROM devices WHERE id = ?`, dev); err != nil {
		t.Fatal(err)
	}
	if got := e.list(Params{}).Items[0].Message; got != "Wake requested for a device" {
		t.Fatalf("after delete: %q", got)
	}
}

func TestMessages(t *testing.T) {
	cmd := uuid.NewString()
	cases := []struct {
		raw  Raw
		want string
	}{
		{Raw{Action: "auth.login", Actor: "alice", Result: "ok"}, "alice signed in"},
		{Raw{Action: "auth.login", Actor: "alice", Result: "failed"}, "Failed sign-in for alice"},
		{Raw{Action: "auth.logout", Actor: "alice", Result: "ok"}, "alice signed out"},
		{Raw{Action: "user.create", Actor: "cli", Result: "ok", Details: `{"username":"carol"}`}, "User carol created"},
		{Raw{Action: "device.shutdown", Result: "ok", DeviceName: "nas", Details: `{"command_id":"` + cmd + `","status":"completed"}`}, "Shutdown requested for nas: nothing to do, already in that state"},
		{Raw{Action: "device.shutdown", Result: "failed", DeviceName: "nas", Details: `{"error_code":"agent_unreachable"}`}, "Shutdown requested for nas: failed (agent_unreachable)"},
		{Raw{Action: "device.update", Result: "ok", Details: `{"name":"tv"}`, DeviceName: "renamed"}, "Device tv updated"},
		{Raw{Action: "controller_key.create", Actor: "system", Result: "ok", Details: `{"key_id":"3f9a0c1d2b4e5f60"}`}, "Controller signing key created (key_id 3f9a0c1d2b4e5f60)"},
		{Raw{Action: "ddns.future", Result: "ok", Details: `not json`}, "ddns.future"},
		{Raw{Action: "alert_channel.create", Result: "ok", Details: `{"name":"My phone","type":"telegram"}`}, "Alert channel My phone created"},
		{Raw{Action: "alert_channel.update", Result: "ok", Details: `{"name":"My phone","fields":["secret"]}`}, "Alert channel My phone updated"},
		{Raw{Action: "alert_channel.delete", Result: "ok", Details: `{"name":"My phone"}`}, "Alert channel My phone deleted"},
		{Raw{Action: "alert_channel.test", Result: "ok", Details: `{"name":"My phone"}`}, "Test message sent to alert channel My phone"},
		{Raw{Action: "alert_channel.test", Result: "failed", Details: `{"name":"My phone","error":"telegram: 403 Forbidden"}`}, "Test message to alert channel My phone failed"},
		{Raw{Action: "ddns.create", Result: "ok", Details: `{"name":"Home","record_name":"home.example.com"}`}, "DDNS configuration Home (home.example.com) created"},
		{Raw{Action: "ddns.update", Result: "ok", Details: `{"name":"home.example.com","record_name":"home.example.com","fields":["api_token"]}`}, "DDNS configuration home.example.com updated"},
		{Raw{Action: "ddns.delete", Result: "ok", Details: `{"name":"Home"}`}, "DDNS configuration Home deleted"},
		{Raw{Action: "ddns.sync", Actor: "system", Result: "ok", Details: `{"record_name":"home.example.com","ip":"8.8.4.4","sync_result":"updated"}`}, "home.example.com updated to 8.8.4.4"},
		{Raw{Action: "ddns.sync", Actor: "alice", Result: "ok", Details: `{"record_name":"home.example.com","ip":"8.8.4.4","sync_result":"unchanged"}`}, "home.example.com checked: already up to date (8.8.4.4)"},
		{Raw{Action: "ddns.sync", Actor: "alice", Result: "failed", Details: `{"record_name":"home.example.com","error":"cloudflare: Invalid API Token (HTTP 403)"}`}, "DDNS sync of home.example.com failed"},
	}
	for _, tc := range cases {
		if got := toItem(tc.raw).Message; got != tc.want {
			t.Errorf("%s: %q, want %q", tc.raw.Action, got, tc.want)
		}
	}
	if it := toItem(Raw{Action: "device.wake", TargetType: "device", TargetID: "not-a-uuid", Details: `{"command_id":"x"}`}); it.DeviceID != "" || it.CommandID != "" {
		t.Fatalf("non-UUID ids must not be surfaced: %+v", it)
	}
}

func TestBadRequests(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for name, tc := range map[string]struct {
		p    Params
		want error
	}{
		"limit":    {Params{Limit: 201}, page.ErrBadLimit},
		"negative": {Params{Limit: -1}, page.ErrBadLimit},
		"cursor":   {Params{Cursor: "garbage"}, page.ErrBadCursor},
		"cursor 0": {Params{Cursor: page.Encode(cursor{T: 1, I: 0})}, page.ErrBadCursor},
		"category": {Params{Category: "nope"}, ErrInvalid},
		"device":   {Params{DeviceID: "zzz"}, ErrInvalid},
	} {
		if _, err := e.svc.List(ctx, tc.p); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", name, err, tc.want)
		}
	}
	// A cursor of another list (commands use t/i with a string id) is rejected, not misread.
	if _, err := e.svc.List(ctx, Params{Cursor: page.Encode(struct {
		T int64  `json:"t"`
		I string `json:"i"`
	}{1, "abc"})}); !errors.Is(err, page.ErrBadCursor) {
		t.Fatalf("foreign cursor: %v", err)
	}
}
