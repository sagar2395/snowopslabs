// SPDX-License-Identifier: Apache-2.0

// Package store is SnowOps Labs's durable state: runs, their logs and steps, and
// the audit trail.
//
// v1 kept run state in an in-memory map capped at 100 entries and results in
// loose JSON files, so history vanished on restart and concurrent writes raced.
// v2 uses SQLite through modernc.org/sqlite — a pure-Go driver, because cgo
// would break cross-compilation and the cross-platform golden rule. See
// docs/adr/0002-sqlite-persistence.md.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver, registered as "sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// ErrSchemaTooNew is returned when the database was written by a newer build.
// Reading it on a best-effort basis risks silent corruption, so opening fails
// loudly and tells the user their options.
var ErrSchemaTooNew = errors.New("database schema is newer than this build understands")

// DefaultPath returns the database location: $SNOWOPS_HOME/snowops.db,
// defaulting to ~/.snowops/snowops.db.
func DefaultPath() (string, error) {
	if home := os.Getenv("SNOWOPS_HOME"); home != "" {
		return filepath.Join(home, "snowops.db"), nil
	}
	dir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating home directory (set SNOWOPS_HOME to override): %w", err)
	}
	return filepath.Join(dir, ".snowops", "snowops.db"), nil
}

// Store is an open database with all migrations applied.
type Store struct {
	db   *sql.DB
	path string
	now  func() time.Time // injectable for tests
}

// Option customises Open.
type Option func(*Store)

// WithClock replaces the time source. Tests use it to make timestamps
// deterministic; production never sets it.
func WithClock(now func() time.Time) Option {
	return func(s *Store) { s.now = now }
}

// Open opens (creating if needed) the database at path and applies every
// pending migration. The parent directory is created if missing.
//
// Concurrency settings matter here: WAL lets readers proceed during a write,
// and a busy timeout turns momentary contention into a short wait rather than
// an immediate SQLITE_BUSY error surfacing as a failed user command.
func Open(ctx context.Context, path string, opts ...Option) (*Store, error) {
	if path == "" {
		return nil, errors.New("store: database path is required")
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("creating %s: %w", dir, err)
		}
	}

	dsn := path + "?_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(ON)" +
		"&_pragma=synchronous(NORMAL)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}

	// SQLite serialises writes. A single connection avoids lock churn and makes
	// the write path deterministic; readers are fast enough at this scale.
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connecting to %s: %w", path, err)
	}

	s := &Store{db: db, path: path, now: time.Now}
	for _, opt := range opts {
		opt(s)
	}

	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the database handle.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Path returns the database file location.
func (s *Store) Path() string { return s.path }

// DB exposes the handle for repositories in this package. It is deliberately
// not part of any wider contract: callers outside store use the repositories.
func (s *Store) DB() *sql.DB { return s.db }

// SchemaVersion returns the highest applied migration version (0 when empty).
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	return currentVersion(ctx, s.db)
}

// migration is one numbered, forward-only schema change.
type migration struct {
	version int
	name    string
	sql     string
}

// loadMigrations reads and orders the embedded migrations. A file whose name
// does not start with a number is a packaging mistake and fails loudly rather
// than being silently skipped.
func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("reading embedded migrations: %w", err)
	}

	out := make([]migration, 0, len(entries))
	seen := make(map[int]string, len(entries))

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		prefix, _, ok := strings.Cut(e.Name(), "_")
		if !ok {
			return nil, fmt.Errorf("migration %q: name must be <version>_<description>.sql", e.Name())
		}
		version, err := strconv.Atoi(prefix)
		if err != nil {
			return nil, fmt.Errorf("migration %q: %q is not a version number", e.Name(), prefix)
		}
		if version <= 0 {
			return nil, fmt.Errorf("migration %q: version must be positive", e.Name())
		}
		if dup, exists := seen[version]; exists {
			return nil, fmt.Errorf("migrations %q and %q share version %d", dup, e.Name(), version)
		}
		body, err := migrationFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("reading migration %q: %w", e.Name(), err)
		}
		seen[version] = e.Name()
		out = append(out, migration{version: version, name: e.Name(), sql: string(body)})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

func currentVersion(ctx context.Context, db *sql.DB) (int, error) {
	var version sql.NullInt64
	err := db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version)
	switch {
	case err != nil && strings.Contains(err.Error(), "no such table"):
		return 0, nil
	case err != nil:
		return 0, fmt.Errorf("reading schema version: %w", err)
	case !version.Valid:
		return 0, nil
	}
	return int(version.Int64), nil
}

// migrate applies every migration newer than the recorded version, each in its
// own transaction so a failure leaves the database at a known version rather
// than half-migrated.
func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			name       TEXT    NOT NULL,
			applied_at INTEGER NOT NULL
		)`); err != nil {
		return fmt.Errorf("creating schema_migrations: %w", err)
	}

	migrations, err := loadMigrations()
	if err != nil {
		return err
	}

	applied, err := currentVersion(ctx, s.db)
	if err != nil {
		return err
	}

	latest := 0
	if len(migrations) > 0 {
		latest = migrations[len(migrations)-1].version
	}

	// Refuse a database from the future. Best-effort reads against an unknown
	// schema are how data gets silently corrupted.
	if applied > latest {
		return fmt.Errorf("%w: %s is at schema %d, this build knows %d — "+
			"upgrade labctl, or point SNOWOPS_HOME at a different directory",
			ErrSchemaTooNew, s.path, applied, latest)
	}

	for _, m := range migrations {
		if m.version <= applied {
			continue
		}
		if err := s.applyMigration(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) applyMigration(ctx context.Context, m migration) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migration %s: begin: %w", m.name, err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once committed

	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return fmt.Errorf("migration %s: %w", m.name, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
		m.version, m.name, s.now().UnixMicro()); err != nil {
		return fmt.Errorf("migration %s: recording version: %w", m.name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migration %s: commit: %w", m.name, err)
	}
	return nil
}

// tx runs fn inside a transaction, rolling back on error or panic. Every
// multi-statement write in this package goes through it, so a partially
// written run record cannot exist.
func (s *Store) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	t, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = t.Rollback()
			panic(p)
		}
	}()
	if err := fn(t); err != nil {
		_ = t.Rollback()
		return err
	}
	return t.Commit()
}

// micro converts a time to the storage representation. The zero time stores as
// NULL so "never started" is distinguishable from "started at the epoch".
func micro(t time.Time) sql.NullInt64 {
	if t.IsZero() {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: t.UnixMicro(), Valid: true}
}

// fromMicro is the inverse of micro.
func fromMicro(v sql.NullInt64) time.Time {
	if !v.Valid {
		return time.Time{}
	}
	return time.UnixMicro(v.Int64).UTC()
}
