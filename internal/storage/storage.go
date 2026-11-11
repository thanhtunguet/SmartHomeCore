// Package storage owns the SQLite database: connection setup, pragmas, migrations and
// transaction helpers. Domain packages define their own repository interfaces and keep their SQL
// next to the domain code; they receive a *DB (or *sql.Tx) from here.
package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver, registers "sqlite" (ADR-004)
)

// DB wraps *sql.DB with HomeCore helpers.
type DB struct {
	*sql.DB
}

// Open opens (creating if needed) the database at path with WAL, busy timeout and foreign keys.
func Open(ctx context.Context, path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	// Pre-create the file owner-only: it holds password hashes and sealed secrets.
	if f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600); err != nil {
		return nil, fmt.Errorf("create database file: %w", err)
	} else {
		_ = f.Close()
	}
	q := url.Values{}
	for _, p := range []string{
		"journal_mode(WAL)",
		"busy_timeout(5000)",
		"foreign_keys(1)",
		"synchronous(NORMAL)",
	} {
		q.Add("_pragma", p)
	}
	q.Set("_txlock", "immediate") // writers take the lock up front: no mid-transaction SQLITE_BUSY
	dsn := (&url.URL{Scheme: "file", Opaque: path, RawQuery: q.Encode()}).String()

	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	sqlDB.SetMaxOpenConns(4)
	sqlDB.SetMaxIdleConns(4)
	sqlDB.SetConnMaxIdleTime(5 * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	return &DB{DB: sqlDB}, nil
}

// WithTx runs fn in a transaction, committing on nil error and rolling back otherwise.
func (db *DB) WithTx(ctx context.Context, fn func(tx *sql.Tx) error) (err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// Backup writes a consistent snapshot of the live database to dst (must not exist).
func (db *DB) Backup(ctx context.Context, dst string) error {
	if _, err := os.Stat(dst); err == nil {
		return errors.New("backup destination already exists")
	}
	_, err := db.ExecContext(ctx, "VACUUM INTO ?", dst)
	return err
}

// Millis converts t to the storage timestamp format (unix milliseconds, UTC).
func Millis(t time.Time) int64 { return t.UnixMilli() }

// FromMillis converts a stored timestamp back to time.Time (UTC).
func FromMillis(ms int64) time.Time { return time.UnixMilli(ms).UTC() }
