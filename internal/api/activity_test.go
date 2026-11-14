package api

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/thanhtunguet/homecore/internal/agentctl"
	"github.com/thanhtunguet/homecore/internal/api/gen"
)

// REQ-API-023 (GAP-API-05): the feed shows what the audit log recorded, newest first, filterable,
// paginated, readable by any authenticated user, without client addresses.
func TestActivityFeedOverHTTP(t *testing.T) {
	se := newShutdownEnv(t, nil)
	c := se.shutdown() // device.create (from setup), then device.shutdown
	other := se.mkDevice(map[string]any{"name": "other", "monitor_port": 4242})

	feed := decode[gen.ActivityList](t, ok(t, se.as("GET", "/api/v1/activity", nil), 200))
	var actions []string
	for _, it := range feed.Items {
		actions = append(actions, it.Action)
	}
	// Newest first: create "other", shutdown, create "gaming pc", the login of the fixture, the account creation.
	if got := strings.Join(actions, ","); got != "device.create,device.shutdown,device.create,auth.login,user.create" {
		t.Fatalf("actions: %s", got)
	}
	sd := feed.Items[1]
	if sd.Category != gen.ActivityCategory("command") || sd.Actor == nil || *sd.Actor != "alice" || sd.Result != "ok" ||
		sd.DeviceId == nil || *sd.DeviceId != se.device.Id || sd.CommandId == nil || *sd.CommandId != c.Id ||
		sd.Message != "Shutdown requested for gaming pc" || sd.Id <= 0 || time.Since(sd.At) > time.Minute {
		t.Fatalf("shutdown entry: %+v", sd)
	}
	if feed.Items[3].Category != "auth" || feed.Items[4].Category != "user" || *feed.Items[4].Actor != "test" {
		t.Fatalf("auth/user entries: %+v %+v", feed.Items[3], feed.Items[4])
	}
	if raw := se.as("GET", "/api/v1/activity", nil).Body.String(); strings.Contains(raw, "192.0.2.") || strings.Contains(raw, "remote") {
		t.Fatalf("the feed exposes client addresses: %s", raw)
	}

	// Filters.
	byDev := decode[gen.ActivityList](t, ok(t, se.as("GET", "/api/v1/activity?device_id="+se.device.Id.String(), nil), 200))
	if len(byDev.Items) != 2 {
		t.Fatalf("device filter: %+v", byDev.Items)
	}
	byCat := decode[gen.ActivityList](t, ok(t, se.as("GET", "/api/v1/activity?category=command", nil), 200))
	if len(byCat.Items) != 1 || byCat.Items[0].Id != sd.Id {
		t.Fatalf("category filter: %+v", byCat.Items)
	}
	if n := len(decode[gen.ActivityList](t, se.as("GET", "/api/v1/activity?category=ddns", nil)).Items); n != 0 {
		t.Fatalf("ddns entries: %d", n)
	}
	_ = other

	// Pagination, one entry at a time, newest first.
	var ids []int64
	url := "/api/v1/activity?limit=1"
	for i := 0; i < 10; i++ {
		pg := decode[gen.ActivityList](t, ok(t, se.as("GET", url, nil), 200))
		for _, it := range pg.Items {
			ids = append(ids, it.Id)
		}
		if pg.NextCursor == nil {
			break
		}
		url = "/api/v1/activity?limit=1&cursor=" + *pg.NextCursor
	}
	if len(ids) != 5 {
		t.Fatalf("paged ids: %v", ids)
	}
	for i := 1; i < len(ids); i++ {
		if ids[i] >= ids[i-1] {
			t.Fatalf("not newest first: %v", ids)
		}
	}

	// Errors and authentication.
	for _, q := range []string{"?limit=0", "?limit=201", "?cursor=bogus", "?category=bogus"} {
		if rec := se.as("GET", "/api/v1/activity"+q, nil); rec.Code != 400 || errCode(t, rec) != "bad_request" {
			t.Errorf("%s → %d %s", q, rec.Code, rec.Body)
		}
	}
	if rec := se.callRaw("GET", "/api/v1/activity?device_id=zzz", nil, se.token); rec.Code != 400 {
		t.Errorf("bad device id → %d", rec.Code)
	}
	want(t, se.call("GET", "/api/v1/activity", nil, ""), 401)
}

// REQ-API-021, REQ-SHD-011 (GAP-API-09): the health job fills status, version and last_ok_at through the API.
func TestAgentStatusFromTheHealthJobOverHTTP(t *testing.T) {
	se := newShutdownEnv(t, nil)
	g := decode[gen.Device](t, se.as("GET", se.path, nil))
	if g.Agent.Status == nil || *g.Agent.Status != gen.AgentBindingStatusUnknown || g.Agent.Version != nil || g.Agent.LastOkAt != nil {
		t.Fatalf("before the first contact: %+v", g.Agent)
	}

	job := agentctl.NewHealth(agentctl.HealthConfig{Store: se.repo, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	job.Pass(context.Background())
	g = decode[gen.Device](t, ok(t, se.as("GET", se.path, nil), 200))
	if *g.Agent.Status != gen.AgentBindingStatusReady || g.Agent.Version == nil || *g.Agent.Version == "" || g.Agent.LastOkAt == nil {
		t.Fatalf("after a health pass: %+v", g.Agent)
	}
	l := decode[gen.DeviceList](t, se.as("GET", "/api/v1/devices", nil))
	if l.Items[0].Agent == nil || *l.Items[0].Agent.Status != gen.AgentBindingStatusReady {
		t.Fatalf("list: %+v", l.Items[0].Agent)
	}

	// The agent goes away: the next pass says unreachable but keeps the last good contact.
	lastOK := *g.Agent.LastOkAt
	se.agent.srv.Close()
	job.Pass(context.Background())
	g = decode[gen.Device](t, se.as("GET", se.path, nil))
	if *g.Agent.Status != gen.AgentBindingStatusUnreachable || g.Agent.LastOkAt == nil || !g.Agent.LastOkAt.Equal(lastOK) || g.Agent.Version == nil {
		t.Fatalf("after the agent stopped: %+v", g.Agent)
	}

	// A PATCH that re-sends the same binding (the UI does it on every save) keeps the information.
	want(t, se.as("PATCH", se.path, map[string]any{"agent": map[string]any{"agent_id": se.agent.ag.ID(), "port": se.agent.port}}), 200)
	if g = decode[gen.Device](t, se.as("GET", se.path, nil)); *g.Agent.Status != gen.AgentBindingStatusUnreachable || g.Agent.LastOkAt == nil {
		t.Fatalf("an identical PATCH reset the status: %+v", g.Agent)
	}
	// Rebinding to another port forgets it.
	g = decode[gen.Device](t, ok(t, se.as("PATCH", se.path, map[string]any{"agent": map[string]any{"agent_id": se.agent.ag.ID(), "port": se.agent.port + 1}}), 200))
	if *g.Agent.Status != gen.AgentBindingStatusUnknown || g.Agent.LastOkAt != nil || g.Agent.Version != nil {
		t.Fatalf("a changed binding keeps old facts: %+v", g.Agent)
	}
}
