package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/stables/stab/migrations"

	_ "modernc.org/sqlite" // pure-Go SQLite driver
)

// Store wraps a *sql.DB with typed accessors.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the SQLite database at path and applies all
// pending migrations.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("store: mkdir: %w", err)
	}
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite: single writer avoids SQLITE_BUSY
	if err := migrations.Apply(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close releases the database.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the underlying handle for advanced queries.
func (s *Store) DB() *sql.DB { return s.db }

// nullable maps an empty string to SQL NULL.
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
