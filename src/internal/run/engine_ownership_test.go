// SPDX-License-Identifier: Apache-2.0

package run

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/snowops-oss/snowopslabs/internal/runowner"
	"github.com/snowops-oss/snowopslabs/internal/store"
	"github.com/snowops-oss/snowopslabs/internal/toolchain"
)

func TestStartPreservesAnotherEngine(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  string
	}{
		{name: "locked operation", key: "lab:default"},
		{name: "unlocked operation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			path := filepath.Join(t.TempDir(), "runs.db")
			resolver, err := toolchain.NewResolver(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			newEngine := func() (*Engine, *store.Store) {
				t.Helper()
				st, err := store.Open(ctx, path)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = st.Close() })
				eng, err := New(st, toolchain.NewFake(), resolver, WithWorkers(1))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
					defer cancel()
					if err := eng.Shutdown(shutdown); err != nil {
						t.Error(err)
					}
				})
				return eng, st
			}
			first, st := newEngine()
			if err := first.Start(ctx); err != nil {
				t.Fatal(err)
			}
			started := make(chan struct{})
			id, err := first.Submit(ctx, Spec{
				Kind: "test.live", LockKey: tc.key,
				Func: func(ctx context.Context, _ io.Writer) error {
					close(started)
					<-ctx.Done()
					return ctx.Err()
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("first engine did not start its operation")
			}
			if _, err := st.AppendLogs(ctx, id, []store.LogLine{{Text: "owner is still executing"}}); err != nil {
				t.Fatal(err)
			}

			second, other := newEngine()
			if err := second.Start(ctx); err != nil {
				t.Fatal(err)
			}
			rec, err := other.GetRun(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if rec.Status != store.StatusRunning {
				t.Errorf("second engine changed live run to %q; want running", rec.Status)
			}
			lines, err := other.ReadLogs(ctx, id, 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			for _, line := range lines {
				if line.Text == "run interrupted: labctl exited while this run was in flight" {
					t.Error("live run was given an interruption record")
				}
			}
			if tc.key != "" {
				_, err := second.Submit(ctx, Spec{Kind: "test.conflict", LockKey: tc.key,
					Func: func(context.Context, io.Writer) error { return nil }})
				var conflict *LockConflictError
				if !errors.As(err, &conflict) || conflict.Holder.ID != id {
					t.Errorf("same-key operation = %v; want conflict with %s", err, id)
				}
			}
			probe, err := second.Submit(ctx, Spec{Kind: "test.unlocked",
				Func: func(context.Context, io.Writer) error { return nil }})
			if err != nil {
				t.Fatal(err)
			}
			(&harness{t: t, store: other}).waitForStatus(probe, store.StatusSucceeded)
		})
	}
}

func TestShutdownTimeoutRetainsOwnership(t *testing.T) {
	h := newHarness(t)
	started, cancelled, finish := make(chan struct{}), make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-finish:
		default:
			close(finish)
		}
	}()
	id, err := h.engine.Submit(t.Context(), Spec{Kind: "test.cleanup", LockKey: "lab", Func: func(ctx context.Context, _ io.Writer) error {
		close(started)
		<-ctx.Done()
		close(cancelled)
		<-finish
		return ctx.Err()
	}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not start")
	}
	rec, err := h.store.GetRun(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := h.engine.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown while cleanup is held = %v", err)
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("shutdown did not request cancellation")
	}
	if err := h.store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.GetRun(t.Context(), id); err != nil {
		t.Fatalf("worker store closed early: %v", err)
	}
	if lock, err := runowner.Probe(t.Context(), h.store.Path(), rec.OwnerID); !errors.Is(err, runowner.ErrBusy) {
		_ = lock.Close()
		t.Fatalf("owner lock released before cleanup: %v", err)
	}
	if lock, err := runowner.Upgrade(t.Context(), h.store.Path(), true); !errors.Is(err, runowner.ErrBusy) {
		_ = lock.Close()
		t.Fatalf("store lifetime lock released before cleanup: %v", err)
	}
	other, err := store.Open(t.Context(), h.store.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	if changed, err := other.RecoverOrphanedRuns(t.Context(), time.Now()); err != nil || len(changed) != 0 {
		t.Fatalf("timed-out shutdown permitted recovery: %v, %v", changed, err)
	}
	if err := other.CreateRun(t.Context(), store.Run{ID: "too-early", Kind: "test", LockKey: "lab"}); !errors.Is(err, store.ErrLockHeld) {
		t.Fatalf("cleanup lost key: %v", err)
	}
	close(finish)
	ctx, cancel = context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := h.engine.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := other.GetRun(t.Context(), id)
	if err != nil || after.Status != store.StatusCancelled {
		t.Fatalf("worker did not persist result: %+v, %v", after, err)
	}
	if _, err := h.store.GetRun(t.Context(), id); err == nil {
		t.Fatal("closed store stayed open after cleanup")
	}
	lock, err := runowner.Probe(t.Context(), h.store.Path(), rec.OwnerID)
	if err != nil {
		t.Fatalf("completed owner remained locked: %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestStartFailureCanBeRetried(t *testing.T) {
	for _, mode := range []string{"cancelled", "deadline", "closed store"} {
		t.Run(mode, func(t *testing.T) {
			st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "runs.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			resolver, err := toolchain.NewResolver(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			engine, err := New(st, toolchain.NewFake(), resolver)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			want := context.Canceled
			switch mode {
			case "cancelled":
				cancel()
			case "deadline":
				cancel()
				ctx, cancel = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
				want = context.DeadlineExceeded
			case "closed store":
				if err := st.Close(); err != nil {
					t.Fatal(err)
				}
			}
			defer cancel()
			if err := engine.Start(ctx); err == nil || mode != "closed store" && !errors.Is(err, want) {
				t.Fatalf("failed start = %v", err)
			}
			if mode != "closed store" {
				if err := engine.Start(t.Context()); err != nil {
					t.Fatalf("retry start = %v", err)
				}
				if err := engine.Start(t.Context()); err == nil {
					t.Fatal("accepted duplicate start")
				}
			}
			if err := engine.Shutdown(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := engine.Start(t.Context()); err == nil {
				t.Fatal("accepted start after shutdown")
			}
		})
	}
}

func TestStartWithMissingOwner(t *testing.T) {
	ctx := t.Context()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "runs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	owner, err := runowner.New(ctx, st.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	for _, id := range []string{"queued", "running"} {
		if err := st.CreateRun(ctx, store.Run{ID: id, Kind: "test", LockKey: id, OwnerID: owner.ID}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.StartOwnedRun(ctx, "running", owner.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	dir, err := runowner.Directory(st.Path())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, owner.ID)); err != nil {
		t.Fatal(err)
	}
	resolver, err := toolchain.NewResolver(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	engine, err := New(st, toolchain.NewFake(), resolver, WithWorkers(1))
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Start(ctx); err != nil {
		t.Fatalf("missing owner blocked engine startup: %v", err)
	}
	defer engine.Shutdown(ctx)
	for _, tc := range []struct {
		id       string
		status   store.Status
		recovery store.RecoveryState
	}{
		{id: "queued", status: store.StatusCancelled, recovery: store.RecoveryNone},
		{id: "running", status: store.StatusRunning, recovery: store.RecoveryRequired},
	} {
		t.Run(tc.id, func(t *testing.T) {
			rec, err := st.GetRun(ctx, tc.id)
			if err != nil || rec.Status != tc.status || rec.RecoveryState != tc.recovery || rec.RecoveryReason != "owner_missing" {
				t.Fatalf("recovered run = %+v, %v", rec, err)
			}
		})
	}
	id, err := engine.Submit(ctx, Spec{Kind: "independent", LockKey: "other", Func: func(context.Context, io.Writer) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	(&harness{t: t, store: st}).waitForStatus(id, store.StatusSucceeded)
}

func TestStartCleansUnreferencedOwners(t *testing.T) {
	ctx := t.Context()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "runs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, tc := range []struct {
		name             string
		referenced, busy bool
	}{
		{name: "unused"}, {name: "busy", busy: true}, {name: "queued", referenced: true},
		{name: "running", referenced: true}, {name: "recovery", referenced: true}, {name: "finished"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owner, err := runowner.New(ctx, st.Path())
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			if tc.referenced || tc.name == "finished" {
				if err := st.CreateRun(ctx, store.Run{ID: tc.name, Kind: "test", LockKey: tc.name, OwnerID: owner.ID}); err != nil {
					t.Fatal(err)
				}
				if tc.name != "queued" {
					if err := st.StartOwnedRun(ctx, tc.name, owner.ID, time.Now()); err != nil {
						t.Fatal(err)
					}
				}
				if tc.name == "finished" || tc.name == "recovery" {
					if err := st.FinishOwnedRun(ctx, tc.name, owner.ID, store.StatusSucceeded, nil, "", time.Now(), 0); err != nil {
						t.Fatal(err)
					}
				}
				if tc.name == "recovery" {
					if _, err := st.DB().ExecContext(ctx, `UPDATE runs SET recovery_state = 'required' WHERE id = ?`, tc.name); err != nil {
						t.Fatal(err)
					}
				}
			}
			if !tc.busy {
				if err := owner.Close(); err != nil {
					t.Fatal(err)
				}
			}
			resolver, err := toolchain.NewResolver(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			engine, err := New(st, toolchain.NewFake(), resolver)
			if err != nil {
				t.Fatal(err)
			}
			if err := engine.Start(ctx); err != nil {
				t.Fatal(err)
			}
			defer engine.Shutdown(ctx)
			dir, err := runowner.Directory(st.Path())
			if err != nil {
				t.Fatal(err)
			}
			_, err = os.Stat(filepath.Join(dir, owner.ID))
			keep := tc.referenced || tc.busy
			if keep && err != nil || !keep && !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("owner file retained=%v: %v", keep, err)
			}
			if _, err := runowner.Probe(ctx, st.Path(), engine.owner.ID); !errors.Is(err, runowner.ErrBusy) {
				t.Fatalf("startup removed or unlocked its owner: %v", err)
			}
		})
	}
}

func TestStartContinuesAfterCleanupErrors(t *testing.T) {
	for _, kind := range []string{"directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			ctx := t.Context()
			st, err := store.Open(ctx, filepath.Join(t.TempDir(), "runs.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			dir, err := runowner.Directory(st.Path())
			if err != nil {
				t.Fatal(err)
			}
			id := "owner_" + strings.Repeat("0", 32)
			path := filepath.Join(dir, id)
			if kind == "directory" {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Symlink(filepath.Join(dir, "missing"), path); err != nil {
				t.Fatal(err)
			}
			resolver, err := toolchain.NewResolver(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			engine, err := New(st, toolchain.NewFake(), resolver, WithWorkers(1))
			if err != nil {
				t.Fatal(err)
			}
			var warnings bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&warnings, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })
			if err := engine.Start(ctx); err != nil {
				t.Fatalf("cleanup blocked engine: %v", err)
			}
			defer engine.Shutdown(ctx)
			if output := warnings.String(); !strings.Contains(output, `"level":"WARN"`) || !strings.Contains(output, id) {
				t.Fatalf("cleanup warning missing: %s", output)
			}
			if _, err := os.Lstat(path); err != nil {
				t.Fatalf("cleanup changed invalid entry: %v", err)
			}
			work, err := engine.Submit(ctx, Spec{Kind: "independent", LockKey: "lab", Func: func(context.Context, io.Writer) error { return nil }})
			if err != nil {
				t.Fatal(err)
			}
			(&harness{t: t, store: st}).waitForStatus(work, store.StatusSucceeded)
		})
	}
}
