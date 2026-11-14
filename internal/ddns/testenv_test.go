package ddns

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/thanhtunguet/homecore/internal/ddns/ddnstest"
	"github.com/thanhtunguet/homecore/internal/secrets"
	"github.com/thanhtunguet/homecore/internal/storage"
)

// fakeClock is a settable clock shared by the service and the syncer.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// fakeIP is an IPDiscoverer the test controls.
type fakeIP struct {
	mu    sync.Mutex
	v4    string
	v6    string
	err   error
	calls int
	gate  chan struct{} // when set, PublicIP waits for it to be closed
}

func (f *fakeIP) PublicIP(ctx context.Context, family RecordType) (netip.Addr, error) {
	f.mu.Lock()
	f.calls++
	gate, err, v := f.gate, f.err, f.v4
	if family == RecordAAAA {
		v = f.v6
	}
	f.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return netip.Addr{}, ctx.Err()
		}
	}
	if err != nil {
		return netip.Addr{}, err
	}
	return netip.MustParseAddr(v), nil
}

func (f *fakeIP) set(v4 string, err error) {
	f.mu.Lock()
	f.v4, f.err = v4, err
	f.mu.Unlock()
}

// quorumFailure is what the real Discoverer returns when providers disagree.
func quorumFailure() error {
	return &DiscoveryError{Family: RecordA, Answered: 2, Total: 2, Detail: "a.example: 8.8.8.8; b.example: 1.1.1.1"}
}

// env is a real SQLite database, the real repository, service and Cloudflare client, a fake
// Cloudflare server and a fake public-IP discoverer.
type env struct {
	t     *testing.T
	db    *storage.DB
	repo  *SQLiteRepository
	svc   *Service
	cf    *ddnstest.Cloudflare
	ip    *fakeIP
	clock *fakeClock
	logs  *syncBuffer
}

// syncBuffer is a goroutine-safe log sink so tests can assert nothing secret is logged.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

const zone = "z1"

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "ddns.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	sealer, err := secrets.NewSealer(bytes.Repeat([]byte{7}, secrets.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	f := ddnstest.New(testToken)
	t.Cleanup(f.Close)
	f.Zones = []ddnstest.Zone{{ID: zone, Name: "example.com"}, {ID: "z2", Name: "other.org"}}
	f.Records[zone] = []ddnstest.Record{
		{ID: "r1", Name: "home.example.com", Type: "A", Content: "8.8.4.4", TTL: 300},
		{ID: "r2", Name: "home6.example.com", Type: "AAAA", Content: "2606:4700:4700::1001", TTL: 300},
		{ID: "r3", Name: "nas.example.com", Type: "A", Content: "8.8.4.4", TTL: 300},
		{ID: "r4", Name: "www.example.com", Type: "CNAME", Content: "home.example.com", TTL: 1},
		{ID: "r5", Name: "cam.example.com", Type: "A", Content: "8.8.4.4", TTL: 300},
	}
	f.Records["z2"] = []ddnstest.Record{{ID: "r9", Name: "home.other.org", Type: "A", Content: "8.8.4.4", TTL: 1}}
	cl, err := NewCloudflare(f.URL(), true, &http.Client{Timeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, db: db, repo: NewSQLiteRepository(db), cf: f, ip: &fakeIP{v4: "8.8.8.8", v6: "2606:4700:4700::1111"},
		clock: &fakeClock{t: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)}, logs: &syncBuffer{}}
	e.svc = NewService(ServiceConfig{Repo: e.repo, Sealer: sealer, DNS: cl, IP: e.ip, Now: e.clock.Now,
		Log: slog.New(slog.NewTextHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))})
	return e
}

// mk creates a configuration for record id (r1 = home.example.com A by default).
func (e *env) mk(name, recordID string, mods ...func(*CreateInput)) Config {
	e.t.Helper()
	rec, ok := e.cf.Record(zone, recordID)
	if !ok {
		e.t.Fatalf("no record %s", recordID)
	}
	in := CreateInput{Name: name, ZoneID: zone, RecordID: recordID, RecordName: rec.Name, RecordType: RecordType(rec.Type),
		APIToken: testToken, TTL: ptrOf(rec.TTL)}
	for _, m := range mods {
		m(&in)
	}
	c, err := e.svc.Create(context.Background(), in)
	if err != nil {
		e.t.Fatalf("create %s: %v", name, err)
	}
	return c
}

func ptrOf[T any](v T) *T { return &v }

// mustSync runs one sync and fails the test on an error that is not a failed result.
func (e *env) mustSync(id string) SyncResult {
	e.t.Helper()
	res, err := e.svc.Sync(context.Background(), id)
	if err != nil {
		e.t.Fatalf("sync: %v", err)
	}
	return res
}

func (e *env) get(id string) Config {
	e.t.Helper()
	c, err := e.svc.Get(context.Background(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return c
}

func (e *env) history(id string) []Outcome {
	e.t.Helper()
	h, err := e.repo.History(context.Background(), id, 100)
	if err != nil {
		e.t.Fatal(err)
	}
	return h
}

func (e *env) count(table string) int {
	e.t.Helper()
	var n int
	if err := e.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

var errBoom = errors.New("boom")
