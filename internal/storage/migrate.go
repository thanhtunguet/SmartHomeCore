package storage

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Migration is one versioned schema change, loaded from migrations/NNNN_name.sql.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// Migrations returns the embedded migrations sorted by version.
func Migrations() ([]Migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, err
	}
	var out []Migration
	seen := map[int]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		prefix, rest, ok := strings.Cut(strings.TrimSuffix(name, ".sql"), "_")
		v, err := strconv.Atoi(prefix)
		if !ok || err != nil || v <= 0 {
			return nil, fmt.Errorf("migration %q: name must be NNNN_description.sql", name)
		}
		if prev, dup := seen[v]; dup {
			return nil, fmt.Errorf("migration version %d duplicated: %s and %s", v, prev, name)
		}
		seen[v] = name
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return nil, err
		}
		out = append(out, Migration{Version: v, Name: rest, SQL: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// Migrate applies all pending migrations, each in its own transaction.
func (db *DB) Migrate(ctx context.Context) (applied int, err error) {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		name       TEXT    NOT NULL,
		applied_at INTEGER NOT NULL
	) STRICT`); err != nil {
		return 0, fmt.Errorf("create schema_migrations: %w", err)
	}
	current, err := db.SchemaVersion(ctx)
	if err != nil {
		return 0, err
	}
	migs, err := Migrations()
	if err != nil {
		return 0, err
	}
	for _, m := range migs {
		if m.Version <= current {
			continue
		}
		err := db.WithTx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx,
				`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
				m.Version, m.Name, Millis(time.Now()))
			return err
		})
		if err != nil {
			return applied, fmt.Errorf("migration %04d_%s: %w", m.Version, m.Name, err)
		}
		applied++
	}
	return applied, nil
}

// SchemaVersion returns the highest applied migration version (0 if none).
func (db *DB) SchemaVersion(ctx context.Context) (int, error) {
	var v sql.NullInt64
	err := db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&v)
	if err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return int(v.Int64), nil
}

// LatestVersion returns the newest embedded migration version.
func LatestVersion() int {
	migs, err := Migrations()
	if err != nil || len(migs) == 0 {
		return 0
	}
	return migs[len(migs)-1].Version
}
