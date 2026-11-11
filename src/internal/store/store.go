// SPDX-License-Identifier: Apache-2.0

// Package store persists labctl's durable state in SQLite: runs, their logs
// and steps, the installed-component inventory, and the audit trail.
//
// It uses modernc.org/sqlite, a pure-Go driver, because the project builds
// without cgo (ADR-0002).
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
	"sync"
	"time"

	"github.com/snowops-oss/snowopslabs/internal/config"
	"github.com/snowops-oss/snowopslabs/internal/runowner"
	_ "modernc.org/sqlite" // pure-Go driver, registered as "sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// ErrSchemaTooNew is returned when the database was migrated by a newer
// labctl. Open refuses it rather than risk misreading an unknown schema.
var ErrSchemaTooNew = errors.New("database schema is newer than this build understands")

// DefaultPath returns the database location: <config.Home>/snowops.db.
func DefaultPath() (string, error) {
	home, err := config.Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "snowops.db"), nil
}

// Store is an open database with all migrations applied.
type Store struct {
	db             *sql.DB
	path           string
	now            func() time.Time // injectable for tests
	upgrade        *runowner.Lock
	lifetimeMu     sync.Mutex
	retained       int
	closeRequested bool
	closed         bool
	closeErr       error
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
// The database runs in WAL mode so reads proceed during a write, with a busy
// timeout so brief contention waits instead of failing with SQLITE_BUSY.
func Open(ctx context.Context, path string, opts ...Option) (*Store, error) {
	if path == "" {
		return nil, errors.New("store: database path is required")
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("creating %s: %w", dir, err)
		}
	}
	upgrade, err := runowner.Upgrade(ctx, path, false)
	if err != nil {
		return nil, fmt.Errorf("opening store upgrade lock: %w", err)
	}

	dsn := path + "?_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(ON)" +
		"&_pragma=synchronous(NORMAL)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("opening %s: %w", path, err), upgrade.Close())
	}

	// SQLite serialises writes anyway; one connection avoids lock contention
	// between our own connections.
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)

	if err := db.PingContext(ctx); err != nil {
		return nil, errors.Join(fmt.Errorf("connecting to %s: %w", path, err), db.Close(), upgrade.Close())
	}

	s := &Store{db: db, path: path, now: time.Now, upgrade: upgrade}
	for _, opt := range opts {
		opt(s)
	}

	if err := s.migrate(ctx); err != nil {
		return nil, errors.Join(err, s.Close())
	}
	return s, nil
}

// Close releases the database after retained workers return and save their results.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	s.lifetimeMu.Lock()
	defer s.lifetimeMu.Unlock()
	s.closeRequested = true
	if s.retained == 0 {
		s.closeResources()
	}
	return s.closeErr
}

// Retain keeps the database and upgrade lock open until its release is called.
// A closing store refuses new borrowers while existing workers finish.
func (s *Store) Retain() (func() error, error) {
	s.lifetimeMu.Lock()
	defer s.lifetimeMu.Unlock()
	if s.closeRequested {
		return nil, errors.New("store is closing; wait for its workers to finish")
	}
	s.retained++
	var once sync.Once
	var releaseErr error
	return func() error {
		once.Do(func() {
			s.lifetimeMu.Lock()
			defer s.lifetimeMu.Unlock()
			s.retained--
			if s.closeRequested && s.retained == 0 {
				s.closeResources()
			}
			releaseErr = s.closeErr
		})
		return releaseErr
	}, nil
}

func (s *Store) closeResources() {
	if !s.closed {
		s.closeErr = errors.Join(s.db.Close(), s.upgrade.Close())
		s.closed = true
	}
}

// Path returns the database file location.
func (s *Store) Path() string { return s.path }

// DB exposes the raw handle. Code outside this package should use the Store
// methods; tests use DB to set up states the methods cannot reach.
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

// loadMigrations reads the embedded migrations and sorts them by version. A
// file whose name does not start with a number is an error, not skipped.
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
// own transaction, so a failure leaves the database at a known version.
func (s *Store) migrate(ctx context.Context) error {
	migrations, err := loadMigrations()
	if err != nil {
		return err
	}
	return s.migrateSchema(ctx, migrations)
}

func (s *Store) migrateSchema(ctx context.Context, migrations []migration) error {
	applied, err := currentVersion(ctx, s.db)
	if err != nil {
		return err
	}

	latest := 0
	if len(migrations) > 0 {
		latest = migrations[len(migrations)-1].version
	}

	// The database was migrated by a newer labctl; see ErrSchemaTooNew.
	if applied > latest {
		return fmt.Errorf("%w: %s is at schema %d, this build knows %d - "+
			"upgrade labctl, or point SNOWOPS_HOME at a different directory",
			ErrSchemaTooNew, s.path, applied, latest)
	}
	if applied == latest {
		return nil
	}
	if err := s.upgrade.Close(); err != nil {
		return fmt.Errorf("releasing store lock for migration: %w", err)
	}
	exclusive, err := runowner.Upgrade(ctx, s.path, true)
	if err != nil {
		return fmt.Errorf("migration requires an offline store; stop all labctl processes and retry: %w", err)
	}
	s.upgrade = exclusive
	owners, err := s.lockMigrationOwners(ctx)
	if err != nil {
		return err
	}
	defer func() {
		for _, lock := range owners {
			_ = lock.Close()
		}
	}()
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at INTEGER NOT NULL
		)`); err != nil {
		return fmt.Errorf("creating schema_migrations: %w", err)
	}
	applied, err = currentVersion(ctx, s.db)
	if err != nil {
		return err
	}
	if applied > latest {
		return fmt.Errorf("%w: upgrade labctl before reopening this store", ErrSchemaTooNew)
	}

	for _, m := range migrations {
		if m.version <= applied {
			continue
		}
		if err := s.applyMigration(ctx, m); err != nil {
			return err
		}
	}
	return s.upgrade.Shared(ctx)
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

// tx runs fn inside a transaction, rolling back on error or panic. Use it for
// every write that spans more than one statement.
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

// micro converts a time to Unix microseconds for storage. The zero time is
// stored as NULL, so "never started" differs from "started at the epoch".
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
