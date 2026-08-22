// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newStore opens a store in a temp directory. Every test is hermetic: its own
// file, its own directory, removed by the test framework.
func newStore(t *testing.T, opts ...Option) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "test.db"), opts...)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return s
}

func TestOpen(t *testing.T) {
	ctx := context.Background()

	t.Run("creates the file and applies every migration", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "nested", "dir", "test.db")
		s, err := Open(ctx, path)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer s.Close()

		if _, err := os.Stat(path); err != nil {
			t.Errorf("database file not created: %v", err)
		}
		version, err := s.SchemaVersion(ctx)
		if err != nil {
			t.Fatalf("SchemaVersion: %v", err)
		}
		if version < 1 {
			t.Errorf("schema version = %d, want >= 1", version)
		}
	})

	t.Run("is idempotent across reopens", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "test.db")

		first, err := Open(ctx, path)
		if err != nil {
			t.Fatalf("first Open: %v", err)
		}
		v1, _ := first.SchemaVersion(ctx)
		if err := first.CreateRun(ctx, Run{ID: "r1", Kind: "test"}); err != nil {
			t.Fatalf("CreateRun: %v", err)
		}
		first.Close()

		second, err := Open(ctx, path)
		if err != nil {
			t.Fatalf("reopen: %v", err)
		}
		defer second.Close()

		v2, _ := second.SchemaVersion(ctx)
		if v1 != v2 {
			t.Errorf("schema version changed on reopen: %d -> %d", v1, v2)
		}
		// Re-running migrations must not have wiped anything.
		if _, err := second.GetRun(ctx, "r1"); err != nil {
			t.Errorf("data lost across reopen: %v", err)
		}
	})

	t.Run("rejects an empty path", func(t *testing.T) {
		if _, err := Open(ctx, ""); err == nil {
			t.Fatal("expected an error for an empty path")
		}
	})

	t.Run("refuses a schema newer than this build", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "test.db")
		s, err := Open(ctx, path)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		// Pretend a future build migrated this database.
		if _, err := s.DB().ExecContext(ctx,
			`INSERT INTO schema_migrations (version, name, applied_at) VALUES (9999, 'future', 0)`); err != nil {
			t.Fatalf("seeding future migration: %v", err)
		}
		s.Close()

		_, err = Open(ctx, path)
		if !errors.Is(err, ErrSchemaTooNew) {
			t.Fatalf("error = %v, want ErrSchemaTooNew", err)
		}
		// The message must tell the user what to do, not just what went wrong.
		if !strings.Contains(err.Error(), "upgrade labctl") {
			t.Errorf("error should suggest a fix, got: %v", err)
		}
	})

	t.Run("honours a cancelled context", func(t *testing.T) {
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := Open(cancelled, filepath.Join(t.TempDir(), "test.db")); err == nil {
			t.Fatal("expected an error when the context is already cancelled")
		}
	})
}

func TestClose(t *testing.T) {
	t.Run("a nil store closes without panicking", func(t *testing.T) {
		var s *Store
		if err := s.Close(); err != nil {
			t.Errorf("Close on nil: %v", err)
		}
	})

	t.Run("double close is safe", func(t *testing.T) {
		s, err := Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("first Close: %v", err)
		}
		if err := s.Close(); err != nil {
			t.Errorf("second Close: %v", err)
		}
	})
}

func TestLoadMigrations(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	if len(migrations) == 0 {
		t.Fatal("no migrations embedded — the //go:embed directive is broken")
	}
	for i := 1; i < len(migrations); i++ {
		if migrations[i-1].version >= migrations[i].version {
			t.Errorf("migrations out of order: %d before %d",
				migrations[i-1].version, migrations[i].version)
		}
	}
	for _, m := range migrations {
		if strings.TrimSpace(m.sql) == "" {
			t.Errorf("migration %s is empty", m.name)
		}
	}
}

func TestDefaultPath(t *testing.T) {
	t.Run("honours SNOWOPS_HOME", func(t *testing.T) {
		t.Setenv("SNOWOPS_HOME", "/custom/home")
		got, err := DefaultPath()
		if err != nil {
			t.Fatalf("DefaultPath: %v", err)
		}
		if want := filepath.Join("/custom/home", "snowops.db"); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("falls back to the home directory", func(t *testing.T) {
		t.Setenv("SNOWOPS_HOME", "")
		got, err := DefaultPath()
		if err != nil {
			t.Skipf("no home directory in this environment: %v", err)
		}
		if !strings.HasSuffix(got, filepath.Join("", "snowops.db")) {
			t.Errorf("got %q, want a path ending in /snowops.db", got)
		}
	})
}

func TestMicroRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		in   time.Time
	}{
		{"zero time stays zero", time.Time{}},
		{"epoch", time.UnixMicro(0).UTC()},
		{"a normal instant", time.Date(2026, 7, 26, 12, 34, 56, 789000000, time.UTC)},
		{"before the epoch", time.Date(1969, 1, 1, 0, 0, 0, 0, time.UTC)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fromMicro(micro(tt.in))
			if tt.in.IsZero() {
				if !got.IsZero() {
					t.Errorf("zero time round-tripped to %v", got)
				}
				return
			}
			if !got.Equal(tt.in) {
				t.Errorf("round trip: got %v, want %v", got, tt.in)
			}
		})
	}
}

func TestStatusTerminal(t *testing.T) {
	tests := []struct {
		status   Status
		terminal bool
		valid    bool
	}{
		{StatusQueued, false, true},
		{StatusRunning, false, true},
		{StatusSucceeded, true, true},
		{StatusFailed, true, true},
		{StatusCancelled, true, true},
		{StatusTimedOut, true, true},
		{Status("bogus"), false, false},
		{Status(""), false, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.Terminal(); got != tt.terminal {
				t.Errorf("Terminal() = %v, want %v", got, tt.terminal)
			}
			if got := tt.status.Valid(); got != tt.valid {
				t.Errorf("Valid() = %v, want %v", got, tt.valid)
			}
		})
	}
}
