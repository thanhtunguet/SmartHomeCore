package audit

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/thanhtunguet/homecore/internal/storage"
)

func TestWriteStoresEntry(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 14, 8, 0, 0, 0, time.UTC)
	w := NewWriter(db, func() time.Time { return at })

	if err := w.Write(ctx, Entry{ActorUserID: "u1", Actor: "alice", Action: "device.shutdown", TargetType: "device",
		TargetID: "d1", Result: ResultOK, RemoteAddr: "10.0.0.2", Details: map[string]any{"agent": "a1"}}); err != nil {
		t.Fatal(err)
	}
	if err := w.Write(ctx, Entry{Action: "auth.login", Result: ResultFailed}); err != nil {
		t.Fatal(err)
	}

	var (
		actorID                                         *string
		ts                                              int64
		actor, action, tt, tid, result, remote, details string
	)
	if err := db.QueryRowContext(ctx, `SELECT at, actor_user_id, actor, action, target_type, target_id, result, remote_addr, details
		FROM audit_log WHERE action = 'device.shutdown'`).Scan(&ts, &actorID, &actor, &action, &tt, &tid, &result, &remote, &details); err != nil {
		t.Fatal(err)
	}
	if ts != at.UnixMilli() || actorID == nil || *actorID != "u1" || actor != "alice" || tt != "device" || tid != "d1" ||
		result != "ok" || remote != "10.0.0.2" || details != `{"agent":"a1"}` {
		t.Fatalf("row = %d %v %s %s %s %s %s %s %s", ts, actorID, actor, action, tt, tid, result, remote, details)
	}
	// An anonymous actor stores NULL, not an empty id, and empty details are "{}".
	if err := db.QueryRowContext(ctx, `SELECT actor_user_id, details FROM audit_log WHERE action = 'auth.login'`).Scan(&actorID, &details); err != nil {
		t.Fatal(err)
	}
	if actorID != nil || details != "{}" {
		t.Fatalf("anonymous row: actor_user_id=%v details=%q", actorID, details)
	}
	if err := w.Write(ctx, Entry{}); err == nil {
		t.Fatal("entry without action accepted")
	}
}

func TestWriteTxRollsBackWithTheAction(t *testing.T) {
	ctx := context.Background()
	db, _ := storage.Open(ctx, filepath.Join(t.TempDir(), "a.db"))
	defer db.Close()
	db.Migrate(ctx)
	w := NewWriter(db, nil)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.WriteTx(ctx, tx, Entry{Action: "x"}); err != nil {
		t.Fatal(err)
	}
	tx.Rollback()
	var n int
	db.QueryRowContext(ctx, `SELECT count(*) FROM audit_log`).Scan(&n)
	if n != 0 {
		t.Fatal("audit row survived a rolled back transaction")
	}
}
