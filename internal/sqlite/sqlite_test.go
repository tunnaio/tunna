package sqlite_test

import (
	"database/sql"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/tunnaio/tunna/internal/sqlite"
)

// openFresh opens a database in a per-test temporary directory. The file is
// removed when the test ends.
func openFresh(t *testing.T) (*sqlite.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tunna.db")
	db, err := sqlite.Open(path, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db, path
}

// inspect opens a second, plain connection to the same file so the test can
// read what the adapter actually wrote. Trust the file, not the adapter.
func inspect(t *testing.T, path string) *sql.DB {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	t.Cleanup(func() { raw.Close() })
	return raw
}

func TestOpenAppliesMigrations(t *testing.T) {
	_, path := openFresh(t)
	raw := inspect(t, path)

	var version int
	if err := raw.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("user_version: %v", err)
	}
	if version != 2 {
		t.Errorf("user_version = %d, want 2", version)
	}

	for _, table := range []string{"api_keys", "buckets", "objects"} {
		var n int
		err := raw.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&n)
		if err != nil {
			t.Fatalf("sqlite_master: %v", err)
		}
		if n != 1 {
			t.Errorf("table %s missing after migration", table)
		}
	}

	var mode string
	if err := raw.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal (it is persistent in the file, so a plain connection sees it)", mode)
	}
}

func TestOpenTwiceIsIdempotent(t *testing.T) {
	first, path := openFresh(t)
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	second, err := sqlite.Open(path, nil) // nil logger must be accepted
	if err != nil {
		t.Fatalf("second Open on an already-migrated file: %v", err)
	}
	defer second.Close()

	var version int
	if err := inspect(t, path).QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 2 {
		t.Errorf("user_version after second open = %d, want 2", version)
	}
}

func TestOpenRejectsUnwritablePath(t *testing.T) {
	_, err := sqlite.Open(filepath.Join(t.TempDir(), "no-such-dir", "tunna.db"), nil)
	if err == nil {
		t.Fatal("Open succeeded on a path whose directory does not exist")
	}
}
