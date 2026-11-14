package agentctl

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/thanhtunguet/homecore/internal/devices"
	"github.com/thanhtunguet/homecore/internal/storage"
)

type healthEnv struct {
	t    *testing.T
	repo *devices.SQLiteRepository
	svc  *devices.Service
	now  time.Time
	mu   sync.Mutex
}

func newHealthEnv(t *testing.T) *healthEnv {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := devices.NewSQLiteRepository(db)
	return &healthEnv{t: t, repo: repo, svc: devices.NewService(repo, nil), now: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)}
}

func (e *healthEnv) clock() time.Time { e.mu.Lock(); defer e.mu.Unlock(); return e.now }
func (e *healthEnv) advance(d time.Duration) {
	e.mu.Lock()
	e.now = e.now.Add(d)
	e.mu.Unlock()
}

func (e *healthEnv) job(ticks <-chan time.Time) *Health {
	return NewHealth(HealthConfig{
		Store: e.repo, Now: e.clock, Ticks: ticks, Timeout: 500 * time.Millisecond, Concurrency: 2,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

// add creates a device bound to agentID at host:port with the given observed status.
func (e *healthEnv) add(name, host string, port int, agentID string, status devices.Status) devices.Device {
	e.t.Helper()
	ctx := context.Background()
	d, err := e.svc.Create(ctx, devices.CreateInput{
		Name: name, IPAddress: host, MonitorPort: 22, Agent: &devices.AgentInput{AgentID: agentID, Port: port},
	})
	if err != nil {
		e.t.Fatal(err)
	}
	if status != devices.StatusUnknown {
		if err := e.repo.RecordObservation(ctx, devices.Observation{
			DeviceID: d.ID, At: e.now, Reachable: status == devices.StatusOnline, Status: status, Previous: devices.StatusUnknown, Changed: true,
		}); err != nil {
			e.t.Fatal(err)
		}
	}
	return d
}

func (e *healthEnv) agent(id string) devices.AgentBinding {
	e.t.Helper()
	d, err := e.svc.Get(context.Background(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return *d.Agent
}

// healthz answers like the real agent for agentID.
func healthz(agentID, version string, hits *atomic.Int32) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			hits.Add(1)
		}
		if r.URL.Path != protocolHealth || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, 200, map[string]string{"status": "ok", "agent_id": agentID, "version": version})
	})
}

const protocolHealth = "/agent/v1/healthz"

func hostPortOf(t *testing.T, s *httptest.Server) (string, int) {
	t.Helper()
	h, p, _ := net.SplitHostPort(strings.TrimPrefix(s.URL, "http://"))
	port, _ := strconv.Atoi(p)
	return h, port
}

// REQ-API-021: an online device's agent is asked for /healthz; status, version and last_ok_at are stored.
func TestHealthPassMarksAReachableAgentReady(t *testing.T) {
	e := newHealthEnv(t)
	agentID := uuid.NewString()
	var hits atomic.Int32
	srv := httptest.NewServer(healthz(agentID, "v1.4.0", &hits))
	defer srv.Close()
	host, port := hostPortOf(t, srv)
	d := e.add("pc", host, port, agentID, devices.StatusOnline)

	if got := e.agent(d.ID); got.Status != devices.AgentUnknown || got.LastOKAt != nil {
		t.Fatalf("before: %+v", got)
	}
	e.job(nil).Pass(context.Background())
	got := e.agent(d.ID)
	if got.Status != devices.AgentReady || got.Version != "v1.4.0" || got.LastOKAt == nil || !got.LastOKAt.Equal(e.now) || hits.Load() != 1 {
		t.Fatalf("after: %+v hits=%d", got, hits.Load())
	}
}

// The returned agent_id must be the bound one (REQ-API-021, protocol §6): another agent answering at
// the address (the IP was reassigned) is unreachable, not ready.
func TestHealthRejectsAnotherAgentAnsweringAtTheAddress(t *testing.T) {
	e := newHealthEnv(t)
	srv := httptest.NewServer(healthz(uuid.NewString(), "v9", nil)) // not the bound agent
	defer srv.Close()
	host, port := hostPortOf(t, srv)
	d := e.add("pc", host, port, uuid.NewString(), devices.StatusOnline)
	if err := e.repo.RecordAgentContact(context.Background(), d.ID, d.Agent.AgentID, devices.AgentContact{OK: true, Version: "old", At: e.now}); err != nil {
		t.Fatal(err)
	}
	e.advance(time.Minute)
	e.job(nil).Pass(context.Background())
	got := e.agent(d.ID)
	if got.Status != devices.AgentUnreachable || got.Version != "old" || got.LastOKAt == nil || got.LastOKAt.Equal(e.now) {
		t.Fatalf("wrong agent answered: %+v", got)
	}
}

func TestHealthTreatsDeadAndForeignServicesAsUnreachable(t *testing.T) {
	e := newHealthEnv(t)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	deadPort := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>")) }))
	defer web.Close()
	wh, wp := hostPortOf(t, web)

	dead := e.add("dead", "127.0.0.1", deadPort, uuid.NewString(), devices.StatusOnline)
	foreign := e.add("web", wh, wp, uuid.NewString(), devices.StatusOnline)
	e.job(nil).Pass(context.Background())
	for _, d := range []devices.Device{dead, foreign} {
		if got := e.agent(d.ID); got.Status != devices.AgentUnreachable {
			t.Fatalf("%s: %+v", d.Name, got)
		}
	}
}

// Offline hosts are never contacted; a stale `ready` is corrected without any request.
func TestHealthDoesNotContactOfflineOrUnknownDevices(t *testing.T) {
	e := newHealthEnv(t)
	var hits atomic.Int32
	agentOff, agentUnk := uuid.NewString(), uuid.NewString()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		healthz(agentOff, "v", nil).ServeHTTP(w, r)
	}))
	defer srv.Close()
	host, port := hostPortOf(t, srv)
	off := e.add("off", host, port, agentOff, devices.StatusOffline)
	unk := e.add("unk", host, port, agentUnk, devices.StatusUnknown)
	ctx := context.Background()
	for _, d := range []devices.Device{off, unk} {
		if err := e.repo.RecordAgentContact(ctx, d.ID, d.Agent.AgentID, devices.AgentContact{OK: true, Version: "v", At: e.now}); err != nil {
			t.Fatal(err)
		}
	}
	e.job(nil).Pass(ctx)
	if hits.Load() != 0 {
		t.Fatalf("%d requests were sent to offline/unknown devices", hits.Load())
	}
	if got := e.agent(off.ID); got.Status != devices.AgentUnreachable {
		t.Fatalf("offline device: %+v", got)
	}
	if got := e.agent(unk.ID); got.Status != devices.AgentReady {
		t.Fatalf("an unknown device's status must not be touched: %+v", got)
	}
}

// A device that is not bound is ignored, and nothing is recorded for it.
func TestHealthIgnoresUnboundDevices(t *testing.T) {
	e := newHealthEnv(t)
	if _, err := e.svc.Create(context.Background(), devices.CreateInput{Name: "plain", IPAddress: "127.0.0.1", MonitorPort: 22}); err != nil {
		t.Fatal(err)
	}
	e.job(nil).Pass(context.Background()) // must not panic or fail
}

// Concurrency is bounded and a pass waits for all its checks.
func TestHealthPassIsBoundedAndComplete(t *testing.T) {
	e := newHealthEnv(t)
	var inflight, peak atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := inflight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
		inflight.Add(-1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	host, port := hostPortOf(t, srv)
	var ds []devices.Device
	for i := 0; i < 6; i++ {
		ds = append(ds, e.add("pc"+strconv.Itoa(i), host, port, uuid.NewString(), devices.StatusOnline))
	}
	e.job(nil).Pass(context.Background())
	if p := peak.Load(); p > 2 || p == 0 {
		t.Fatalf("peak concurrency %d, want 1..2", p)
	}
	for _, d := range ds {
		if got := e.agent(d.ID); got.Status != devices.AgentUnreachable { // 204 is not a healthy agent
			t.Fatalf("%s not checked: %+v", d.Name, got)
		}
	}
}

// Run: one pass at start, one per tick, and it stops with the context without leaking goroutines.
func TestHealthRunChecksAtStartAndOnEveryTickAndStops(t *testing.T) {
	e := newHealthEnv(t)
	agentID := uuid.NewString()
	var hits atomic.Int32
	srv := httptest.NewServer(healthz(agentID, "v1", &hits))
	defer srv.Close()
	host, port := hostPortOf(t, srv)
	d := e.add("pc", host, port, agentID, devices.StatusOnline)

	before := runtime.NumGoroutine()
	ticks := make(chan time.Time)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- e.job(ticks).Run(ctx) }()

	waitFor := func(n int32) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for hits.Load() < n && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if hits.Load() != n {
			t.Fatalf("%d health requests, want %d", hits.Load(), n)
		}
	}
	waitFor(1) // the pass at start
	first := e.agent(d.ID).LastOKAt
	e.advance(time.Minute)
	ticks <- e.now // a minute later
	waitFor(2)
	time.Sleep(20 * time.Millisecond)
	if second := e.agent(d.ID).LastOKAt; first == nil || second == nil || !second.After(*first) {
		t.Fatalf("last_ok_at did not move with the clock: %v -> %v", first, second)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after cancel")
	}
	deadline := time.Now().Add(time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutines leaked: %d before, %d after", before, after)
	}
}

// A cancelled pass says nothing about the agents (no false "unreachable" while shutting down).
func TestHealthCancelledPassRecordsNothing(t *testing.T) {
	e := newHealthEnv(t)
	agentID := uuid.NewString()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	host, port := hostPortOf(t, srv)
	d := e.add("pc", host, port, agentID, devices.StatusOnline)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	e.job(nil).Pass(ctx)
	if got := e.agent(d.ID); got.Status != devices.AgentUnknown {
		t.Fatalf("a cancelled pass changed the status: %+v", got)
	}
}
