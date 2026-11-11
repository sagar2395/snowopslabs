// SPDX-License-Identifier: Apache-2.0

package traffic

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/snowops-oss/snowopslabs/internal/run"
	"github.com/snowops-oss/snowopslabs/internal/store"
	"github.com/snowops-oss/snowopslabs/internal/toolchain"
	trafficopts "github.com/snowops-oss/snowopslabs/internal/traffic"
)

type harness struct {
	svc    *Service
	engine *run.Engine
	store  *store.Store
	fake   *toolchain.Fake
	root   string
	events <-chan run.Event
}

func newHarness(t *testing.T, env map[string]string) *harness {
	t.Helper()
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
	})
	root := t.TempDir()
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	for _, script := range []string{"start.sh", "stop.sh", "status.sh", "profiles/steady.js"} {
		filename := filepath.Join(root, trafficopts.ScriptDir, script)
		if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(filename, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	resolver, err := toolchain.NewResolver(root)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	fake := toolchain.NewFake()
	engine, err := run.New(
		st,
		fake,
		resolver,
		run.WithWorkers(2),
	)
	if err != nil {
		t.Fatalf("run.New: %v", err)
	}
	if err := engine.Start(t.Context()); err != nil {
		t.Fatalf("engine.Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		if err := engine.Shutdown(ctx); err != nil {
			t.Errorf("engine.Shutdown: %v", err)
		}
	})
	events, unsubscribe := engine.Subscribe("")
	t.Cleanup(unsubscribe)
	svc, err := New(engine, root, env)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return &harness{svc: svc, engine: engine, store: st, fake: fake, root: root, events: events}
}

func (h *harness) waitFor(t *testing.T, id string, want store.Status) store.Run {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	for {
		rec, err := h.store.GetRun(ctx, id)
		if err != nil {
			t.Fatalf("GetRun: %v", err)
		}
		if rec.Status == want {
			return rec
		}
		if rec.Status.Terminal() {
			t.Fatalf("run %s reached %s, want %s: %s", id, rec.Status, want, rec.Error)
		}
		select {
		case <-h.events:
		case <-ctx.Done():
			t.Fatalf("waiting for run %s to reach %s: %v", id, want, ctx.Err())
		}
	}
}

func validOptions() trafficopts.Options {
	return trafficopts.Options{Profile: "steady", Target: "https://example.test", RPS: 20}
}

func TestOperationsRecordHistory(t *testing.T) {
	for _, tc := range []struct {
		name    string
		kind    string
		lock    string
		timeout time.Duration
	}{
		{
			name: "start", kind: "traffic.start", lock: "traffic", timeout: 2 * time.Minute,
		},
		{
			name: "stop", kind: "traffic.stop", lock: "traffic", timeout: 2 * time.Minute,
		},
		{
			name: "status", kind: "traffic.status", timeout: 30 * time.Second,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, map[string]string{"NAMESPACE": "demo"})
			h.fake.WhenArgsContainStderr(
				tc.name+".sh",
				"traffic ready\n",
				"traffic detail\n",
				0,
			)
			id, err := callOperation(t.Context(), h.svc, tc.name)
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			rec := h.waitFor(t, id, store.StatusSucceeded)
			wantScript := filepath.ToSlash(filepath.Join(trafficopts.ScriptDir, tc.name+".sh"))
			hasMetadata := rec.Kind == tc.kind && rec.Target == "traffic" && rec.LockKey == tc.lock
			if !hasMetadata || rec.Script != wantScript {
				t.Errorf(
					"history = %+v, want kind %s, target traffic, lock %q, script %s",
					rec,
					tc.kind,
					tc.lock,
					wantScript,
				)
			}
			if rec.Timeout != tc.timeout {
				t.Errorf("timeout = %s, want %s", rec.Timeout, tc.timeout)
			}
			calls := h.fake.Calls()
			if len(calls) != 1 {
				t.Fatalf("calls = %+v, want one script", calls)
			}
			if calls[0].Path != filepath.Join(h.root, wantScript) || calls[0].Dir != h.root {
				t.Fatalf(
					"calls = %+v, want one %s script in %s",
					calls,
					tc.name,
					h.root,
				)
			}
			if calls[0].Env["NAMESPACE"] != "demo" {
				t.Errorf("environment = %v, want configured namespace", calls[0].Env)
			}
			logs, err := h.store.ReadLogs(
				t.Context(),
				id,
				0,
				100,
			)
			if err != nil {
				t.Fatalf("ReadLogs: %v", err)
			}
			if len(logs) != 2 {
				t.Fatalf("logs = %+v, want two output lines", logs)
			}
			hasStdout := logs[0].Text == "traffic ready" && logs[0].Stream == store.StreamStdout
			hasStderr := logs[1].Text == "traffic detail" && logs[1].Stream == store.StreamStderr
			if !hasStdout || !hasStderr {
				t.Errorf("logs = %+v, want durable stdout and stderr", logs)
			}
		})
	}
}

func TestNew(t *testing.T) {
	h := newHarness(t, nil)
	for _, tc := range []struct {
		name   string
		engine *run.Engine
		root   string
		want   string
	}{
		{name: "missing engine", root: h.root, want: "run engine is required"},
		{name: "missing root", engine: h.engine, want: "project root is required"},
		{name: "valid", engine: h.engine, root: h.root},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, err := New(tc.engine, tc.root, nil)
			if tc.want == "" {
				if err != nil || svc == nil {
					t.Fatalf("New = %v, %v, want service", svc, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) || svc != nil {
				t.Errorf("New = %v, %v, want %q", svc, err, tc.want)
			}
		})
	}
}

func TestStartValidatesOptions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*trafficopts.Options)
		want   string
	}{
		{name: "missing profile", change: func(o *trafficopts.Options) { o.Profile = "" }, want: "profile is required"},
		{name: "unknown profile", change: func(o *trafficopts.Options) { o.Profile = "missing" }, want: "available: steady"},
		{name: "low rps", change: func(o *trafficopts.Options) { o.RPS = 0 }, want: "between 1 and 10000"},
		{name: "high rps", change: func(o *trafficopts.Options) { o.RPS = 10001 }, want: "between 1 and 10000"},
		{name: "bad duration", change: func(o *trafficopts.Options) { o.Duration = "forever" }, want: "use Go syntax"},
		{
			name:   "negative duration",
			change: func(o *trafficopts.Options) { o.Duration = "-1s" },
			want:   "duration must be positive",
		},
		{name: "bad target", change: func(o *trafficopts.Options) { o.Target = "ftp://example.test" }, want: "http(s) URL"},
		{
			name:   "bad method",
			change: func(o *trafficopts.Options) { o.Method = "OPTIONS" },
			want:   "GET/POST/PUT/PATCH/DELETE",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, nil)
			opts := validOptions()
			tc.change(&opts)
			id, err := h.svc.Start(t.Context(), opts)
			if err == nil || id != "" || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Start = %q, %v, want %q", id, err, tc.want)
			}
			assertNoRuns(t, h)
		})
	}
}

func TestEnvironmentIsolation(t *testing.T) {
	base := map[string]string{"NAMESPACE": "demo", "TRAFFIC_RPS": "5"}
	original := maps.Clone(base)
	h := newHarness(t, base)
	base["NAMESPACE"] = "changed"
	opts := validOptions()
	opts.Duration, opts.Method = "45s", "POST"
	first, err := h.svc.Start(t.Context(), opts)
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}
	h.waitFor(t, first, store.StatusSucceeded)
	opts.Target, opts.Duration, opts.Method = "", "", ""
	opts.RPS = 1
	second, err := h.svc.Start(t.Context(), opts)
	if err != nil {
		t.Fatalf("second Start: %v", err)
	}
	h.waitFor(t, second, store.StatusSucceeded)
	stop, err := h.svc.Stop(t.Context())
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	h.waitFor(t, stop, store.StatusSucceeded)
	status, err := h.svc.Status(t.Context())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	h.waitFor(t, status, store.StatusSucceeded)
	wants := []map[string]string{
		{
			"NAMESPACE": "demo", "TRAFFIC_PROFILE": "steady", "TRAFFIC_RPS": "20",
			"TRAFFIC_TARGET": "https://example.test", "TRAFFIC_DURATION": "45s", "TRAFFIC_METHOD": "POST",
		},
		{"NAMESPACE": "demo", "TRAFFIC_PROFILE": "steady", "TRAFFIC_RPS": "1"},
		original,
		original,
	}
	calls := h.fake.Calls()
	if len(calls) != len(wants) {
		t.Fatalf("got %d calls, want %d", len(calls), len(wants))
	}
	for i, want := range wants {
		if !maps.Equal(calls[i].Env, want) {
			t.Errorf("call %d environment = %v, want %v", i, calls[i].Env, want)
		}
	}
	if base["TRAFFIC_RPS"] != "5" || len(base) != 2 {
		t.Errorf("caller environment was mutated: %v", base)
	}
	calls[2].Env["NAMESPACE"] = "runner change"
	if calls[3].Env["NAMESPACE"] != "demo" {
		t.Errorf("stop and status share an environment: %v", calls[3].Env)
	}
}

func TestOperationsRespectCallerContext(t *testing.T) {
	for _, operation := range []string{"start", "stop", "status"} {
		for _, state := range []string{"cancelled", "deadline"} {
			t.Run(operation+" "+state, func(t *testing.T) {
				h := newHarness(t, nil)
				ctx, cancel := context.WithCancel(t.Context())
				want := context.Canceled
				if state == "deadline" {
					cancel()
					ctx, cancel = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
					want = context.DeadlineExceeded
				}
				cancel()
				id, err := callOperation(ctx, h.svc, operation)
				if id != "" || !errors.Is(err, want) {
					t.Fatalf("%s = %q, %v, want %v", operation, id, err, want)
				}
				assertNoRuns(t, h)
			})
		}
	}
}

func TestConflictingOperationsNameHolder(t *testing.T) {
	for _, firstOperation := range []string{"start", "stop"} {
		for _, nextOperation := range []string{"start", "stop"} {
			t.Run(firstOperation+" blocks "+nextOperation, func(t *testing.T) {
				h := newHarness(t, nil)
				h.fake.WhenArgsContainBlock(firstOperation+".sh", time.Hour)
				first, err := callOperation(t.Context(), h.svc, firstOperation)
				if err != nil {
					t.Fatalf("first operation: %v", err)
				}
				h.waitFor(t, first, store.StatusRunning)
				id, err := callOperation(t.Context(), h.svc, nextOperation)
				var conflict *run.LockConflictError
				if id != "" || !errors.As(err, &conflict) {
					t.Fatalf("second operation = %q, %v, want lock conflict", id, err)
				}
				if conflict.Holder.ID != first || conflict.LockKey != "traffic" ||
					!strings.Contains(err.Error(), "labctl runs cancel "+first) {
					t.Errorf("lock error = %v, want holder %s and its cancellation command", err, first)
				}
				status, err := h.svc.Status(t.Context())
				if err != nil {
					t.Fatalf("Status while %s runs: %v", firstOperation, err)
				}
				h.waitFor(t, status, store.StatusSucceeded)
				if err := h.engine.Cancel(t.Context(), first); err != nil {
					t.Fatalf("Cancel: %v", err)
				}
				h.waitFor(t, first, store.StatusCancelled)
				if _, held, err := h.store.ActiveRunForLock(t.Context(), "traffic"); err != nil || held {
					t.Errorf("traffic lock after cancellation: held=%v, err=%v", held, err)
				}
			})
		}
	}
}

func TestOperationsTimeOut(t *testing.T) {
	for _, operation := range []string{"start", "stop", "status"} {
		t.Run(operation, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := newHarness(t, nil)
				h.fake.WhenArgsContainBlock(operation+".sh", time.Hour)
				id, err := callOperation(t.Context(), h.svc, operation)
				if err != nil {
					t.Fatalf("%s: %v", operation, err)
				}
				rec := h.waitFor(t, id, store.StatusTimedOut)
				if !strings.Contains(rec.Error, "timed out after "+rec.Timeout.String()) {
					t.Errorf("timeout error = %q, want timeout duration", rec.Error)
				}
			})
		})
	}
}

func TestOperationErrors(t *testing.T) {
	for _, operation := range []string{"start", "stop", "status"} {
		for _, failure := range []string{"missing script", "stopped engine", "closed database", "script exit", "runner error"} {
			t.Run(operation+" "+failure, func(t *testing.T) {
				h := newHarness(t, nil)
				switch failure {
				case "missing script":
					if err := os.Remove(filepath.Join(h.root, trafficopts.ScriptDir, operation+".sh")); err != nil {
						t.Fatal(err)
					}
				case "stopped engine":
					if err := h.engine.Shutdown(t.Context()); err != nil {
						t.Fatal(err)
					}
				case "closed database":
					// Close retains the handle for the active engine, so close the raw
					// database to exercise an unavailable dependency.
					if err := h.store.DB().Close(); err != nil {
						t.Fatal(err)
					}
				case "script exit":
					h.fake.WhenArgsContainStderr(
						operation+".sh",
						"partial output\n",
						"cluster unavailable\n",
						7,
					)
				case "runner error":
					h.fake.WhenArgsContainError(operation+".sh", errors.New("permission denied; check script permissions"))
				}
				id, err := callOperation(t.Context(), h.svc, operation)
				if failure == "script exit" || failure == "runner error" {
					if err != nil {
						t.Fatalf("submit: %v", err)
					}
					rec := h.waitFor(t, id, store.StatusFailed)
					hasExitCode := rec.ExitCode != nil && *rec.ExitCode == 7
					hasStderr := strings.Contains(rec.Error, "cluster unavailable")
					if failure == "script exit" && (!hasExitCode || !hasStderr) {
						t.Errorf("failed run = %+v, want script exit and stderr", rec)
					}
					if failure == "runner error" && !strings.Contains(rec.Error, "check script permissions") {
						t.Errorf("failed run error = %q", rec.Error)
					}
					return
				}
				if id != "" || err == nil {
					t.Fatalf("submit = %q, %v, want error", id, err)
				}
				if failure == "missing script" && !errors.Is(err, toolchain.ErrScriptNotFound) {
					t.Errorf("missing script error = %v", err)
				}
				if failure == "stopped engine" && !errors.Is(err, run.ErrNotRunning) {
					t.Errorf("stopped engine error = %v", err)
				}
				if failure == "closed database" && !strings.Contains(err.Error(), "database is closed") {
					t.Errorf("closed database error = %v", err)
				}
			})
		}
	}
}

func callOperation(ctx context.Context, svc *Service, operation string) (string, error) {
	switch operation {
	case "start":
		return svc.Start(ctx, validOptions())
	case "stop":
		return svc.Stop(ctx)
	default:
		return svc.Status(ctx)
	}
}

func assertNoRuns(t *testing.T, h *harness) {
	t.Helper()
	runs, err := h.store.ListRuns(t.Context(), store.RunFilter{})
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 0 || len(h.fake.Calls()) != 0 {
		t.Errorf("rejected request created runs or calls: runs=%v, calls=%v", runs, h.fake.Calls())
	}
}

func TestSeparateEnginesPreserveTrafficOwner(t *testing.T) {
	for _, firstOperation := range []string{"start", "stop"} {
		for _, nextOperation := range []string{"start", "stop"} {
			t.Run(firstOperation+" blocks "+nextOperation, func(t *testing.T) {
				ctx := t.Context()
				first := newHarness(t, nil)
				first.fake.WhenArgsContainBlock(firstOperation+".sh", time.Hour)
				id, err := callOperation(ctx, first.svc, firstOperation)
				if err != nil {
					t.Fatal(err)
				}
				first.waitFor(t, id, store.StatusRunning)
				if _, err := first.store.AppendLogs(ctx, id, []store.LogLine{{Text: "live traffic output"}}); err != nil {
					t.Fatal(err)
				}

				st, err := store.Open(ctx, first.store.Path())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = st.Close() })
				resolver, err := toolchain.NewResolver(first.root)
				if err != nil {
					t.Fatal(err)
				}
				fake := toolchain.NewFake()
				engine, err := run.New(st, fake, resolver, run.WithWorkers(1))
				if err != nil {
					t.Fatal(err)
				}
				if err := engine.Start(ctx); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
					defer cancel()
					if err := engine.Shutdown(shutdown); err != nil {
						t.Error(err)
					}
				})
				events, unsubscribe := engine.Subscribe("")
				t.Cleanup(unsubscribe)
				svc, err := New(engine, first.root, nil)
				if err != nil {
					t.Fatal(err)
				}
				second := &harness{svc: svc, engine: engine, store: st, fake: fake, root: first.root, events: events}

				status, err := second.svc.Status(ctx)
				if err != nil {
					t.Fatalf("independent status: %v", err)
				}
				second.waitFor(t, status, store.StatusSucceeded)
				rec, err := st.GetRun(ctx, id)
				if err != nil || rec.Status != store.StatusRunning {
					t.Fatalf("second engine interrupted live traffic: %+v, %v", rec, err)
				}
				lines, err := st.ReadLogs(ctx, id, 0, 100)
				if err != nil {
					t.Fatal(err)
				}
				preserved := false
				for _, line := range lines {
					preserved = preserved || line.Text == "live traffic output"
				}
				if !preserved {
					t.Fatal("second engine lost the live transcript")
				}
				if _, err := callOperation(ctx, second.svc, nextOperation); err == nil {
					t.Fatal("second engine bypassed the live traffic key")
				} else {
					var conflict *run.LockConflictError
					if !errors.As(err, &conflict) || conflict.Holder.ID != id || conflict.LockKey != LockKey {
						t.Fatalf("wrong cross-engine conflict: %v", err)
					}
				}
				if err := first.engine.Cancel(ctx, id); err != nil {
					t.Fatal(err)
				}
				shutdown, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()
				if err := first.engine.Shutdown(shutdown); err != nil {
					t.Fatalf("waiting for first owner: %v", err)
				}
				first.waitFor(t, id, store.StatusCancelled)
				next, err := callOperation(ctx, second.svc, nextOperation)
				if err != nil {
					t.Fatalf("traffic key after owner shutdown: %v", err)
				}
				second.waitFor(t, next, store.StatusSucceeded)
			})
		}
	}
}
