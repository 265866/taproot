// Package store owns taproot's SQLite database. Each taproot module owns its
// own tables and registers its current schema here, so modules never edit a
// shared schema file.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

// Config is the store section of taproot.yaml.
type Config struct {
	// Path is the database file. Relative paths resolve against the data dir.
	Path string `yaml:"path"`
}

// Schema is a module's current tables and indexes. The repository holds only
// the current schema: a change to a table the deployed database already has
// is applied to that database by hand.
type Schema struct {
	Module string
	// SQL creates the module's tables and indexes. Every statement must be
	// idempotent (CREATE TABLE IF NOT EXISTS, CREATE INDEX IF NOT EXISTS).
	SQL string
	// Seed fills the module's tables. It runs only when Open creates the
	// database, so rows a user deletes or edits stay that way, and a seed added
	// later never reaches a deployed database.
	Seed string
}

var (
	registryMu sync.Mutex
	registry   []Schema
)

// Register adds a module's schema. Call it once from the module's init.
func Register(s Schema) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = append(registry, s)
}

// DB wraps the database handle.
type DB struct {
	*sql.DB
	path string
}

// Open opens or creates the database and applies every registered schema.
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
	// temp_store(MEMORY): the container runs with a read-only root and only the
	// data dir mounted, so SQLite's temp-file search (TMPDIR, /var/tmp, /tmp)
	// may find nowhere to spill sorts and GROUP BYs. Taproot's temp structures
	// are bounded by query results, not by table size.
	dsn := "file:" + filepath.ToSlash(path) + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)&_pragma=temp_store(MEMORY)"
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	// A dashboard page load reads through several connections at once.
	// database/sql keeps 2 idle by default, so every burst would open (and
	// later close) the rest, paying SQLite's open, the pragmas, a schema
	// parse, and a cold page cache each time.
	sqlDB.SetMaxIdleConns(8)
	db := &DB{DB: sqlDB, path: path}
	if err := db.applySchema(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return db, nil
}

// Path returns the database file path.
func (db *DB) Path() string { return db.path }

// applySchema creates missing tables and indexes, and seeds a database it
// created, in module order and in one transaction. A database whose existing
// tables lack declared columns is refused and left unchanged.
func (db *DB) applySchema(ctx context.Context) error {
	schemas := registered()
	want, err := declaredColumns(ctx, schemas)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin schema: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	existed, err := tableNames(ctx, tx)
	if err != nil {
		return err
	}
	// Check before applying: a declared index on a missing column would
	// otherwise fail with a bare "no such column".
	if err := checkColumns(ctx, tx, want, existed); err != nil {
		return err
	}
	for _, s := range schemas {
		if _, err := tx.ExecContext(ctx, s.SQL); err != nil {
			return fmt.Errorf("apply %s schema: %w", s.Module, err)
		}
	}
	if len(existed) == 0 {
		for _, s := range schemas {
			if s.Seed == "" {
				continue
			}
			if _, err := tx.ExecContext(ctx, s.Seed); err != nil {
				return fmt.Errorf("seed %s: %w", s.Module, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit schema: %w", err)
	}
	return nil
}

// CheckFile reports whether the database file at path has every declared
// column, without changing it, so a backup can be refused before it is
// restored rather than when the server next starts.
func CheckFile(ctx context.Context, path string) error {
	want, err := declaredColumns(ctx, registered())
	if err != nil {
		return err
	}
	// immutable: the file is a private copy, so a stray -wal beside it is ignored.
	sqlDB, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro&immutable=1")
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = sqlDB.Close() }()
	existed, err := tableNames(ctx, sqlDB)
	if err != nil {
		return err
	}
	return checkColumns(ctx, sqlDB, want, existed)
}

func registered() []Schema {
	registryMu.Lock()
	schemas := slices.Clone(registry)
	registryMu.Unlock()
	slices.SortStableFunc(schemas, func(a, b Schema) int { return strings.Compare(a.Module, b.Module) })
	return schemas
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// declaredColumns builds the schemas in an empty in-memory database and
// returns each table's columns.
func declaredColumns(ctx context.Context, schemas []Schema) (map[string][]string, error) {
	mem, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, fmt.Errorf("open schema check: %w", err)
	}
	defer func() { _ = mem.Close() }()
	// Every connection to :memory: opens its own empty database.
	mem.SetMaxOpenConns(1)
	for _, s := range schemas {
		if _, err := mem.ExecContext(ctx, s.SQL); err != nil {
			return nil, fmt.Errorf("apply %s schema: %w", s.Module, err)
		}
	}
	tables, err := tableNames(ctx, mem)
	if err != nil {
		return nil, err
	}
	cols := make(map[string][]string, len(tables))
	for table := range tables {
		if cols[table], err = columnNames(ctx, mem, table); err != nil {
			return nil, err
		}
	}
	return cols, nil
}

// checkColumns fails when an existing table lacks a declared column. Extra
// live columns are fine.
func checkColumns(ctx context.Context, q querier, want map[string][]string, existing map[string]bool) error {
	var drift []string
	for _, table := range slices.Sorted(maps.Keys(want)) {
		if !existing[table] {
			continue
		}
		live, err := columnNames(ctx, q, table)
		if err != nil {
			return err
		}
		var missing []string
		for _, c := range want[table] {
			if !slices.Contains(live, c) {
				missing = append(missing, c)
			}
		}
		if len(missing) > 0 {
			drift = append(drift, fmt.Sprintf("%s (%s)", table, strings.Join(missing, ", ")))
		}
	}
	if len(drift) > 0 {
		return fmt.Errorf("database lacks declared columns: %s; apply the schema change to the database by hand", strings.Join(drift, "; "))
	}
	return nil
}

func tableNames(ctx context.Context, q querier) (map[string]bool, error) {
	names, err := queryStrings(ctx, q, `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite\_%' ESCAPE '\'`)
	if err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	// SQLite matches table and column names case-insensitively.
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[strings.ToLower(n)] = true
	}
	return set, nil
}

func columnNames(ctx context.Context, q querier, table string) ([]string, error) {
	names, err := queryStrings(ctx, q, `SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, fmt.Errorf("list %s columns: %w", table, err)
	}
	for i, n := range names {
		names[i] = strings.ToLower(n)
	}
	return names, nil
}

func queryStrings(ctx context.Context, q querier, query string, args ...any) ([]string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
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
