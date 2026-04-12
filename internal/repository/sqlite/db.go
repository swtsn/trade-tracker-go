// Package sqlite provides a SQLite-backed implementation of the repository interfaces.
package sqlite

import (
	"database/sql"
	"embed"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Open opens a SQLite database at the given path, applies connection pragmas,
// and runs any pending migrations. Use ":memory:" for in-memory databases.
func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// Single connection ensures pragmas apply to all operations and avoids
	// multiple writers competing in WAL mode for this single-process server.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("pragma journal_mode: %w", err)
	}
	if _, err := db.Exec(`PRAGMA synchronous=NORMAL`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("pragma synchronous: %w", err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("pragma foreign_keys: %w", err)
	}

	if err := runMigrations(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrations: %w", err)
	}

	return db, nil
}

// runMigrations checks the schema_migrations table and applies any pending migrations.
func runMigrations(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`)
	if err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	type migration struct {
		version int
		path    string
	}
	migrations := []migration{
		{1, "migrations/001_initial_schema.sql"},
		{2, "migrations/002_chains.sql"},
		{3, "migrations/003_import_history.sql"},
		{5, "migrations/005_position_closed_at.sql"},
		{6, "migrations/006_position_strategy.sql"},
	}

	for _, m := range migrations {
		sqlBytes, err := migrationsFS.ReadFile(m.path)
		if err != nil {
			return fmt.Errorf("read migration %d: %w", m.version, err)
		}

		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", m.version, err)
		}

		// Check and apply atomically within the same transaction to avoid a
		// TOCTOU race when multiple processes share the same DB file.
		var count int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, m.version).Scan(&count); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("check migration %d: %w", m.version, err)
		}
		if count > 0 {
			_ = tx.Rollback()
			continue
		}

		if _, err := tx.Exec(string(sqlBytes)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("exec migration %d: %w", m.version, err)
		}

		if _, err := tx.Exec(
			`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
			m.version, time.Now().UTC().Format(time.RFC3339),
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %d: %w", m.version, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", m.version, err)
		}
	}

	return nil
}
