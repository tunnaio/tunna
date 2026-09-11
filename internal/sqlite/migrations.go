package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"strconv"
	"strings"
)

// migrations holds the numbered SQL files under migrations/, compiled into
// the binary. The directive must sit directly above the variable.
//
//go:embed migrations/*.sql
var migrations embed.FS

// migrate applies every embedded migration whose number exceeds the
// database's user_version, in order, each in its own transaction that also
// records the new version. A shipped file is never edited; a change is a new
// file with the next number.
func migrate(ctx context.Context, db *sql.DB, logger *slog.Logger) error {
	var current int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return err
	}
	found := current

	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		return err
	}

	for _, e := range entries {
		numText, _, ok := strings.Cut(e.Name(), "_")
		if !ok {
			return fmt.Errorf("migration %q: name must be NNNN_description.sql", e.Name())
		}
		n, err := strconv.Atoi(numText)
		if err != nil {
			return fmt.Errorf("migration %q: %w", e.Name(), err)
		}
		if n <= current {
			continue
		}
		path := "migrations/" + e.Name()
		ddl, err := migrations.ReadFile(path)
		if err != nil {
			return err
		}
		if err := applyOne(ctx, db, n, string(ddl)); err != nil {
			return fmt.Errorf("migration %q: %w", e.Name(), err)
		}
		current = n
		logger.Info("migration applied", "file", e.Name(), "version", n)
	}

	logger.Info("schema", "found", found, "version", current)
	return nil
}

// applyOne runs one migration and the user_version bump in a single
// transaction, so a failure leaves the file at the previous version.
func applyOne(ctx context.Context, db *sql.DB, version int, ddl string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() // no-op after a successful Commit
	if _, err := tx.ExecContext(ctx, ddl); err != nil {
		return err
	}
	// PRAGMA does not accept bound parameters; version is an int we parsed
	// from a filename, so formatting it in is safe.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
		return err
	}
	return tx.Commit()
}
