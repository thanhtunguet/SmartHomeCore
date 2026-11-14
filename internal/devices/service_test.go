package devices

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/thanhtunguet/homecore/internal/page"
	"github.com/thanhtunguet/homecore/internal/storage"
)

func newTestService(t *testing.T) (*Service, *SQLiteRepository, *storage.DB) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "devices.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := NewSQLiteRepository(db)
	return NewService(repo, nil), repo, db
}

func ptr[T any](v T) *T { return &v }

func create(t *testing.T, s *Service, name string) Device {
	t.Helper()
	d, err := s.Create(context.Background(), CreateInput{Name: name, IPAddress: "192.168.1.20", MonitorPort: 22})
	if err != nil {
		t.Fatalf("create %q: %v", name, err)
	}
	return d
}

func TestCreateAppliesDefaultsAndNormalises(t *testing.T) {
	s, _, _ := newTestService(t)
	d, err := s.Create(context.Background(), CreateInput{
		Name: "  Living Room PC ", IPAddress: " 192.168.1.20 ", MonitorPort: 22,
		MACAddress: "AA-BB-CC-DD-EE-FF",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := uuid.Parse(d.ID); err != nil || uuid.MustParse(d.ID).Version() != 7 {
		t.Fatalf("id %q is not a UUIDv7", d.ID)
	}
	want := Device{
		Name: "Living Room PC", IPAddress: "192.168.1.20", MACAddress: "aa:bb:cc:dd:ee:ff",
		WOLPort: 9, Type: TypeDesktop, OS: OSLinux, MonitorPort: 22,
		MonitorInterval: 30 * time.Second, MonitorTimeout: 2 * time.Second, Enabled: true, Status: StatusUnknown,
	}
	got := d
	got.ID, got.CreatedAt, got.UpdatedAt = "", time.Time{}, time.Time{}
	if got != want {
		t.Fatalf("defaults:\n got %+v\nwant %+v", got, want)
	}
	if d.LastSeenAt != nil || d.LastCheckedAt != nil || d.LastLatencyMs != nil || d.Agent != nil {
		t.Fatalf("observed state and binding must be empty: %+v", d)
	}
}

func TestCreateValidation(t *testing.T) {
	s, _, _ := newTestService(t)
	ok := func() CreateInput { return CreateInput{Name: "pc", IPAddress: "10.0.0.5", MonitorPort: 22} }
	long := func(n int) string { return strings.Repeat("a", n) }
	tests := []struct {
		name   string
		mutate func(*CreateInput)
		want   string // substring of the problem; "" = valid
	}{
		{"valid", func(*CreateInput) {}, ""},
		{"hostname as address", func(c *CreateInput) { c.IPAddress = "nas.local" }, ""},
		{"ipv6", func(c *CreateInput) { c.IPAddress = "fd00::1" }, ""},
		{"empty name", func(c *CreateInput) { c.Name = "  " }, "name: required"},
		{"name 100 chars", func(c *CreateInput) { c.Name = long(100) }, ""},
		{"name 101 chars", func(c *CreateInput) { c.Name = long(101) }, "name: at most 100"},
		{"name control char", func(c *CreateInput) { c.Name = "a\x00b" }, "control"},
		{"description too long", func(c *CreateInput) { c.Description = long(1001) }, "description: at most 1000"},
		{"missing address", func(c *CreateInput) { c.IPAddress = "" }, "ip_address: required"},
		{"address not ip nor host", func(c *CreateInput) { c.IPAddress = "not a host!" }, "ip_address: must be"},
		{"address unspecified", func(c *CreateInput) { c.IPAddress = "0.0.0.0" }, "not a usable"},
		{"address multicast", func(c *CreateInput) { c.IPAddress = "224.0.0.1" }, "not a usable"},
		{"address with zone", func(c *CreateInput) { c.IPAddress = "fe80::1%eth0" }, "zones"},
		{"address 254 chars", func(c *CreateInput) { c.IPAddress = long(254) }, "at most 253"},
		{"hostname underscore", func(c *CreateInput) { c.Hostname = "my_pc" }, "hostname: not a valid"},
		{"hostname leading hyphen", func(c *CreateInput) { c.Hostname = "-pc" }, "hostname: not a valid"},
		{"hostname ok", func(c *CreateInput) { c.Hostname = "pc-1.lan" }, ""},
		{"mac bad", func(c *CreateInput) { c.MACAddress = "zz:bb:cc:dd:ee:ff" }, "mac_address"},
		{"mac 8 bytes", func(c *CreateInput) { c.MACAddress = "00:11:22:33:44:55:66:77" }, "mac_address"},
		{"mac bare hex", func(c *CreateInput) { c.MACAddress = "AABBCCDDEEFF" }, ""},
		{"broadcast ipv6", func(c *CreateInput) { c.WOLBroadcastAddress = "ff02::1" }, "wol_broadcast_address"},
		{"broadcast hostname", func(c *CreateInput) { c.WOLBroadcastAddress = "bcast.lan" }, "wol_broadcast_address"},
		{"broadcast ok", func(c *CreateInput) { c.WOLBroadcastAddress = "192.168.1.255" }, ""},
		{"monitor port missing", func(c *CreateInput) { c.MonitorPort = 0 }, "monitor_port: required"},
		{"monitor port too big", func(c *CreateInput) { c.MonitorPort = 65536 }, "monitor_port: must be between"},
		{"wol port 0", func(c *CreateInput) { c.WOLPort = ptr(0) }, "wol_port"},
		{"wol port 65535", func(c *CreateInput) { c.WOLPort = ptr(65535) }, ""},
		{"interval 4", func(c *CreateInput) { c.MonitorIntervalSeconds = ptr(4) }, "monitor_interval_seconds"},
		{"interval 5", func(c *CreateInput) { c.MonitorIntervalSeconds = ptr(5) }, ""},
		{"interval 3600", func(c *CreateInput) { c.MonitorIntervalSeconds = ptr(3600) }, ""},
		{"interval 3601", func(c *CreateInput) { c.MonitorIntervalSeconds = ptr(3601) }, "monitor_interval_seconds"},
		{"timeout 99", func(c *CreateInput) { c.MonitorTimeoutMs = ptr(99) }, "monitor_timeout_ms"},
		{"timeout 100", func(c *CreateInput) { c.MonitorTimeoutMs = ptr(100) }, ""},
		{"timeout 10000", func(c *CreateInput) { c.MonitorTimeoutMs = ptr(10000) }, ""},
		{"timeout 10001", func(c *CreateInput) { c.MonitorTimeoutMs = ptr(10001) }, "monitor_timeout_ms"},
		{"bad type", func(c *CreateInput) { c.DeviceType = "toaster" }, "device_type"},
		{"type tv", func(c *CreateInput) { c.DeviceType = "tv" }, ""},
		{"bad os", func(c *CreateInput) { c.OS = "beos" }, "os:"},
		{"os embedded", func(c *CreateInput) { c.OS = "embedded" }, ""},
		{"agent nil uuid", func(c *CreateInput) { c.Agent = &AgentInput{AgentID: uuid.Nil.String()} }, "agent.agent_id"},
		{"agent garbage id", func(c *CreateInput) { c.Agent = &AgentInput{AgentID: "nope"} }, "agent.agent_id"},
		{"agent bad port", func(c *CreateInput) { c.Agent = &AgentInput{AgentID: uuid.NewString(), Port: 70000} }, "agent.port"},
		{"agent bad scheme", func(c *CreateInput) { c.Agent = &AgentInput{AgentID: uuid.NewString(), Scheme: "ftp"} }, "agent.scheme"},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := ok()
			in.Name = fmt.Sprintf("pc-%d", i) // unique per case; overridden by cases that test the name
			tt.mutate(&in)
			_, err := s.Create(context.Background(), in)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			var verr *ValidationError
			if !errors.As(err, &verr) || !strings.Contains(verr.Error(), tt.want) {
				t.Fatalf("got %v, want a validation error containing %q", err, tt.want)
			}
		})
	}
}

func TestCreateReportsAllProblemsAtOnce(t *testing.T) {
	s, _, _ := newTestService(t)
	_, err := s.Create(context.Background(), CreateInput{Name: "", IPAddress: "", MonitorPort: 0})
	var verr *ValidationError
	if !errors.As(err, &verr) || len(verr.Problems) != 3 {
		t.Fatalf("want 3 problems, got %v", err)
	}
}

func TestNameUniqueIgnoringCase(t *testing.T) {
	s, _, _ := newTestService(t)
	create(t, s, "Desktop")
	for _, name := range []string{"Desktop", "desktop", "DESKTOP", "  desktop  "} {
		_, err := s.Create(context.Background(), CreateInput{Name: name, IPAddress: "10.0.0.9", MonitorPort: 22})
		if !errors.Is(err, ErrNameTaken) {
			t.Fatalf("name %q: got %v, want ErrNameTaken", name, err)
		}
	}
	other := create(t, s, "Laptop")
	if _, err := s.Update(context.Background(), other.ID, UpdateInput{Name: ptr("DESKTOP")}); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("rename onto an existing name: got %v", err)
	}
	// Renaming to a different case of its own name is fine.
	if _, err := s.Update(context.Background(), other.ID, UpdateInput{Name: ptr("LAPTOP")}); err != nil {
		t.Fatalf("case-only rename: %v", err)
	}
}

func TestUpdateClearsWithEmptyString(t *testing.T) {
	s, _, _ := newTestService(t)
	ctx := context.Background()
	d, err := s.Create(ctx, CreateInput{
		Name: "nas", IPAddress: "10.0.0.2", MonitorPort: 445, Description: "basement", Hostname: "nas.lan",
		MACAddress: "00:11:22:33:44:55", WOLBroadcastAddress: "10.0.0.255",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Omitted fields are unchanged.
	got, err := s.Update(ctx, d.ID, UpdateInput{Enabled: ptr(false)})
	if err != nil || got.Description != "basement" || got.Hostname != "nas.lan" || got.MACAddress == "" || got.Enabled {
		t.Fatalf("omitted fields changed: %+v %v", got, err)
	}
	// "" clears the four optional text fields.
	got, err = s.Update(ctx, d.ID, UpdateInput{
		Description: ptr(""), Hostname: ptr(""), MACAddress: ptr(""), WOLBroadcastAddress: ptr(""),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Description != "" || got.Hostname != "" || got.MACAddress != "" || got.WOLBroadcastAddress != "" {
		t.Fatalf("not cleared: %+v", got)
	}
	// Required fields cannot be cleared.
	for _, in := range []UpdateInput{{Name: ptr("")}, {IPAddress: ptr("")}} {
		var verr *ValidationError
		if _, err := s.Update(ctx, d.ID, in); !errors.As(err, &verr) {
			t.Fatalf("clearing a required field: got %v", err)
		}
	}
	// Validation applies to the patched value.
	var verr *ValidationError
	if _, err := s.Update(ctx, d.ID, UpdateInput{MonitorIntervalSeconds: ptr(1)}); !errors.As(err, &verr) {
		t.Fatalf("bad interval accepted: %v", err)
	}
}

func TestEmptyUpdateChangesNothing(t *testing.T) {
	s, _, _ := newTestService(t)
	d := create(t, s, "pc")
	time.Sleep(2 * time.Millisecond)
	got, err := s.Update(context.Background(), d.ID, UpdateInput{})
	if err != nil || !got.UpdatedAt.Equal(d.UpdatedAt) {
		t.Fatalf("empty patch touched the device: %v %v vs %v", err, got.UpdatedAt, d.UpdatedAt)
	}
	if _, err := s.Update(context.Background(), uuid.NewString(), UpdateInput{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown device: %v", err)
	}
}

func TestAgentBindingLifecycle(t *testing.T) {
	s, _, db := newTestService(t)
	ctx := context.Background()
	agentA, agentB := uuid.NewString(), uuid.NewString()

	// Create with a binding: defaults port 9750 and http.
	d, err := s.Create(ctx, CreateInput{Name: "srv", IPAddress: "10.0.0.3", MonitorPort: 22, Agent: &AgentInput{AgentID: agentA}})
	if err != nil {
		t.Fatal(err)
	}
	if d.Agent == nil || *d.Agent != (AgentBinding{AgentID: agentA, Port: 9750, Scheme: "http", Status: AgentUnknown}) {
		t.Fatalf("binding after create: %+v", d.Agent)
	}

	// The same agent cannot serve two devices (agent_bindings.agent_id is UNIQUE).
	if _, err := s.Create(ctx, CreateInput{Name: "other", IPAddress: "10.0.0.4", MonitorPort: 22, Agent: &AgentInput{AgentID: agentA}}); !errors.Is(err, ErrAgentTaken) {
		t.Fatalf("duplicate agent on create: %v", err)
	}
	// The failed create left no half-written device behind.
	if page, _ := s.List(ctx, 0, ""); len(page.Items) != 1 {
		t.Fatalf("failed create must roll back, have %d devices", len(page.Items))
	}

	// PATCH replaces the binding; a PATCH without `agent` leaves it alone.
	d, err = s.Update(ctx, d.ID, UpdateInput{Agent: &AgentInput{AgentID: agentB, Port: 9999, Scheme: "https"}})
	if err != nil || *d.Agent != (AgentBinding{AgentID: agentB, Port: 9999, Scheme: "https", Status: AgentUnknown}) {
		t.Fatalf("binding after update: %+v %v", d.Agent, err)
	}
	if d, err = s.Update(ctx, d.ID, UpdateInput{Description: ptr("x")}); err != nil || d.Agent == nil || d.Agent.AgentID != agentB {
		t.Fatalf("binding lost by an unrelated PATCH: %+v %v", d.Agent, err)
	}
	var rows int
	_ = db.QueryRow(`SELECT count(*) FROM agent_bindings`).Scan(&rows)
	if rows != 1 {
		t.Fatalf("expected one binding row, have %d", rows)
	}

	// Add a binding to a device that had none; taking a bound agent id is a 409.
	plain := create(t, s, "plain")
	if _, err := s.Update(ctx, plain.ID, UpdateInput{Agent: &AgentInput{AgentID: agentB}}); !errors.Is(err, ErrAgentTaken) {
		t.Fatalf("duplicate agent on update: %v", err)
	}
	if plain, err = s.Update(ctx, plain.ID, UpdateInput{Agent: &AgentInput{AgentID: agentA}}); err != nil || plain.Agent == nil {
		t.Fatalf("bind after create: %+v %v", plain.Agent, err)
	}

	// Remove is idempotent; only an unknown device is a 404.
	for i := 0; i < 2; i++ {
		if err := s.RemoveAgent(ctx, d.ID); err != nil {
			t.Fatalf("remove #%d: %v", i, err)
		}
	}
	if d, _ = s.Get(ctx, d.ID); d.Agent != nil {
		t.Fatalf("binding still there: %+v", d.Agent)
	}
	if err := s.RemoveAgent(ctx, uuid.NewString()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown device: %v", err)
	}
}

func TestDeleteCascadesToBindingHistoryAndCommands(t *testing.T) {
	s, repo, db := newTestService(t)
	ctx := context.Background()
	d, err := s.Create(ctx, CreateInput{Name: "gone", IPAddress: "10.0.0.8", MonitorPort: 22, Agent: &AgentInput{AgentID: uuid.NewString()}})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordObservation(ctx, Observation{DeviceID: d.ID, At: time.Now(), Reachable: true, Status: StatusOnline, Previous: StatusUnknown, Changed: true, Reason: "probe"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO commands (id, device_id, type, status, created_at, updated_at) VALUES (?, ?, 'wake', 'sent', 0, 0)`, uuid.NewString(), d.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"devices", "agent_bindings", "device_status_history", "commands"} {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s: %d rows left (%v)", table, n, err)
		}
	}
	if err := s.Delete(ctx, d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

func TestListPagination(t *testing.T) {
	s, _, _ := newTestService(t)
	ctx := context.Background()
	// Mixed case on purpose: order is case-insensitive, not byte order ("Zeta" < "alpha" in ASCII).
	names := []string{"alpha", "Bravo", "charlie", "Delta", "echo", "Foxtrot", "golf", "Hotel", "india"}
	for _, i := range []int{4, 0, 8, 2, 6, 1, 7, 3, 5} { // inserted out of order
		create(t, s, names[i])
	}

	var seen []string
	cursor := ""
	pages := 0
	for {
		res, err := s.List(ctx, 4, cursor)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for _, d := range res.Items {
			seen = append(seen, d.Name)
		}
		if res.NextCursor == "" {
			if len(res.Items) == 0 {
				t.Fatal("last page must not be empty")
			}
			break
		}
		if len(res.Items) != 4 {
			t.Fatalf("a page with next_cursor must be full, got %d", len(res.Items))
		}
		cursor = res.NextCursor
	}
	if pages != 3 || strings.Join(seen, ",") != strings.Join(names, ",") {
		t.Fatalf("pages=%d seen=%v", pages, seen)
	}

	// Exactly one page: no cursor.
	res, err := s.List(ctx, 9, "")
	if err != nil || len(res.Items) != 9 || res.NextCursor != "" {
		t.Fatalf("exact page: %d items, cursor %q, %v", len(res.Items), res.NextCursor, err)
	}

	// A cursor survives a rename and a delete of the item it points at (keyset, not offset).
	first, _ := s.List(ctx, 2, "")
	if err := s.Delete(ctx, first.Items[1].ID); err != nil {
		t.Fatal(err)
	}
	next, err := s.List(ctx, 2, first.NextCursor)
	if err != nil || next.Items[0].Name != "charlie" {
		t.Fatalf("after deleting the cursor item: %v %v", next.Items, err)
	}
}

func TestListCursorAcrossNameCaseAndOrderTies(t *testing.T) {
	_, repo, _ := newTestService(t)
	ctx := context.Background()
	// Names are unique case-insensitively, so two rows can never tie on name; the id tie-break in
	// the keyset condition is defence in depth. Check it by hand with the repository: a cursor whose
	// name equals an existing name in another case must resume after that row by id.
	svc := NewService(repo, nil)
	a := create(t, svc, "Same")
	list, err := repo.List(ctx, &ListAfter{Name: "SAME", ID: a.ID}, 10)
	if err != nil || len(list) != 0 {
		t.Fatalf("cursor on the last row (other case) must yield nothing: %v %v", list, err)
	}
	list, err = repo.List(ctx, &ListAfter{Name: "SAME", ID: ""}, 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("cursor with a smaller id must include the tied row: %v %v", list, err)
	}
}

func TestListBadParameters(t *testing.T) {
	s, _, _ := newTestService(t)
	ctx := context.Background()
	for _, limit := range []int{-1, 201} {
		if _, err := s.List(ctx, limit, ""); !errors.Is(err, page.ErrBadLimit) {
			t.Fatalf("limit %d: %v", limit, err)
		}
	}
	for _, cur := range []string{"!!!", "e30", page.Encode(struct{ X int }{1}), page.Encode(historyCursor{T: 1, I: 1})} {
		if _, err := s.List(ctx, 0, cur); !errors.Is(err, page.ErrBadCursor) {
			t.Fatalf("cursor %q: %v", cur, err)
		}
	}
}

func TestObservationsAndHistory(t *testing.T) {
	s, repo, _ := newTestService(t)
	ctx := context.Background()
	d := create(t, s, "pc")
	base := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)

	rec := func(at time.Duration, o Observation) {
		t.Helper()
		o.DeviceID, o.At = d.ID, base.Add(at)
		if err := repo.RecordObservation(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	rec(0, Observation{Reachable: true, Latency: 7 * time.Millisecond, Status: StatusOnline, Previous: StatusUnknown, Changed: true, Reason: "probe"})
	rec(time.Minute, Observation{Reachable: true, Latency: 3 * time.Millisecond, Status: StatusOnline, Previous: StatusOnline}) // no change: no history
	rec(2*time.Minute, Observation{Reachable: false, Status: StatusOnline, Previous: StatusOnline})                             // failure below threshold
	rec(3*time.Minute, Observation{Reachable: false, Status: StatusOffline, Previous: StatusOnline, Changed: true, Reason: "probe"})
	rec(3*time.Minute, Observation{Reachable: true, Latency: 9 * time.Millisecond, Status: StatusOnline, Previous: StatusOffline, Changed: true, Reason: "manual probe"}) // same millisecond: id breaks the tie

	got, _ := s.Get(ctx, d.ID)
	if got.Status != StatusOnline || got.LastSeenAt == nil || !got.LastSeenAt.Equal(base.Add(3*time.Minute)) ||
		got.LastLatencyMs == nil || *got.LastLatencyMs != 9 {
		t.Fatalf("stored state: %+v latency=%v", got, got.LastLatencyMs)
	}
	// A failed probe updates last_checked_at but keeps the last success' latency and last_seen_at.
	rec(4*time.Minute, Observation{Reachable: false, Status: StatusOnline, Previous: StatusOnline})
	got, _ = s.Get(ctx, d.ID)
	if got.LastCheckedAt == nil || !got.LastCheckedAt.Equal(base.Add(4*time.Minute)) || !got.LastSeenAt.Equal(base.Add(3*time.Minute)) || *got.LastLatencyMs != 9 {
		t.Fatalf("after a failed probe: %+v", got)
	}

	// History: transitions only, newest first, ties by id, paginated by cursor.
	var changes []StatusChange
	cursor := ""
	for {
		p, err := s.History(ctx, d.ID, 2, cursor)
		if err != nil {
			t.Fatal(err)
		}
		changes = append(changes, p.Items...)
		if p.NextCursor == "" {
			break
		}
		cursor = p.NextCursor
	}
	var order []string
	for _, c := range changes {
		order = append(order, string(c.PreviousStatus)+">"+string(c.Status)+":"+c.Reason)
	}
	want := "offline>online:manual probe,online>offline:probe,unknown>online:probe"
	if strings.Join(order, ",") != want {
		t.Fatalf("history order:\n got %v\nwant %s", order, want)
	}

	if _, err := s.History(ctx, uuid.NewString(), 10, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("history of an unknown device: %v", err)
	}
	if err := repo.RecordObservation(ctx, Observation{DeviceID: uuid.NewString(), At: base, Status: StatusOnline}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("observation of an unknown device: %v", err)
	}
}

func TestMonitorTargets(t *testing.T) {
	s, repo, db := newTestService(t)
	ctx := context.Background()
	on := create(t, s, "on")
	off, _ := s.Create(ctx, CreateInput{Name: "off", IPAddress: "10.0.0.7", MonitorPort: 22, Enabled: ptr(false)})
	busy, _ := s.Create(ctx, CreateInput{Name: "busy", IPAddress: "10.0.0.6", MonitorPort: 22, Enabled: ptr(false)})
	if _, err := db.ExecContext(ctx, `INSERT INTO commands (id, device_id, type, status, created_at, updated_at) VALUES (?, ?, 'wake', 'sent', 0, 0)`, uuid.NewString(), busy.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO commands (id, device_id, type, status, created_at, updated_at) VALUES (?, ?, 'wake', 'completed', 0, 0)`, uuid.NewString(), off.ID); err != nil {
		t.Fatal(err)
	}
	targets, err := repo.MonitorTargets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tg := range targets {
		got[tg.Device.Name] = tg.Urgent
	}
	// enabled devices are targets; a disabled one only while a command is active.
	if len(got) != 2 || got["on"] || !got["busy"] {
		t.Fatalf("targets = %v (on=%s)", got, on.ID)
	}
}

func TestPruneHistory(t *testing.T) {
	s, repo, _ := newTestService(t)
	ctx := context.Background()
	d := create(t, s, "pc")
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range 5 {
		err := repo.RecordObservation(ctx, Observation{DeviceID: d.ID, At: base.Add(time.Duration(i) * 24 * time.Hour), Status: StatusOnline, Previous: StatusOffline, Changed: true})
		if err != nil {
			t.Fatal(err)
		}
	}
	cutoff := base.Add(3 * 24 * time.Hour) // rows 0,1,2 are older
	n, err := repo.PruneHistory(ctx, cutoff, 2)
	if err != nil || n != 2 {
		t.Fatalf("first batch: %d %v", n, err)
	}
	if n, err = repo.PruneHistory(ctx, cutoff, 2); err != nil || n != 1 {
		t.Fatalf("second batch: %d %v", n, err)
	}
	p, _ := s.History(ctx, d.ID, 10, "")
	if len(p.Items) != 2 {
		t.Fatalf("expected 2 rows left, got %d", len(p.Items))
	}
}

// REQ-API-021: the agent status lives on the binding; a contact updates it, the device API cannot,
// and re-binding to another agent or address forgets it while an identical PATCH keeps it.
func TestAgentContactIsRecordedAndForgottenOnRebind(t *testing.T) {
	s, repo, _ := newTestService(t)
	ctx := context.Background()
	agentA, agentB := uuid.NewString(), uuid.NewString()
	d, err := s.Create(ctx, CreateInput{Name: "srv", IPAddress: "10.0.0.3", MonitorPort: 22, Agent: &AgentInput{AgentID: agentA}})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)

	if err := repo.RecordAgentContact(ctx, d.ID, agentA, AgentContact{OK: true, Version: "1.2.3", At: at}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, d.ID)
	if got.Agent.Status != AgentReady || got.Agent.Version != "1.2.3" || got.Agent.LastOKAt == nil || !got.Agent.LastOKAt.Equal(at) {
		t.Fatalf("after a good contact: %+v", got.Agent)
	}
	// A failure flips the status but keeps what was last known to work; an empty version keeps the old one.
	if err := repo.RecordAgentContact(ctx, d.ID, agentA, AgentContact{OK: false, At: at.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordAgentContact(ctx, d.ID, agentA, AgentContact{OK: true, At: at.Add(2 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(ctx, d.ID)
	if got.Agent.Status != AgentReady || got.Agent.Version != "1.2.3" || !got.Agent.LastOKAt.Equal(at.Add(2*time.Minute)) {
		t.Fatalf("after fail then ok without version: %+v", got.Agent)
	}
	if err := repo.RecordAgentContact(ctx, d.ID, agentA, AgentContact{OK: false, At: at.Add(3 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(ctx, d.ID)
	if got.Agent.Status != AgentUnreachable || !got.Agent.LastOKAt.Equal(at.Add(2*time.Minute)) {
		t.Fatalf("after a failure: %+v", got.Agent)
	}

	// A late result for a previous agent_id is ignored.
	if err := repo.RecordAgentContact(ctx, d.ID, agentB, AgentContact{OK: true, Version: "evil", At: at}); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.Get(ctx, d.ID); got.Agent.Status != AgentUnreachable || got.Agent.Version != "1.2.3" {
		t.Fatalf("a contact for another agent_id changed the binding: %+v", got.Agent)
	}

	// The UI sends the whole binding on every PATCH: an identical one keeps the status...
	if err := repo.RecordAgentContact(ctx, d.ID, agentA, AgentContact{OK: true, At: at}); err != nil {
		t.Fatal(err)
	}
	if got, err = s.Update(ctx, d.ID, UpdateInput{Agent: &AgentInput{AgentID: agentA, Port: 9750, Scheme: "http"}}); err != nil || got.Agent.Status != AgentReady {
		t.Fatalf("identical rebind: %+v %v", got.Agent, err)
	}
	// ...a changed port or agent forgets it.
	for _, in := range []AgentInput{{AgentID: agentA, Port: 9751}, {AgentID: agentB}} {
		in := in
		if got, err = s.Update(ctx, d.ID, UpdateInput{Agent: &in}); err != nil || got.Agent.Status != AgentUnknown || got.Agent.Version != "" || got.Agent.LastOKAt != nil {
			t.Fatalf("rebind %+v: %+v %v", in, got.Agent, err)
		}
		_ = repo.RecordAgentContact(ctx, d.ID, in.AgentID, AgentContact{OK: true, Version: "9", At: at})
	}

	// BoundDevices lists only devices with a binding.
	create(t, s, "plain")
	bound, err := repo.BoundDevices(ctx)
	if err != nil || len(bound) != 1 || bound[0].ID != d.ID {
		t.Fatalf("bound devices: %+v %v", bound, err)
	}
}

// REQ-DEV-012: the legacy endpoints find a device by UUID or by name.
func TestResolveByIDOrName(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newTestService(t)
	pc := create(t, s, "Gaming PC")
	numeric := create(t, s, "42") // a numeric name is a name: there are no integer ids

	for ref, want := range map[string]string{
		pc.ID: pc.ID, "gaming pc": pc.ID, "  GAMING PC ": pc.ID, "42": numeric.ID,
		strings.ToUpper(pc.ID): pc.ID, // a UUID in any case
	} {
		d, err := s.Resolve(ctx, ref)
		if err != nil || d.ID != want {
			t.Errorf("Resolve(%q) = %q, %v; want %q", ref, d.ID, err, want)
		}
	}
	for _, ref := range []string{"", "  ", "nope", uuid.NewString(), "Gaming"} {
		if _, err := s.Resolve(ctx, ref); !errors.Is(err, ErrNotFound) {
			t.Errorf("Resolve(%q): %v, want ErrNotFound", ref, err)
		}
	}
}
