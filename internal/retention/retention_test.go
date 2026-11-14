package retention

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/thanhtunguet/homecore/internal/commands"
	"github.com/thanhtunguet/homecore/internal/ddns"
	"github.com/thanhtunguet/homecore/internal/devices"
	"github.com/thanhtunguet/homecore/internal/storage"
)

type clock struct{ t time.Time }

func (c *clock) Now() time.Time { return c.t }

func count(t *testing.T, db *storage.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPrunesOldRowsInBatchesAndKeepsRecentAndActiveOnes(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	clk := &clock{t: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)}
	devRepo := devices.NewSQLiteRepository(db)
	cmdRepo := commands.NewSQLiteRepository(db)
	svc := devices.NewService(devRepo, clk.Now)
	d, err := svc.Create(ctx, devices.CreateInput{Name: "pc", IPAddress: "10.0.0.1", MonitorPort: 22})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO ddns_configs (id, name, zone_id, record_name, record_type, api_token_sealed, created_at, updated_at)
		VALUES ('c1', 'home', 'z', 'home.example.com', 'A', x'00', 0, 0)`); err != nil {
		t.Fatal(err)
	}

	day := 24 * time.Hour
	age := func(days int) time.Time { return clk.t.Add(-time.Duration(days) * day) }
	// 7 old + 2 recent rows per table, retention 30 days.
	for i := range 9 {
		at := age(31 + i)
		if i >= 7 {
			at = age(1)
		}
		if err := devRepo.RecordObservation(ctx, devices.Observation{DeviceID: d.ID, At: at, Status: devices.StatusOnline, Previous: devices.StatusOffline, Changed: true}); err != nil {
			t.Fatal(err)
		}
		st := []commands.Status{commands.StatusCompleted, commands.StatusFailed, commands.StatusTimedOut}[i%3]
		if i >= 7 {
			st = commands.StatusCompleted
		}
		if err := cmdRepo.Insert(ctx, commands.Command{ID: uuid.NewString(), DeviceID: d.ID, Type: commands.TypeWake, Status: st, CreatedAt: at, UpdatedAt: at}); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO ddns_sync_history (config_id, result, at) VALUES ('c1', 'unchanged', ?)`, storage.Millis(at)); err != nil {
			t.Fatal(err)
		}
	}
	// An old command that is still active must survive: it is not history yet.
	if err := cmdRepo.Insert(ctx, commands.Command{ID: uuid.NewString(), DeviceID: d.ID, Type: commands.TypeShutdown, Status: commands.StatusSent, CreatedAt: age(60), UpdatedAt: age(60)}); err != nil {
		t.Fatal(err)
	}

	job := New(Config{
		Days: 30, BatchSize: 3, Now: clk.Now, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Targets: []Target{
			{Name: "device_status_history", Prune: devRepo.PruneHistory},
			{Name: "commands", Prune: cmdRepo.PruneTerminal},
			{Name: "ddns_sync_history", Prune: func(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
				return ddns.PruneSyncHistory(ctx, db, cutoff, limit)
			}},
		},
	})
	got := job.RunOnce(ctx)
	if got["device_status_history"] != 7 || got["commands"] != 7 || got["ddns_sync_history"] != 7 {
		t.Fatalf("deleted %v, want 7 from each table", got)
	}
	if n := count(t, db, "device_status_history"); n != 2 {
		t.Fatalf("history rows left: %d", n)
	}
	if n := count(t, db, "commands"); n != 3 { // 2 recent + 1 old but active
		t.Fatalf("command rows left: %d", n)
	}
	if n := count(t, db, "ddns_sync_history"); n != 2 {
		t.Fatalf("ddns rows left: %d", n)
	}

	// A day later the cutoff moves with the fake clock: one more row becomes eligible per table.
	clk.t = clk.t.Add(30 * day)
	got = job.RunOnce(ctx)
	if got["device_status_history"] != 2 || got["commands"] != 2 || got["ddns_sync_history"] != 2 {
		t.Fatalf("after 30 more days: %v", got)
	}
	if n := count(t, db, "commands"); n != 1 {
		t.Fatalf("only the active command may remain, have %d", n)
	}
}

func TestRunDisabledAndStopsOnCancel(t *testing.T) {
	calls := 0
	target := Target{Name: "t", Prune: func(context.Context, time.Time, int) (int64, error) { calls++; return 0, nil }}
	for _, days := range []int{0, 30} {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		job := New(Config{Days: days, Interval: time.Hour, Targets: []Target{target}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
		go func() { done <- job.Run(ctx) }()
		time.Sleep(30 * time.Millisecond)
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("days=%d: Run did not stop", days)
		}
	}
	if calls != 1 { // only the enabled job ran its startup pass
		t.Fatalf("prune calls = %d, want 1 (disabled job must not prune)", calls)
	}
}

func TestPruneErrorStopsThatTargetOnly(t *testing.T) {
	boom := errors.New("boom")
	ran := false
	job := New(Config{Days: 1, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Targets: []Target{
		{Name: "bad", Prune: func(context.Context, time.Time, int) (int64, error) { return 0, boom }},
		{Name: "good", Prune: func(context.Context, time.Time, int) (int64, error) { ran = true; return 0, nil }},
	}})
	job.RunOnce(context.Background())
	if !ran {
		t.Fatal("a failing target must not block the others")
	}
}
