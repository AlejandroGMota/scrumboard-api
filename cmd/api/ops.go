package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/alejandrogmota/scrumboard-api/config"
	"github.com/alejandrogmota/scrumboard-api/internal/adapter/out/sqlite"
)

// The production image is FROM scratch: no shell, no curl, no sqlite3. Two
// jobs still need doing inside it, so the binary does them itself.

// healthcheck asks the running server whether it is well, and turns the answer
// into an exit code Docker understands.
//
//	healthcheck:
//	  test: ["CMD", "/api", "-healthcheck"]
func healthcheck() error {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	client := &http.Client{Timeout: 3 * time.Second}
	res, err := client.Get("http://127.0.0.1:" + port + "/health")
	if err != nil {
		return fmt.Errorf("health check: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("health check: server answered %d", res.StatusCode)
	}
	return nil
}

// backup writes a consistent snapshot of the database to dest.
//
// It uses VACUUM INTO rather than a file copy. With WAL enabled, copying the
// file while writes are in flight produces a snapshot that looks fine and
// fails on the day it is needed (00-scope.md §6.4). VACUUM INTO is SQLite's
// own hot-backup path: it reads through the same transaction machinery as
// everything else, so what lands on disk is a real point-in-time database,
// already compacted.
//
// The scope document says `sqlite3 .backup`, which does the same job from the
// command line. The binary does it instead because a scratch image has no
// sqlite3 in it, and adding one would mean shipping a second executable purely
// for the nightly cron.
func backup(dest string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	db, err := sqlite.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()

	if err := os.RemoveAll(dest); err != nil {
		return fmt.Errorf("clear %s: %w", dest, err)
	}
	// The destination is interpolated rather than bound: SQLite does not
	// accept a parameter here. It comes from the cron script, not a request.
	if _, err := db.SQL().ExecContext(ctx, fmt.Sprintf("VACUUM INTO %s", quoteSQLString(dest))); err != nil {
		return fmt.Errorf("backup to %s: %w", dest, err)
	}

	info, err := os.Stat(dest)
	if err != nil {
		return fmt.Errorf("stat %s: %w", dest, err)
	}
	fmt.Printf("backup written: %s (%d bytes)\n", dest, info.Size())
	return nil
}

// verify opens a backup and runs PRAGMA integrity_check on it. A backup nobody
// has ever restored is a hypothesis, not a backup.
//
// Read-only, deliberately: the backup is usually mounted `:ro`, and a check
// that had to write to the thing it was checking would be no check at all.
func verify(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	db, err := sqlite.OpenReadOnly(ctx, path)
	if err != nil {
		return err
	}
	defer db.Close()

	var result string
	if err := db.SQL().QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil {
		return fmt.Errorf("integrity check on %s: %w", path, err)
	}
	if result != "ok" {
		return fmt.Errorf("integrity check on %s: %s", path, result)
	}

	var boards, cards int
	//nolint:errcheck // the counts are informational; integrity_check already passed
	db.SQL().QueryRowContext(ctx, "SELECT COUNT(*) FROM boards WHERE deleted_at IS NULL").Scan(&boards)
	//nolint:errcheck // as above
	db.SQL().QueryRowContext(ctx, "SELECT COUNT(*) FROM cards WHERE deleted_at IS NULL").Scan(&cards)

	fmt.Printf("%s: ok — %d boards, %d cards\n", path, boards, cards)
	return nil
}

// quoteSQLString renders a string literal for SQL, doubling any single quotes.
func quoteSQLString(s string) string {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '\'')
	for i := 0; i < len(s); i++ {
		if s[i] == '\'' {
			out = append(out, '\'')
		}
		out = append(out, s[i])
	}
	return string(append(out, '\''))
}
