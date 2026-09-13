// Package sqlite is the metadata store adapter (ADR-0002): SQLite in WAL mode
// with synchronous=FULL, one file, migrations embedded in the binary. It
// implements the root package's store interfaces on a single DB type and is
// the only package that imports the driver.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	_ "modernc.org/sqlite" // registers the "sqlite" driver with database/sql
)

// DB is an open metadata database. It satisfies the root package's store
// interfaces; each is a method set on this one type.
type DB struct {
	db     *sql.DB
	logger *slog.Logger
}

// Open opens or creates the database at path, applies the connection pragmas
// from docs/schema.md through the DSN so every pooled connection gets them,
// and runs any embedded migration newer than the file's user_version.
func Open(path string, logger *slog.Logger) (*DB, error) {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := migrate(context.Background(), db, logger); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite: migrate: %w", err)
	}
	return &DB{db: db, logger: logger}, nil
}

// Close releases the connection pool.
func (d *DB) Close() error {
	return d.db.Close()
}
