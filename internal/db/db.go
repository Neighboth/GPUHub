package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	_ "modernc.org/sqlite"
)

// Open opens (and if needed creates) the SQLite database with WAL mode,
// foreign_keys ON and busy_timeout=5000, then applies embedded migrations.
func Open(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)", path)
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(8)
	conn.SetMaxIdleConns(4)
	if err := conn.Ping(); err != nil {
		return nil, err
	}
	// Best effort: 0600 on the DB file (and -wal).
	_ = os.Chmod(path, 0o600)

	if err := Migrate(conn); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

// Migrate applies internal/db/migrations/*.sql in filename order.
// Tracked in schema_migrations (goose-compatible enough: one tx per file, applied once).
func Migrate(conn *sql.DB) error {
	if _, err := conn.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (filename TEXT PRIMARY KEY, applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		return err
	}
	migrations, err := migrationsFS()
	if err != nil {
		return err
	}
	names := make([]string, 0, len(migrations))
	for name := range migrations {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		var existing string
		err := conn.QueryRow(`SELECT filename FROM schema_migrations WHERE filename = ?`, name).Scan(&existing)
		if err == nil {
			continue
		}
		if err != sql.ErrNoRows {
			return err
		}
		tx, err := conn.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(migrations[name])); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(filename) VALUES (?)`, name); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
