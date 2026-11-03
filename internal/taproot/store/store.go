// Package store owns taproot's SQLite database. Each taproot module owns its
// own tables and registers its migrations here, so modules never edit a shared
// schema file.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	_ "modernc.org/sqlite"
)

// Config is the store section of taproot.yaml.
type Config struct {
	// Path is the database file. Relative paths resolve against the data dir.
	Path string `yaml:"path"`
}

// Migration is one forward-only schema step owned by a module.
type Migration struct {
	Module  string
	Version int
	SQL     string
}

var (
	registryMu sync.Mutex
	registry   []Migration
)

// Register adds migrations for a module. Call it from the module's init.
func Register(migrations ...Migration) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = append(registry, migrations...)
}

// DB wraps the database handle.
type DB struct {
	*sql.DB
	path string
}

// Open opens or creates the database and applies every registered migration.
func Open(ctx context.Context, cfg Config, dataDir string) (*DB, error) {
	path := cfg.Path
	if path == "" {
		path = "taproot.db"
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(dataDir, path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create store dir: %w", err)
	}
	dsn := "file:" + filepath.ToSlash(path) + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	db := &DB{DB: sqlDB, path: path}
	if err := db.migrate(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return db, nil
}

// Path returns the database file path.
func (db *DB) Path() string { return db.path }

func (db *DB) migrate(ctx context.Context) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		module TEXT NOT NULL,
		version INTEGER NOT NULL,
		applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
		PRIMARY KEY (module, version)
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	registryMu.Lock()
	pending := append([]Migration(nil), registry...)
	registryMu.Unlock()
	sort.SliceStable(pending, func(i, j int) bool {
		if pending[i].Module != pending[j].Module {
			return pending[i].Module < pending[j].Module
		}
		return pending[i].Version < pending[j].Version
	})

	for _, m := range pending {
		var applied int
		err := db.QueryRowContext(ctx, `SELECT 1 FROM schema_migrations WHERE module = ? AND version = ?`, m.Module, m.Version).Scan(&applied)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("check migration %s/%d: %w", m.Module, m.Version, err)
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %s/%d: %w", m.Module, m.Version, err)
		}
		if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s/%d: %w", m.Module, m.Version, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (module, version) VALUES (?, ?)`, m.Module, m.Version); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %s/%d: %w", m.Module, m.Version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s/%d: %w", m.Module, m.Version, err)
		}
	}
	return nil
}

// Snapshot writes a consistent copy of the database to dest using VACUUM INTO.
// dest must not exist.
func (db *DB) Snapshot(ctx context.Context, dest string) error {
	if _, err := os.Stat(dest); err == nil {
		return fmt.Errorf("snapshot destination exists: %s", dest)
	}
	if _, err := db.ExecContext(ctx, `VACUUM INTO ?`, dest); err != nil {
		return fmt.Errorf("snapshot store: %w", err)
	}
	return nil
}
