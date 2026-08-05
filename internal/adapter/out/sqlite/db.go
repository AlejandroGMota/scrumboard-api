// Package sqlite is the only place in the project that knows SQL exists.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver: no cgo, so the image stays static
)

// DSN builds the connection string for a database file.
//
// Three pragmas matter and all three are deliberate:
//
//   - journal_mode(WAL): readers do not block the writer. It is also why
//     backups must use `sqlite3 .backup` rather than `cp` (00-scope.md §6.4).
//   - busy_timeout(5000): wait rather than fail on a held lock. Belt and
//     braces next to the single-writer pool below.
//   - foreign_keys(1): SQLite has them off by default, which is a trap. The
//     schema declares them, so they had better be enforced.
//
// _txlock=immediate takes the write lock when a transaction begins instead of
// on its first write, which is what turns the classic "database is locked"
// mid-transaction upgrade error into a wait.
func DSN(path string) string {
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Set("_txlock", "immediate")
	return "file:" + path + "?" + q.Encode()
}

// Open opens the database, applies the pragmas and runs any pending migration.
//
// The pool is capped at one connection. With a single user that costs nothing
// measurable, and it removes "database is locked" as a category rather than
// mitigating it: SQLite allows exactly one writer, so a pool that can hand out
// two connections is a pool that can produce a conflict (00-scope.md §6.3).
func Open(ctx context.Context, path string) (*DB, error) {
	db, err := sql.Open("sqlite", DSN(path))
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite %s: %w", path, err)
	}
	if err := Migrate(ctx, db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate %s: %w", path, err)
	}
	return &DB{db: db}, nil
}

// OpenReadOnly opens an existing database without migrating it, and without
// taking a write lock.
//
// It exists for one caller: checking a backup. Verifying a backup must not
// modify the backup — and it cannot, because the file is mounted read-only.
// Open() would fail there twice over: it applies migrations, and even the
// WAL pragma is a write.
func OpenReadOnly(ctx context.Context, path string) (*DB, error) {
	q := url.Values{}
	q.Set("mode", "ro")
	q.Add("_pragma", "busy_timeout(5000)")

	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s read-only: %w", path, err)
	}
	db.SetMaxOpenConns(1)

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite %s: %w", path, err)
	}
	return &DB{db: db}, nil
}

// DB wraps the pool and hands out the repositories, so nothing outside this
// package holds a *sql.DB it could use to go around them.
type DB struct{ db *sql.DB }

func (d *DB) Close() error { return d.db.Close() }

// SQL exposes the pool for the health check and for tests that assert on
// persistence underneath the API (01-structure.md §1.7).
func (d *DB) SQL() *sql.DB { return d.db }

func (d *DB) Boards() *BoardRepo   { return &BoardRepo{d} }
func (d *DB) Columns() *ColumnRepo { return &ColumnRepo{d} }
func (d *DB) Cards() *CardRepo     { return &CardRepo{d} }
func (d *DB) OpLog() *OpLogRepo    { return &OpLogRepo{d} }

// Ping is what GET /health reports on. A health check that only proves the
// process started will not catch a misconfigured volume (02-testing.md §4.1).
func (d *DB) Ping(ctx context.Context) error {
	var one int
	return d.db.QueryRowContext(ctx, "SELECT 1").Scan(&one)
}

// --- time and null helpers ------------------------------------------------

// timeLayout is RFC 3339 with nanoseconds. Text, sortable, and unambiguous
// because everything stored is UTC.
const timeLayout = time.RFC3339Nano

func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(timeLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse timestamp %q: %w", s, err)
	}
	return t.UTC(), nil
}

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return formatTime(*t)
}

func scanNullTime(ns sql.NullString) (*time.Time, error) {
	if !ns.Valid {
		return nil, nil
	}
	t, err := parseTime(ns.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func nullInt(i *int) any {
	if i == nil {
		return nil
	}
	return *i
}

func scanNullInt(ni sql.NullInt64) *int {
	if !ni.Valid {
		return nil
	}
	v := int(ni.Int64)
	return &v
}
