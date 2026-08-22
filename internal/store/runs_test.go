// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func intPtr(i int) *int { return &i }

func TestCreateRun(t *testing.T) {
	ctx := context.Background()

	t.Run("round-trips every field", func(t *testing.T) {
		s := newStore(t)
		queued := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)

		want := Run{
			ID:       "run-1",
			Kind:     "platform.install",
			Target:   "ingress/traefik",
			LockKey:  "lab:default",
			Actor:    "sagar",
			Script:   "platform/ingress/traefik/install.sh",
			Argv:     []string{"--namespace", "ingress"},
			Timeout:  20 * time.Minute,
			QueuedAt: queued,
		}
		if err := s.CreateRun(ctx, want); err != nil {
			t.Fatalf("CreateRun: %v", err)
		}

		got, err := s.GetRun(ctx, "run-1")
		if err != nil {
			t.Fatalf("GetRun: %v", err)
		}
		if got.Kind != want.Kind || got.Target != want.Target || got.LockKey != want.LockKey {
			t.Errorf("identity fields: got %+v", got)
		}
		if got.Actor != want.Actor || got.Script != want.Script {
			t.Errorf("actor/script: got %q/%q", got.Actor, got.Script)
		}
		if got.Timeout != want.Timeout {
			t.Errorf("timeout: got %v, want %v", got.Timeout, want.Timeout)
		}
		if !got.QueuedAt.Equal(queued) {
			t.Errorf("queuedAt: got %v, want %v", got.QueuedAt, queued)
		}
		if strings.Join(got.Argv, " ") != "--namespace ingress" {
			t.Errorf("argv: got %v", got.Argv)
		}
		if got.Status != StatusQueued {
			t.Errorf("status: got %q, want queued", got.Status)
		}
		if !got.StartedAt.IsZero() || !got.EndedAt.IsZero() {
			t.Error("a queued run must not have start/end timestamps")
		}
		if got.ExitCode != nil {
			t.Errorf("exit code should be nil, got %d", *got.ExitCode)
		}
	})

	t.Run("nil argv round-trips as empty, not null", func(t *testing.T) {
		s := newStore(t)
		if err := s.CreateRun(ctx, Run{ID: "r", Kind: "test"}); err != nil {
			t.Fatalf("CreateRun: %v", err)
		}
		got, err := s.GetRun(ctx, "r")
		if err != nil {
			t.Fatalf("GetRun: %v", err)
		}
		if got.Argv == nil {
			t.Error("argv decoded as nil; callers should get an empty slice")
		}
	})

	t.Run("rejects invalid input", func(t *testing.T) {
		tests := []struct {
			name string
			run  Run
			want string
		}{
			{"missing ID", Run{Kind: "test"}, "ID is required"},
			{"missing kind", Run{ID: "r"}, "kind is required"},
			{"unknown status", Run{ID: "r", Kind: "test", Status: Status("weird")}, "invalid run status"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				s := newStore(t)
				err := s.CreateRun(ctx, tt.run)
				if err == nil {
					t.Fatal("expected an error")
				}
				if !strings.Contains(err.Error(), tt.want) {
					t.Errorf("error = %q, want it to mention %q", err, tt.want)
				}
			})
		}
	})

	t.Run("rejects a duplicate ID", func(t *testing.T) {
		s := newStore(t)
		r := Run{ID: "dup", Kind: "test"}
		if err := s.CreateRun(ctx, r); err != nil {
			t.Fatalf("first CreateRun: %v", err)
		}
		err := s.CreateRun(ctx, r)
		if err == nil {
			t.Fatal("expected a duplicate-ID error")
		}
		if !strings.Contains(err.Error(), "already exists") {
			t.Errorf("error = %q, want it to say the run already exists", err)
		}
	})

	t.Run("defaults QueuedAt from the clock", func(t *testing.T) {
		fixed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		s := newStore(t, WithClock(func() time.Time { return fixed }))
		if err := s.CreateRun(ctx, Run{ID: "r", Kind: "test"}); err != nil {
			t.Fatalf("CreateRun: %v", err)
		}
		got, _ := s.GetRun(ctx, "r")
		if !got.QueuedAt.Equal(fixed) {
			t.Errorf("queuedAt: got %v, want %v", got.QueuedAt, fixed)
		}
	})

	t.Run("honours a cancelled context", func(t *testing.T) {
		s := newStore(t)
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if err := s.CreateRun(cancelled, Run{ID: "r", Kind: "test"}); err == nil {
			t.Fatal("expected an error on a cancelled context")
		}
	})
}

func TestGetRun_NotFound(t *testing.T) {
	s := newStore(t)
	_, err := s.GetRun(context.Background(), "nope")
	if !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("error = %v, want ErrRunNotFound", err)
	}
}

func TestRunLifecycle(t *testing.T) {
	ctx := context.Background()

	t.Run("queued to running to succeeded", func(t *testing.T) {
		s := newStore(t)
		start := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
		end := start.Add(90 * time.Second)

		if err := s.CreateRun(ctx, Run{ID: "r", Kind: "test"}); err != nil {
			t.Fatalf("CreateRun: %v", err)
		}
		if err := s.StartRun(ctx, "r", start); err != nil {
			t.Fatalf("StartRun: %v", err)
		}

		running, _ := s.GetRun(ctx, "r")
		if running.Status != StatusRunning {
			t.Fatalf("status = %q, want running", running.Status)
		}
		if !running.StartedAt.Equal(start) {
			t.Errorf("startedAt = %v, want %v", running.StartedAt, start)
		}

		if err := s.FinishRun(ctx, "r", StatusSucceeded, intPtr(0), "", end, 90*time.Second); err != nil {
			t.Fatalf("FinishRun: %v", err)
		}

		done, _ := s.GetRun(ctx, "r")
		if done.Status != StatusSucceeded {
			t.Errorf("status = %q, want succeeded", done.Status)
		}
		if done.ExitCode == nil || *done.ExitCode != 0 {
			t.Errorf("exitCode = %v, want 0", done.ExitCode)
		}
		if done.Duration != 90*time.Second {
			t.Errorf("duration = %v, want 90s", done.Duration)
		}
	})

	t.Run("records each terminal status with its exit code", func(t *testing.T) {
		tests := []struct {
			name     string
			status   Status
			exitCode *int
			errText  string
		}{
			{"failed with an exit code", StatusFailed, intPtr(1), "exit status 1"},
			{"cancelled before exec, no exit code", StatusCancelled, nil, "cancelled by user"},
			{"timed out", StatusTimedOut, nil, "timed out after 20m0s"},
			{"failed with a signal exit code", StatusFailed, intPtr(143), "terminated"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				s := newStore(t)
				if err := s.CreateRun(ctx, Run{ID: "r", Kind: "test"}); err != nil {
					t.Fatalf("CreateRun: %v", err)
				}
				if err := s.StartRun(ctx, "r", time.Now()); err != nil {
					t.Fatalf("StartRun: %v", err)
				}
				if err := s.FinishRun(ctx, "r", tt.status, tt.exitCode, tt.errText, time.Now(), time.Second); err != nil {
					t.Fatalf("FinishRun: %v", err)
				}

				got, _ := s.GetRun(ctx, "r")
				if got.Status != tt.status {
					t.Errorf("status = %q, want %q", got.Status, tt.status)
				}
				if got.Error != tt.errText {
					t.Errorf("error = %q, want %q", got.Error, tt.errText)
				}
				if tt.exitCode == nil && got.ExitCode != nil {
					t.Errorf("exitCode = %d, want nil", *got.ExitCode)
				}
				if tt.exitCode != nil && (got.ExitCode == nil || *got.ExitCode != *tt.exitCode) {
					t.Errorf("exitCode = %v, want %d", got.ExitCode, *tt.exitCode)
				}
			})
		}
	})

	t.Run("a queued run can be finished without ever starting", func(t *testing.T) {
		// Cancelling while still in the queue is a real path: the user hits
		// cancel before a worker picks the run up.
		s := newStore(t)
		if err := s.CreateRun(ctx, Run{ID: "r", Kind: "test"}); err != nil {
			t.Fatalf("CreateRun: %v", err)
		}
		if err := s.FinishRun(ctx, "r", StatusCancelled, nil, "cancelled while queued", time.Now(), 0); err != nil {
			t.Fatalf("FinishRun: %v", err)
		}
		got, _ := s.GetRun(ctx, "r")
		if got.Status != StatusCancelled {
			t.Errorf("status = %q, want cancelled", got.Status)
		}
		if !got.StartedAt.IsZero() {
			t.Error("a run cancelled from the queue must have no start time")
		}
	})

	t.Run("StartRun refuses a run that is not queued", func(t *testing.T) {
		s := newStore(t)
		if err := s.CreateRun(ctx, Run{ID: "r", Kind: "test"}); err != nil {
			t.Fatalf("CreateRun: %v", err)
		}
		if err := s.StartRun(ctx, "r", time.Now()); err != nil {
			t.Fatalf("first StartRun: %v", err)
		}
		// The guard that makes a double-start from two workers impossible.
		if err := s.StartRun(ctx, "r", time.Now()); err == nil {
			t.Fatal("expected the second StartRun to fail")
		}
	})

	t.Run("StartRun on a missing run fails", func(t *testing.T) {
		s := newStore(t)
		err := s.StartRun(ctx, "ghost", time.Now())
		if !errors.Is(err, ErrRunNotFound) {
			t.Fatalf("error = %v, want ErrRunNotFound", err)
		}
	})

	t.Run("FinishRun refuses a non-terminal status", func(t *testing.T) {
		s := newStore(t)
		if err := s.CreateRun(ctx, Run{ID: "r", Kind: "test"}); err != nil {
			t.Fatalf("CreateRun: %v", err)
		}
		err := s.FinishRun(ctx, "r", StatusRunning, nil, "", time.Now(), 0)
		if err == nil || !strings.Contains(err.Error(), "not a terminal status") {
			t.Fatalf("error = %v, want a not-terminal complaint", err)
		}
	})

	t.Run("the first terminal outcome wins", func(t *testing.T) {
		// A cancellation racing with a natural exit must not overwrite the
		// outcome that already landed.
		s := newStore(t)
		if err := s.CreateRun(ctx, Run{ID: "r", Kind: "test"}); err != nil {
			t.Fatalf("CreateRun: %v", err)
		}
		if err := s.StartRun(ctx, "r", time.Now()); err != nil {
			t.Fatalf("StartRun: %v", err)
		}
		if err := s.FinishRun(ctx, "r", StatusSucceeded, intPtr(0), "", time.Now(), time.Second); err != nil {
			t.Fatalf("FinishRun: %v", err)
		}

		err := s.FinishRun(ctx, "r", StatusCancelled, nil, "too late", time.Now(), time.Second)
		if err == nil {
			t.Fatal("expected the second FinishRun to be refused")
		}
		got, _ := s.GetRun(ctx, "r")
		if got.Status != StatusSucceeded {
			t.Errorf("status = %q, want the original succeeded", got.Status)
		}
	})
}

func TestActiveRunForLock(t *testing.T) {
	ctx := context.Background()

	t.Run("finds a queued or running holder", func(t *testing.T) {
		for _, status := range []Status{StatusQueued, StatusRunning} {
			t.Run(string(status), func(t *testing.T) {
				s := newStore(t)
				if err := s.CreateRun(ctx, Run{ID: "holder", Kind: "test", LockKey: "lab:default"}); err != nil {
					t.Fatalf("CreateRun: %v", err)
				}
				if status == StatusRunning {
					if err := s.StartRun(ctx, "holder", time.Now()); err != nil {
						t.Fatalf("StartRun: %v", err)
					}
				}

				got, found, err := s.ActiveRunForLock(ctx, "lab:default")
				if err != nil {
					t.Fatalf("ActiveRunForLock: %v", err)
				}
				if !found {
					t.Fatal("expected to find the lock holder")
				}
				if got.ID != "holder" {
					t.Errorf("holder = %q, want holder", got.ID)
				}
			})
		}
	})

	t.Run("a terminal run releases the lock", func(t *testing.T) {
		s := newStore(t)
		if err := s.CreateRun(ctx, Run{ID: "old", Kind: "test", LockKey: "lab:default"}); err != nil {
			t.Fatalf("CreateRun: %v", err)
		}
		if err := s.FinishRun(ctx, "old", StatusSucceeded, intPtr(0), "", time.Now(), time.Second); err != nil {
			t.Fatalf("FinishRun: %v", err)
		}
		_, found, err := s.ActiveRunForLock(ctx, "lab:default")
		if err != nil {
			t.Fatalf("ActiveRunForLock: %v", err)
		}
		if found {
			t.Error("a finished run must not still hold its lock")
		}
	})

	t.Run("returns the oldest holder", func(t *testing.T) {
		s := newStore(t)
		base := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
		if err := s.CreateRun(ctx, Run{ID: "second", Kind: "test", LockKey: "k", QueuedAt: base.Add(time.Minute)}); err != nil {
			t.Fatalf("CreateRun: %v", err)
		}
		if err := s.CreateRun(ctx, Run{ID: "first", Kind: "test", LockKey: "k", QueuedAt: base}); err != nil {
			t.Fatalf("CreateRun: %v", err)
		}
		got, found, _ := s.ActiveRunForLock(ctx, "k")
		if !found || got.ID != "first" {
			t.Errorf("holder = %q (found=%v), want first", got.ID, found)
		}
	})

	t.Run("distinct keys do not conflict", func(t *testing.T) {
		s := newStore(t)
		if err := s.CreateRun(ctx, Run{ID: "a", Kind: "test", LockKey: "lab:one"}); err != nil {
			t.Fatalf("CreateRun: %v", err)
		}
		_, found, err := s.ActiveRunForLock(ctx, "lab:two")
		if err != nil {
			t.Fatalf("ActiveRunForLock: %v", err)
		}
		if found {
			t.Error("a different lock key must not conflict")
		}
	})

	t.Run("an empty key never conflicts", func(t *testing.T) {
		s := newStore(t)
		if err := s.CreateRun(ctx, Run{ID: "a", Kind: "test"}); err != nil {
			t.Fatalf("CreateRun: %v", err)
		}
		_, found, err := s.ActiveRunForLock(ctx, "")
		if err != nil {
			t.Fatalf("ActiveRunForLock: %v", err)
		}
		if found {
			t.Error("an unlocked run must never be reported as a lock holder")
		}
	})
}

func TestListRuns(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)

	seed := func(t *testing.T) *Store {
		t.Helper()
		s := newStore(t)
		for i := range 5 {
			kind := "platform.install"
			if i%2 == 1 {
				kind = "scenario.activate"
			}
			id := fmt.Sprintf("run-%d", i)
			if err := s.CreateRun(ctx, Run{
				ID: id, Kind: kind, QueuedAt: base.Add(time.Duration(i) * time.Minute),
			}); err != nil {
				t.Fatalf("CreateRun: %v", err)
			}
		}
		// run-0 succeeded, run-1 failed, the rest stay queued.
		if err := s.FinishRun(ctx, "run-0", StatusSucceeded, intPtr(0), "", base, time.Second); err != nil {
			t.Fatalf("FinishRun: %v", err)
		}
		if err := s.FinishRun(ctx, "run-1", StatusFailed, intPtr(1), "boom", base, time.Second); err != nil {
			t.Fatalf("FinishRun: %v", err)
		}
		return s
	}

	t.Run("returns newest first", func(t *testing.T) {
		s := seed(t)
		runs, err := s.ListRuns(ctx, RunFilter{})
		if err != nil {
			t.Fatalf("ListRuns: %v", err)
		}
		if len(runs) != 5 {
			t.Fatalf("got %d runs, want 5", len(runs))
		}
		if runs[0].ID != "run-4" || runs[4].ID != "run-0" {
			t.Errorf("order wrong: first=%s last=%s", runs[0].ID, runs[4].ID)
		}
	})

	t.Run("filters", func(t *testing.T) {
		tests := []struct {
			name   string
			filter RunFilter
			want   []string
		}{
			{"by kind", RunFilter{Kind: "scenario.activate"}, []string{"run-3", "run-1"}},
			{"by single status", RunFilter{Status: []Status{StatusFailed}}, []string{"run-1"}},
			{"by several statuses", RunFilter{Status: []Status{StatusSucceeded, StatusFailed}}, []string{"run-1", "run-0"}},
			{"by cursor", RunFilter{Before: base.Add(2 * time.Minute)}, []string{"run-1", "run-0"}},
			{"by limit", RunFilter{Limit: 2}, []string{"run-4", "run-3"}},
			{"kind and status together", RunFilter{Kind: "platform.install", Status: []Status{StatusQueued}}, []string{"run-4", "run-2"}},
			{"no match", RunFilter{Kind: "nonexistent"}, nil},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				s := seed(t)
				runs, err := s.ListRuns(ctx, tt.filter)
				if err != nil {
					t.Fatalf("ListRuns: %v", err)
				}
				var ids []string
				for _, r := range runs {
					ids = append(ids, r.ID)
				}
				if strings.Join(ids, ",") != strings.Join(tt.want, ",") {
					t.Errorf("got %v, want %v", ids, tt.want)
				}
			})
		}
	})

	t.Run("an empty store returns no rows and no error", func(t *testing.T) {
		s := newStore(t)
		runs, err := s.ListRuns(ctx, RunFilter{})
		if err != nil {
			t.Fatalf("ListRuns: %v", err)
		}
		if len(runs) != 0 {
			t.Errorf("got %d runs, want 0", len(runs))
		}
	})
}

func TestRecoverOrphanedRuns(t *testing.T) {
	ctx := context.Background()

	t.Run("cancels in-flight runs and leaves terminal ones alone", func(t *testing.T) {
		s := newStore(t)
		if err := s.CreateRun(ctx, Run{ID: "queued", Kind: "test", LockKey: "lab:a"}); err != nil {
			t.Fatalf("CreateRun: %v", err)
		}
		if err := s.CreateRun(ctx, Run{ID: "running", Kind: "test", LockKey: "lab:b"}); err != nil {
			t.Fatalf("CreateRun: %v", err)
		}
		if err := s.StartRun(ctx, "running", time.Now()); err != nil {
			t.Fatalf("StartRun: %v", err)
		}
		if err := s.CreateRun(ctx, Run{ID: "done", Kind: "test"}); err != nil {
			t.Fatalf("CreateRun: %v", err)
		}
		if err := s.FinishRun(ctx, "done", StatusSucceeded, intPtr(0), "", time.Now(), time.Second); err != nil {
			t.Fatalf("FinishRun: %v", err)
		}

		ids, err := s.RecoverOrphanedRuns(ctx, time.Now())
		if err != nil {
			t.Fatalf("RecoverOrphanedRuns: %v", err)
		}
		if len(ids) != 2 {
			t.Errorf("reaped %v, want 2 runs", ids)
		}

		for _, id := range []string{"queued", "running"} {
			got, _ := s.GetRun(ctx, id)
			if got.Status != StatusCancelled {
				t.Errorf("%s: status = %q, want cancelled", id, got.Status)
			}
			// The user must be able to tell an interruption from a real cancel.
			if !strings.Contains(got.Error, "interrupted") {
				t.Errorf("%s: error = %q, want it to explain the interruption", id, got.Error)
			}
		}

		done, _ := s.GetRun(ctx, "done")
		if done.Status != StatusSucceeded {
			t.Errorf("a finished run was rewritten: %q", done.Status)
		}
	})

	t.Run("releases the locks the dead runs held", func(t *testing.T) {
		s := newStore(t)
		if err := s.CreateRun(ctx, Run{ID: "stuck", Kind: "test", LockKey: "lab:default"}); err != nil {
			t.Fatalf("CreateRun: %v", err)
		}
		if _, err := s.RecoverOrphanedRuns(ctx, time.Now()); err != nil {
			t.Fatalf("RecoverOrphanedRuns: %v", err)
		}
		_, found, err := s.ActiveRunForLock(ctx, "lab:default")
		if err != nil {
			t.Fatalf("ActiveRunForLock: %v", err)
		}
		if found {
			t.Error("a recovered run must release its lock, or it blocks forever")
		}
	})

	t.Run("is a no-op on a clean database", func(t *testing.T) {
		s := newStore(t)
		ids, err := s.RecoverOrphanedRuns(ctx, time.Now())
		if err != nil {
			t.Fatalf("RecoverOrphanedRuns: %v", err)
		}
		if len(ids) != 0 {
			t.Errorf("reaped %v, want nothing", ids)
		}
	})
}

func TestPruneRuns(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)

	t.Run("deletes old terminal runs and cascades their logs", func(t *testing.T) {
		s := newStore(t)
		if err := s.CreateRun(ctx, Run{ID: "old", Kind: "test", QueuedAt: base}); err != nil {
			t.Fatalf("CreateRun: %v", err)
		}
		if err := s.FinishRun(ctx, "old", StatusSucceeded, intPtr(0), "", base, time.Second); err != nil {
			t.Fatalf("FinishRun: %v", err)
		}
		if _, err := s.AppendLogs(ctx, "old", []LogLine{{Text: "hello"}}); err != nil {
			t.Fatalf("AppendLogs: %v", err)
		}

		n, err := s.PruneRuns(ctx, base.Add(time.Hour))
		if err != nil {
			t.Fatalf("PruneRuns: %v", err)
		}
		if n != 1 {
			t.Errorf("pruned %d, want 1", n)
		}
		if _, err := s.GetRun(ctx, "old"); !errors.Is(err, ErrRunNotFound) {
			t.Errorf("run survived pruning: %v", err)
		}
		lines, err := s.ReadLogs(ctx, "old", 0, 10)
		if err != nil {
			t.Fatalf("ReadLogs: %v", err)
		}
		if len(lines) != 0 {
			t.Errorf("logs survived the cascade: %d lines", len(lines))
		}
	})

	t.Run("never prunes an in-flight run", func(t *testing.T) {
		s := newStore(t)
		if err := s.CreateRun(ctx, Run{ID: "live", Kind: "test", QueuedAt: base}); err != nil {
			t.Fatalf("CreateRun: %v", err)
		}
		n, err := s.PruneRuns(ctx, base.Add(24*time.Hour))
		if err != nil {
			t.Fatalf("PruneRuns: %v", err)
		}
		if n != 0 {
			t.Errorf("pruned %d in-flight runs, want 0", n)
		}
	})

	t.Run("keeps runs newer than the cutoff", func(t *testing.T) {
		s := newStore(t)
		if err := s.CreateRun(ctx, Run{ID: "recent", Kind: "test", QueuedAt: base}); err != nil {
			t.Fatalf("CreateRun: %v", err)
		}
		if err := s.FinishRun(ctx, "recent", StatusSucceeded, intPtr(0), "", base.Add(time.Hour), time.Second); err != nil {
			t.Fatalf("FinishRun: %v", err)
		}
		n, err := s.PruneRuns(ctx, base)
		if err != nil {
			t.Fatalf("PruneRuns: %v", err)
		}
		if n != 0 {
			t.Errorf("pruned %d, want 0", n)
		}
	})
}

// TestConcurrentWrites is the guard against v1's unsynchronised JSON files.
// Many goroutines writing at once must all succeed, with no lost updates and
// no SQLITE_BUSY surfacing to the caller.
func TestConcurrentWrites(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	const writers = 16
	var wg sync.WaitGroup
	errCh := make(chan error, writers)

	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := fmt.Sprintf("run-%02d", i)
			if err := s.CreateRun(ctx, Run{ID: id, Kind: "test"}); err != nil {
				errCh <- err
				return
			}
			if err := s.StartRun(ctx, id, time.Now()); err != nil {
				errCh <- err
				return
			}
			if _, err := s.AppendLogs(ctx, id, []LogLine{{Text: "line"}}); err != nil {
				errCh <- err
				return
			}
			if err := s.FinishRun(ctx, id, StatusSucceeded, intPtr(0), "", time.Now(), time.Second); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent write failed: %v", err)
	}

	runs, err := s.ListRuns(ctx, RunFilter{Limit: 100})
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != writers {
		t.Errorf("got %d runs, want %d", len(runs), writers)
	}
	for _, r := range runs {
		if r.Status != StatusSucceeded {
			t.Errorf("run %s: status = %q, want succeeded", r.ID, r.Status)
		}
	}
}
