package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func openTemp(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestMigrateIsIdempotentAndEnablesPragmas(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)

	n, err := db.Migrate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("expected migrations to be applied")
	}
	if n2, err := db.Migrate(ctx); err != nil || n2 != 0 {
		t.Fatalf("second migrate: applied=%d err=%v", n2, err)
	}
	if v, _ := db.SchemaVersion(ctx); v != LatestVersion() {
		t.Fatalf("schema version %d, want %d", v, LatestVersion())
	}

	var mode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode=%q err=%v", mode, err)
	}
	var fk int
	if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign_keys=%d err=%v", fk, err)
	}
}

func TestActiveCommandUniqueness(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	mustExec := func(q string, args ...any) error {
		_, err := db.ExecContext(ctx, q, args...)
		return err
	}
	if err := mustExec(`INSERT INTO devices (id, name, ip_address, monitor_port, created_at, updated_at)
		VALUES ('d1', 'pc', '192.168.1.20', 22, 0, 0)`); err != nil {
		t.Fatal(err)
	}
	insert := `INSERT INTO commands (id, device_id, type, status, created_at, updated_at) VALUES (?, 'd1', 'wake', ?, 0, 0)`
	if err := mustExec(insert, "c1", "pending"); err != nil {
		t.Fatal(err)
	}
	if err := mustExec(insert, "c2", "sent"); err == nil {
		t.Fatal("second active wake command for the same device must be rejected")
	}
	if err := mustExec(`UPDATE commands SET status = 'completed' WHERE id = 'c1'`); err != nil {
		t.Fatal(err)
	}
	if err := mustExec(insert, "c3", "pending"); err != nil {
		t.Fatalf("new command after completion must be allowed: %v", err)
	}
}

func TestDeviceMetaColumnsDefaultAndConstrain(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO devices (id, name, ip_address, monitor_port, created_at, updated_at)
		VALUES ('d1', 'pc', '192.168.1.20', 22, 0, 0)`); err != nil {
		t.Fatal(err)
	}
	var typ, os string
	var latency *int
	if err := db.QueryRowContext(ctx, `SELECT device_type, os, last_latency_ms FROM devices WHERE id='d1'`).Scan(&typ, &os, &latency); err != nil {
		t.Fatal(err)
	}
	if typ != "desktop" || os != "linux" || latency != nil {
		t.Fatalf("defaults: type=%q os=%q latency=%v", typ, os, latency)
	}
	if _, err := db.ExecContext(ctx, `UPDATE devices SET device_type='toaster' WHERE id='d1'`); err == nil {
		t.Fatal("invalid device_type accepted")
	}
	if _, err := db.ExecContext(ctx, `UPDATE devices SET os='beos' WHERE id='d1'`); err == nil {
		t.Fatal("invalid os accepted")
	}
}

// ADR-011: accounts have no role; a user row needs only credentials.
func TestUsersHaveNoRoleColumn(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO users (id, username, password_hash, created_at, updated_at)
		VALUES ('u1', 'alice', 'hash', 0, 0)`); err != nil {
		t.Fatalf("insert without role: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('users') WHERE name='role'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("role column still present (n=%d, err=%v)", n, err)
	}
}

// Migration 0004: the sealed copy of the successor pair lives on the (used) refresh token row and
// is nullable, so tokens issued before the migration and unused tokens carry none.
func TestRefreshTokensHaveNullableSuccessorColumn(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO users (id, username, password_hash, created_at, updated_at) VALUES ('u1', 'alice', 'h', 0, 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO refresh_tokens (id, user_id, family_id, token_hash, expires_at, created_at)
		VALUES ('r1', 'u1', 'f1', x'01', 1, 0)`); err != nil {
		t.Fatalf("insert without successor_sealed: %v", err)
	}
	var sealed []byte
	if err := db.QueryRowContext(ctx, `SELECT successor_sealed FROM refresh_tokens WHERE id = 'r1'`).Scan(&sealed); err != nil || sealed != nil {
		t.Fatalf("successor_sealed = %v, err %v; want NULL", sealed, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE refresh_tokens SET successor_sealed = x'0a0b' WHERE id = 'r1'`); err != nil {
		t.Fatal(err)
	}
}
