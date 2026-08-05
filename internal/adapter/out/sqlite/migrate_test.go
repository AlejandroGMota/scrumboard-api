package sqlite_test

import (
	"context"
	"database/sql"
	"os"
	"sort"
	"testing"

	"github.com/alejandrogmota/scrumboard-api/internal/adapter/out/sqlite"
	"github.com/google/go-cmp/cmp"
)

// What `atlas migrate lint` cannot know (02-testing.md §5.3). Three properties,
// and the third is the one nobody tests until it fails on the VM.

func rawDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", sqlite.DSN(t.TempDir()+"/scrumboard.db"))
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

func tableNames(t *testing.T, db *sql.DB) []string {
	t.Helper()

	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatalf("listing tables: %v", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scanning: %v", err)
		}
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func TestMigrateFromEmptyBuildsTheExpectedSchema(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db := rawDB(t)
	if err := sqlite.Migrate(ctx, db); err != nil {
		t.Fatalf("migrating: %v", err)
	}

	want := []string{"boards", "cards", "columns", "op_log", "schema_migrations"}
	if diff := cmp.Diff(want, tableNames(t, db)); diff != "" {
		t.Errorf("tables mismatch (-want +got):\n%s", diff)
	}
}

// The container restarts more often than anyone plans for.
func TestMigrateTwiceIsANoOp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db := rawDB(t)
	if err := sqlite.Migrate(ctx, db); err != nil {
		t.Fatalf("first run: %v", err)
	}
	before := tableNames(t, db)

	if err := sqlite.Migrate(ctx, db); err != nil {
		t.Fatalf("second run must be a no-op, got %v", err)
	}
	if diff := cmp.Diff(before, tableNames(t, db)); diff != "" {
		t.Errorf("the schema changed on the second run (-first +second):\n%s", diff)
	}

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatalf("counting applied migrations: %v", err)
	}
	versions, err := sqlite.MigrationVersions()
	if err != nil {
		t.Fatalf("listing migrations: %v", err)
	}
	if n != len(versions) {
		t.Errorf("%d rows in schema_migrations, want %d: migrations were recorded twice", n, len(versions))
	}
}

// The real production path: a container that has been running since before the
// latest migration existed.
func TestMigrateFromAnIntermediateVersion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	versions, err := sqlite.MigrationVersions()
	if err != nil {
		t.Fatalf("listing migrations: %v", err)
	}
	if len(versions) < 2 {
		t.Skip("needs at least two migrations to be meaningful")
	}

	db := rawDB(t)
	if err := sqlite.MigrateTo(ctx, db, versions[0]); err != nil {
		t.Fatalf("migrating to the first version: %v", err)
	}

	// Halfway there: the board tables exist, the op log does not yet.
	if got := tableNames(t, db); len(got) == 0 {
		t.Fatal("no tables after the first migration")
	}
	var opLog int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'op_log'`).Scan(&opLog); err != nil {
		t.Fatalf("checking for op_log: %v", err)
	}
	if opLog != 0 {
		t.Fatal("op_log exists after the first migration; the fixture no longer tests what it claims")
	}

	if err := sqlite.Migrate(ctx, db); err != nil {
		t.Fatalf("migrating the rest of the way: %v", err)
	}

	want := []string{"boards", "cards", "columns", "op_log", "schema_migrations"}
	if diff := cmp.Diff(want, tableNames(t, db)); diff != "" {
		t.Errorf("tables mismatch (-want +got):\n%s", diff)
	}
}

// The repositories and the schema have to agree. Open() migrates and then the
// first query is the proof.
func TestTheMigratedSchemaMatchesWhatTheRepositoriesExpect(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db := newDB(t)
	if _, err := db.Boards().List(ctx); err != nil {
		t.Errorf("listing boards against the migrated schema: %v", err)
	}
	if _, err := db.OpLog().Applied(ctx, "op-1"); err != nil {
		t.Errorf("reading the op log against the migrated schema: %v", err)
	}
}

// Verifying a backup must not write to it. The nightly script mounts the
// backup directory `:ro`, so anything that migrates — or even sets the WAL
// pragma — fails there with "attempt to write a readonly database", and the
// check that was supposed to prove the backup good is the thing that breaks.
func TestOpenReadOnlyDoesNotWriteToTheFile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// A real database with a real backup taken from it.
	source := newDB(t)
	_, col := seed(t, source, nil)
	insertCard(t, ctx, source, col.ID, "k1", "in the backup", 0)

	backup := t.TempDir() + "/backup.db"
	if _, err := source.SQL().ExecContext(ctx, `VACUUM INTO '`+backup+`'`); err != nil {
		t.Fatalf("taking the backup: %v", err)
	}

	// Read-only on disk, the way it arrives inside the verifying container.
	if err := os.Chmod(backup, 0o444); err != nil {
		t.Fatalf("making the backup read-only: %v", err)
	}

	db, err := sqlite.OpenReadOnly(ctx, backup)
	if err != nil {
		t.Fatalf("opening a read-only backup: %v", err)
	}
	defer db.Close()

	var result string
	if err := db.SQL().QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil {
		t.Fatalf("integrity check: %v", err)
	}
	if result != "ok" {
		t.Errorf("integrity_check = %q, want ok", result)
	}

	// And the data is really in there, not just a well-formed empty file.
	var cards int
	if err := db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM cards`).Scan(&cards); err != nil {
		t.Fatalf("counting cards: %v", err)
	}
	if cards != 1 {
		t.Errorf("%d cards in the backup, want 1", cards)
	}
}
