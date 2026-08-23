// SPDX-License-Identifier: Apache-2.0

package run

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sagar2395/snowopslabs/internal/store"
	"github.com/sagar2395/snowopslabs/internal/toolchain"
)

// harness wires an engine to a temp store, a fake runner and a scratch content
// root. Every test is hermetic: no cluster, no network, no real binaries.
type harness struct {
	t       *testing.T
	engine  *Engine
	store   *store.Store
	fake    *toolchain.Fake
	root    string
	scripts map[string]string
}

func newHarness(t *testing.T, opts ...Option) *harness {
	t.Helper()
	ctx := context.Background()

	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	root := t.TempDir()
	resolver, err := toolchain.NewResolver(root)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	fake := toolchain.NewFake()
	engine, err := New(st, fake, resolver, append([]Option{WithWorkers(2)}, opts...)...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := engine.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	h := &harness{t: t, engine: engine, store: st, fake: fake, root: root, scripts: map[string]string{}}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = engine.Shutdown(shutdownCtx)
	})
	return h
}

// script creates an executable file in the content root and returns its
// relative name, so specs can reference it.
func (h *harness) script(name string) string {
	h.t.Helper()
	path := filepath.Join(h.root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		h.t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte("#!/usr/bin/env bash\n"), 0o755); err != nil {
		h.t.Fatalf("WriteFile: %v", err)
	}
	return name
}

// waitForStatus polls until the run reaches a terminal state or the deadline
// passes. Polling the store (rather than sleeping a fixed time) keeps the test
// fast and non-flaky.
func (h *harness) waitForStatus(id string, want store.Status) store.Run {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var last store.Run
	for time.Now().Before(deadline) {
		rec, err := h.store.GetRun(context.Background(), id)
		if err == nil {
			last = rec
			if rec.Status == want {
				return rec
			}
			if rec.Status.Terminal() && want.Terminal() && rec.Status != want {
				h.t.Fatalf("run %s reached %q, want %q (error: %s)", id, rec.Status, want, rec.Error)
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatalf("run %s did not reach %q within 10s (last status %q)", id, want, last.Status)
	return last
}

func (h *harness) logText(id string) string {
	h.t.Helper()
	lines, err := h.store.ReadLogs(context.Background(), id, 0, 1000)
	if err != nil {
		h.t.Fatalf("ReadLogs: %v", err)
	}
	var sb strings.Builder
	for _, l := range lines {
		sb.WriteString(l.Text)
		sb.WriteString("\n")
	}
	return sb.String()
}

func TestNew(t *testing.T) {
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()
	resolver, _ := toolchain.NewResolver(t.TempDir())

	tests := []struct {
		name     string
		store    *store.Store
		runner   toolchain.Runner
		resolver *toolchain.Resolver
	}{
		{"nil store", nil, toolchain.NewFake(), resolver},
		{"nil runner", st, nil, resolver},
		{"nil resolver", st, toolchain.NewFake(), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := New(tt.store, tt.runner, tt.resolver); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestSubmit(t *testing.T) {
	ctx := context.Background()

	t.Run("runs a script to success", func(t *testing.T) {
		h := newHarness(t)
		id, err := h.engine.Submit(ctx, Spec{
			Kind:   "platform.install",
			Target: "ingress/traefik",
			Script: h.script("install.sh"),
			Args:   []string{"--namespace", "ingress"},
		})
		if err != nil {
			t.Fatalf("Submit: %v", err)
		}

		rec := h.waitForStatus(id, store.StatusSucceeded)
		if rec.ExitCode == nil || *rec.ExitCode != 0 {
			t.Errorf("exit code = %v, want 0", rec.ExitCode)
		}
		if rec.Duration <= 0 {
			t.Error("duration was not recorded")
		}
		if !h.fake.Called("install.sh") {
			t.Errorf("the script was never invoked; calls: %v", h.fake.CallStrings())
		}
		if !h.fake.Called("--namespace ingress") {
			t.Errorf("arguments were not passed through; calls: %v", h.fake.CallStrings())
		}
	})

	t.Run("records a failing script with its exit code", func(t *testing.T) {
		h := newHarness(t)
		h.fake.WhenArgsContainStderr("install.sh", "", "helm: release exists\n", 1)

		id, err := h.engine.Submit(ctx, Spec{Kind: "platform.install", Script: h.script("install.sh")})
		if err != nil {
			t.Fatalf("Submit: %v", err)
		}

		rec := h.waitForStatus(id, store.StatusFailed)
		if rec.ExitCode == nil || *rec.ExitCode != 1 {
			t.Errorf("exit code = %v, want 1", rec.ExitCode)
		}
		if !strings.Contains(h.logText(id), "release exists") {
			t.Errorf("stderr did not reach the transcript:\n%s", h.logText(id))
		}
	})

	t.Run("captures stdout and stderr into the transcript", func(t *testing.T) {
		h := newHarness(t)
		h.fake.WhenArgsContainStderr("run.sh", "line one\nline two\n", "a warning\n", 0)

		id, err := h.engine.Submit(ctx, Spec{Kind: "test", Script: h.script("run.sh")})
		if err != nil {
			t.Fatalf("Submit: %v", err)
		}
		h.waitForStatus(id, store.StatusSucceeded)

		lines, err := h.store.ReadLogs(ctx, id, 0, 100)
		if err != nil {
			t.Fatalf("ReadLogs: %v", err)
		}
		var stdout, stderr []string
		for _, l := range lines {
			switch l.Stream {
			case store.StreamStdout:
				stdout = append(stdout, l.Text)
			case store.StreamStderr:
				stderr = append(stderr, l.Text)
			}
		}
		if strings.Join(stdout, "|") != "line one|line two" {
			t.Errorf("stdout = %v", stdout)
		}
		if strings.Join(stderr, "|") != "a warning" {
			t.Errorf("stderr = %v", stderr)
		}
	})

	t.Run("rejects a spec missing required fields", func(t *testing.T) {
		h := newHarness(t)
		tests := []struct {
			name string
			spec Spec
		}{
			{"no kind", Spec{Script: "x.sh"}},
			{"no script", Spec{Kind: "test"}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if _, err := h.engine.Submit(ctx, tt.spec); err == nil {
					t.Fatal("expected an error")
				}
			})
		}
	})

	t.Run("rejects an unresolvable script before creating a run", func(t *testing.T) {
		// A typo in a scenario should be an immediate clear error, not a
		// queued run that fails later and clutters the history.
		h := newHarness(t)
		_, err := h.engine.Submit(ctx, Spec{Kind: "test", Script: "does-not-exist.sh"})
		if !errors.Is(err, toolchain.ErrScriptNotFound) {
			t.Fatalf("error = %v, want ErrScriptNotFound", err)
		}
		runs, _ := h.store.ListRuns(ctx, store.RunFilter{})
		if len(runs) != 0 {
			t.Errorf("a rejected submission created %d run records", len(runs))
		}
	})

	t.Run("refuses a script outside the content root", func(t *testing.T) {
		h := newHarness(t)
		outside := filepath.Join(t.TempDir(), "evil.sh")
		if err := os.WriteFile(outside, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		_, err := h.engine.Submit(ctx, Spec{Kind: "test", Script: outside})
		if !errors.Is(err, toolchain.ErrOutsideRoot) {
			t.Fatalf("error = %v, want ErrOutsideRoot", err)
		}
	})

	t.Run("writes an audit entry", func(t *testing.T) {
		h := newHarness(t)
		id, err := h.engine.Submit(ctx, Spec{
			Kind: "platform.install", Target: "ingress", Actor: "sagar",
			Script: h.script("install.sh"),
		})
		if err != nil {
			t.Fatalf("Submit: %v", err)
		}
		h.waitForStatus(id, store.StatusSucceeded)

		entries, err := h.store.ListAudit(ctx, 10)
		if err != nil {
			t.Fatalf("ListAudit: %v", err)
		}
		if len(entries) == 0 {
			t.Fatal("no audit entry was written")
		}
		if entries[0].Actor != "sagar" || entries[0].RunID != id {
			t.Errorf("audit entry = %+v", entries[0])
		}
	})

	t.Run("fails when the engine is not running", func(t *testing.T) {
		st, _ := store.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
		defer st.Close()
		resolver, _ := toolchain.NewResolver(t.TempDir())
		e, _ := New(st, toolchain.NewFake(), resolver)

		if _, err := e.Submit(ctx, Spec{Kind: "test", Script: "x.sh"}); !errors.Is(err, ErrNotRunning) {
			t.Fatalf("error = %v, want ErrNotRunning", err)
		}
	})
}

// TestLockConflict covers ADR-0004: conflicting work is refused immediately
// and the refusal names the run holding the lock.
func TestLockConflict(t *testing.T) {
	ctx := context.Background()

	t.Run("refuses a second run on the same key, naming the holder", func(t *testing.T) {
		h := newHarness(t)
		h.fake.WhenArgsContainBlock("slow.sh", 30*time.Second)

		first, err := h.engine.Submit(ctx, Spec{
			Kind: "platform.install", Target: "ingress", LockKey: "lab:default",
			Script: h.script("slow.sh"),
		})
		if err != nil {
			t.Fatalf("first Submit: %v", err)
		}
		h.waitForStatus(first, store.StatusRunning)

		start := time.Now()
		_, err = h.engine.Submit(ctx, Spec{
			Kind: "platform.install", LockKey: "lab:default", Script: h.script("other.sh"),
		})
		elapsed := time.Since(start)

		var conflict *LockConflictError
		if !errors.As(err, &conflict) {
			t.Fatalf("error = %v, want *LockConflictError", err)
		}
		// The exit criterion: refusal is immediate, not a wait.
		if elapsed > 100*time.Millisecond {
			t.Errorf("took %v to refuse; the conflict must be reported immediately", elapsed)
		}
		if conflict.Holder.ID != first {
			t.Errorf("holder = %q, want %q", conflict.Holder.ID, first)
		}
		// The message must be actionable, not just "conflict".
		msg := conflict.Error()
		for _, want := range []string{first, "already in progress", "labctl runs cancel"} {
			if !strings.Contains(msg, want) {
				t.Errorf("message %q should mention %q", msg, want)
			}
		}
	})

	t.Run("different keys run concurrently", func(t *testing.T) {
		h := newHarness(t)
		h.fake.WhenArgsContainBlock("slow.sh", 400*time.Millisecond)

		a, err := h.engine.Submit(ctx, Spec{Kind: "test", LockKey: "lab:a", Script: h.script("slow.sh")})
		if err != nil {
			t.Fatalf("Submit a: %v", err)
		}
		b, err := h.engine.Submit(ctx, Spec{Kind: "test", LockKey: "lab:b", Script: h.script("slow.sh")})
		if err != nil {
			t.Fatalf("Submit b: %v", err)
		}
		h.waitForStatus(a, store.StatusSucceeded)
		h.waitForStatus(b, store.StatusSucceeded)
	})

	t.Run("an empty lock key never conflicts", func(t *testing.T) {
		h := newHarness(t)
		h.fake.WhenArgsContainBlock("slow.sh", 300*time.Millisecond)

		for range 2 {
			if _, err := h.engine.Submit(ctx, Spec{Kind: "test", Script: h.script("slow.sh")}); err != nil {
				t.Fatalf("Submit: %v", err)
			}
		}
	})

	t.Run("the lock is released when the holder finishes", func(t *testing.T) {
		h := newHarness(t)
		first, err := h.engine.Submit(ctx, Spec{Kind: "test", LockKey: "k", Script: h.script("fast.sh")})
		if err != nil {
			t.Fatalf("Submit: %v", err)
		}
		h.waitForStatus(first, store.StatusSucceeded)

		second, err := h.engine.Submit(ctx, Spec{Kind: "test", LockKey: "k", Script: h.script("fast.sh")})
		if err != nil {
			t.Fatalf("second Submit after release: %v", err)
		}
		h.waitForStatus(second, store.StatusSucceeded)
	})
}

func TestCancel(t *testing.T) {
	ctx := context.Background()

	t.Run("cancels a running run and says so in the transcript", func(t *testing.T) {
		h := newHarness(t)
		h.fake.WhenArgsContainBlock("slow.sh", 30*time.Second)

		id, err := h.engine.Submit(ctx, Spec{Kind: "test", Script: h.script("slow.sh")})
		if err != nil {
			t.Fatalf("Submit: %v", err)
		}
		h.waitForStatus(id, store.StatusRunning)

		if err := h.engine.Cancel(ctx, id); err != nil {
			t.Fatalf("Cancel: %v", err)
		}

		rec := h.waitForStatus(id, store.StatusCancelled)
		if !strings.Contains(rec.Error, "cancelled") {
			t.Errorf("error = %q, want it to mention cancellation", rec.Error)
		}
		// The reason must appear where the user is already looking.
		if !strings.Contains(h.logText(id), "cancelled by user") {
			t.Errorf("the transcript does not explain the cancellation:\n%s", h.logText(id))
		}
	})

	t.Run("cancels a run still in the queue", func(t *testing.T) {
		// Saturate the workers so the next submission has to wait.
		h := newHarness(t, WithWorkers(1))
		h.fake.WhenArgsContainBlock("slow.sh", 30*time.Second)

		blocker, err := h.engine.Submit(ctx, Spec{Kind: "test", Script: h.script("slow.sh")})
		if err != nil {
			t.Fatalf("Submit blocker: %v", err)
		}
		h.waitForStatus(blocker, store.StatusRunning)

		queued, err := h.engine.Submit(ctx, Spec{Kind: "test", Script: h.script("queued.sh")})
		if err != nil {
			t.Fatalf("Submit queued: %v", err)
		}
		if err := h.engine.Cancel(ctx, queued); err != nil {
			t.Fatalf("Cancel: %v", err)
		}

		rec := h.waitForStatus(queued, store.StatusCancelled)
		if !rec.StartedAt.IsZero() {
			t.Error("a run cancelled from the queue must never have started")
		}
		if h.fake.Called("queued.sh") {
			t.Error("the cancelled run's script was executed anyway")
		}
	})

	t.Run("refuses to cancel a finished run", func(t *testing.T) {
		h := newHarness(t)
		id, err := h.engine.Submit(ctx, Spec{Kind: "test", Script: h.script("fast.sh")})
		if err != nil {
			t.Fatalf("Submit: %v", err)
		}
		h.waitForStatus(id, store.StatusSucceeded)

		if err := h.engine.Cancel(ctx, id); !errors.Is(err, ErrNotCancellable) {
			t.Fatalf("error = %v, want ErrNotCancellable", err)
		}
	})

	t.Run("reports an unknown run", func(t *testing.T) {
		h := newHarness(t)
		if err := h.engine.Cancel(ctx, "run_nonexistent"); !errors.Is(err, store.ErrRunNotFound) {
			t.Fatalf("error = %v, want ErrRunNotFound", err)
		}
	})

	t.Run("cancelling twice is not an error the second time round", func(t *testing.T) {
		h := newHarness(t)
		h.fake.WhenArgsContainBlock("slow.sh", 30*time.Second)

		id, err := h.engine.Submit(ctx, Spec{Kind: "test", Script: h.script("slow.sh")})
		if err != nil {
			t.Fatalf("Submit: %v", err)
		}
		h.waitForStatus(id, store.StatusRunning)

		if err := h.engine.Cancel(ctx, id); err != nil {
			t.Fatalf("first Cancel: %v", err)
		}
		h.waitForStatus(id, store.StatusCancelled)

		// The second attempt must be a clear refusal, not a panic or a
		// duplicate terminal record.
		if err := h.engine.Cancel(ctx, id); !errors.Is(err, ErrNotCancellable) {
			t.Fatalf("second Cancel error = %v, want ErrNotCancellable", err)
		}
	})
}

func TestTimeout(t *testing.T) {
	ctx := context.Background()

	t.Run("a run exceeding its timeout is marked timed_out", func(t *testing.T) {
		h := newHarness(t)
		h.fake.WhenArgsContainBlock("slow.sh", 30*time.Second)

		id, err := h.engine.Submit(ctx, Spec{
			Kind: "test", Script: h.script("slow.sh"), Timeout: 200 * time.Millisecond,
		})
		if err != nil {
			t.Fatalf("Submit: %v", err)
		}

		rec := h.waitForStatus(id, store.StatusTimedOut)
		// A timeout must be distinguishable from a cancellation and from a
		// script that failed on its own.
		if !strings.Contains(rec.Error, "timed out") {
			t.Errorf("error = %q, want it to mention the timeout", rec.Error)
		}
		if rec.ExitCode != nil {
			t.Errorf("exit code = %d; a timed-out run has no meaningful exit code", *rec.ExitCode)
		}
		if !strings.Contains(h.logText(id), "timed out") {
			t.Errorf("the transcript does not explain the timeout:\n%s", h.logText(id))
		}
	})

	t.Run("falls back to the per-kind default", func(t *testing.T) {
		h := newHarness(t, WithTimeouts(map[string]time.Duration{"quick.kind": 150 * time.Millisecond}))
		h.fake.WhenArgsContainBlock("slow.sh", 30*time.Second)

		id, err := h.engine.Submit(ctx, Spec{Kind: "quick.kind", Script: h.script("slow.sh")})
		if err != nil {
			t.Fatalf("Submit: %v", err)
		}
		h.waitForStatus(id, store.StatusTimedOut)
	})

	t.Run("timeoutFor resolves the right duration", func(t *testing.T) {
		e := &Engine{timeouts: map[string]time.Duration{"known": time.Minute}}
		tests := []struct {
			kind string
			want time.Duration
		}{
			{"known", time.Minute},
			{"unknown", DefaultTimeout},
			{"", DefaultTimeout},
		}
		for _, tt := range tests {
			if got := e.timeoutFor(tt.kind); got != tt.want {
				t.Errorf("timeoutFor(%q) = %v, want %v", tt.kind, got, tt.want)
			}
		}
	})

	t.Run("every shipped kind has a sane default", func(t *testing.T) {
		for kind, d := range DefaultTimeouts {
			if d <= 0 {
				t.Errorf("%s: timeout %v must be positive", kind, d)
			}
			if d > time.Hour {
				t.Errorf("%s: timeout %v is longer than any laptop operation should take", kind, d)
			}
		}
	})
}

func TestSteps(t *testing.T) {
	ctx := context.Background()

	t.Run("parses step markers into a timeline", func(t *testing.T) {
		h := newHarness(t)
		h.fake.WhenArgsContain("install.sh", strings.Join([]string{
			"starting",
			StepMarker + "install-prometheus",
			"prometheus output",
			StepMarker + "install-grafana",
			"grafana output",
			"",
		}, "\n"), 0)

		id, err := h.engine.Submit(ctx, Spec{Kind: "test", Script: h.script("install.sh")})
		if err != nil {
			t.Fatalf("Submit: %v", err)
		}
		h.waitForStatus(id, store.StatusSucceeded)

		steps, err := h.store.ListSteps(ctx, id)
		if err != nil {
			t.Fatalf("ListSteps: %v", err)
		}
		if len(steps) != 2 {
			t.Fatalf("got %d steps, want 2: %+v", len(steps), steps)
		}
		if steps[0].Name != "install-prometheus" || steps[1].Name != "install-grafana" {
			t.Errorf("step names = %q, %q", steps[0].Name, steps[1].Name)
		}
		if steps[0].Status != store.StepSucceeded {
			t.Errorf("first step = %q, want succeeded", steps[0].Status)
		}
		if steps[1].Status != store.StepSucceeded {
			t.Errorf("last step = %q, want succeeded once the run ended well", steps[1].Status)
		}
	})

	t.Run("a failing run marks its open step failed", func(t *testing.T) {
		// So a failure report can say precisely where it died.
		h := newHarness(t)
		h.fake.WhenArgsContain("install.sh", StepMarker+"install-kafka\nworking\n", 2)

		id, err := h.engine.Submit(ctx, Spec{Kind: "test", Script: h.script("install.sh")})
		if err != nil {
			t.Fatalf("Submit: %v", err)
		}
		h.waitForStatus(id, store.StatusFailed)

		steps, _ := h.store.ListSteps(ctx, id)
		if len(steps) != 1 {
			t.Fatalf("got %d steps, want 1", len(steps))
		}
		if steps[0].Status != store.StepFailed {
			t.Errorf("step status = %q, want failed", steps[0].Status)
		}
		if steps[0].Name != "install-kafka" {
			t.Errorf("step name = %q, want install-kafka", steps[0].Name)
		}
	})

	t.Run("marker lines still appear in the transcript", func(t *testing.T) {
		h := newHarness(t)
		h.fake.WhenArgsContain("x.sh", StepMarker+"a-step\n", 0)

		id, err := h.engine.Submit(ctx, Spec{Kind: "test", Script: h.script("x.sh")})
		if err != nil {
			t.Fatalf("Submit: %v", err)
		}
		h.waitForStatus(id, store.StatusSucceeded)

		if !strings.Contains(h.logText(id), "a-step") {
			t.Errorf("the marker line was swallowed:\n%s", h.logText(id))
		}
	})
}

func TestParseStepMarker(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		want  string
		found bool
	}{
		{"plain marker", StepMarker + "install", "install", true},
		{"leading whitespace", "  " + StepMarker + "install", "install", true},
		{"trailing whitespace", StepMarker + "install  ", "install", true},
		{"hyphenated name", StepMarker + "install-prometheus-stack", "install-prometheus-stack", true},
		{"empty name", StepMarker, "", false},
		{"whitespace-only name", StepMarker + "   ", "", false},
		{"not a marker", "regular output", "", false},
		{"marker mid-line is not a marker", "echo " + StepMarker + "x", "", false},
		{"empty line", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := parseStepMarker(tt.line)
			if found != tt.found || got != tt.want {
				t.Errorf("parseStepMarker(%q) = %q, %v; want %q, %v", tt.line, got, found, tt.want, tt.found)
			}
		})
	}
}

// TestRecoveryOnStart covers the exit criterion about killing the server
// mid-run: on restart the run shows cancelled, its partial log is intact, and
// its lock is released.
func TestRecoveryOnStart(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "x.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	resolver, _ := toolchain.NewResolver(root)

	// Simulate a process that died with a run in flight.
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := st.CreateRun(ctx, store.Run{ID: "orphan", Kind: "test", LockKey: "lab:default"}); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if err := st.StartRun(ctx, "orphan", time.Now()); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if _, err := st.AppendLogs(ctx, "orphan", []store.LogLine{{Text: "got this far"}}); err != nil {
		t.Fatalf("AppendLogs: %v", err)
	}
	if _, err := st.StartStep(ctx, "orphan", "half-done", time.Now()); err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	st.Close()

	// Restart.
	st2, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st2.Close()

	engine, err := New(st2, toolchain.NewFake(), resolver)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := engine.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = engine.Shutdown(shutdownCtx)
	}()

	rec, err := st2.GetRun(ctx, "orphan")
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if rec.Status != store.StatusCancelled {
		t.Errorf("status = %q, want cancelled", rec.Status)
	}
	if !strings.Contains(rec.Error, "interrupted") {
		t.Errorf("error = %q, want it to explain the interruption", rec.Error)
	}

	lines, err := st2.ReadLogs(ctx, "orphan", 0, 100)
	if err != nil {
		t.Fatalf("ReadLogs: %v", err)
	}
	var text strings.Builder
	for _, l := range lines {
		text.WriteString(l.Text + "\n")
	}
	if !strings.Contains(text.String(), "got this far") {
		t.Error("the partial log was lost across the restart")
	}
	if !strings.Contains(text.String(), "interrupted") {
		t.Errorf("the transcript does not explain the interruption:\n%s", text.String())
	}

	steps, _ := st2.ListSteps(ctx, "orphan")
	if len(steps) != 1 || steps[0].Status != store.StepFailed {
		t.Errorf("the open step was not closed: %+v", steps)
	}

	// The lock must be free, or nothing can ever run against that lab again.
	if _, err := engine.Submit(ctx, Spec{Kind: "test", LockKey: "lab:default", Script: "x.sh"}); err != nil {
		t.Errorf("the recovered run's lock was not released: %v", err)
	}
}

func TestShutdown(t *testing.T) {
	ctx := context.Background()

	t.Run("cancels in-flight runs and waits", func(t *testing.T) {
		h := newHarness(t)
		h.fake.WhenArgsContainBlock("slow.sh", 30*time.Second)

		id, err := h.engine.Submit(ctx, Spec{Kind: "test", Script: h.script("slow.sh")})
		if err != nil {
			t.Fatalf("Submit: %v", err)
		}
		h.waitForStatus(id, store.StatusRunning)

		shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		start := time.Now()
		if err := h.engine.Shutdown(shutdownCtx); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Errorf("shutdown took %v; it must not wait out the whole run", elapsed)
		}

		rec, err := h.store.GetRun(ctx, id)
		if err != nil {
			t.Fatalf("GetRun: %v", err)
		}
		if !rec.Status.Terminal() {
			t.Errorf("status = %q; shutdown must leave no run in flight", rec.Status)
		}
	})

	t.Run("is idempotent", func(t *testing.T) {
		h := newHarness(t)
		for range 2 {
			if err := h.engine.Shutdown(ctx); err != nil {
				t.Fatalf("Shutdown: %v", err)
			}
		}
	})

	t.Run("closes subscriber channels", func(t *testing.T) {
		h := newHarness(t)
		events, unsubscribe := h.engine.Subscribe("")
		defer unsubscribe()

		if err := h.engine.Shutdown(ctx); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}

		// Drain, then confirm the channel is closed rather than hanging.
		deadline := time.After(2 * time.Second)
		for {
			select {
			case _, ok := <-events:
				if !ok {
					return
				}
			case <-deadline:
				t.Fatal("the subscriber channel was never closed")
			}
		}
	})
}

func TestQueueFull(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, WithWorkers(1), WithQueueSize(1))
	h.fake.WhenArgsContainBlock("slow.sh", 30*time.Second)

	script := h.script("slow.sh")
	// One executing, one queued, then saturation.
	var lastErr error
	for range 12 {
		if _, err := h.engine.Submit(ctx, Spec{Kind: "test", Script: script}); err != nil {
			lastErr = err
			break
		}
	}
	if !errors.Is(lastErr, ErrQueueFull) {
		t.Fatalf("error = %v, want ErrQueueFull", lastErr)
	}
}

func TestSubscribe(t *testing.T) {
	ctx := context.Background()

	t.Run("delivers status and log events for a run", func(t *testing.T) {
		h := newHarness(t)
		h.fake.WhenArgsContain("x.sh", "hello\n", 0)

		events, unsubscribe := h.engine.Subscribe("")
		defer unsubscribe()

		id, err := h.engine.Submit(ctx, Spec{Kind: "test", Script: h.script("x.sh")})
		if err != nil {
			t.Fatalf("Submit: %v", err)
		}
		h.waitForStatus(id, store.StatusSucceeded)

		seen := map[EventType]bool{}
		deadline := time.After(2 * time.Second)
	collect:
		for {
			select {
			case ev, ok := <-events:
				if !ok {
					break collect
				}
				seen[ev.Type] = true
				if seen[EventLog] && seen[EventStatus] {
					break collect
				}
			case <-deadline:
				break collect
			}
		}
		if !seen[EventStatus] {
			t.Error("no status event was delivered")
		}
		if !seen[EventLog] {
			t.Error("no log event was delivered")
		}
	})

	t.Run("filters to one run", func(t *testing.T) {
		h := newHarness(t)
		wanted, err := h.engine.Submit(ctx, Spec{Kind: "test", Script: h.script("a.sh")})
		if err != nil {
			t.Fatalf("Submit: %v", err)
		}
		h.waitForStatus(wanted, store.StatusSucceeded)

		events, unsubscribe := h.engine.Subscribe(wanted)
		defer unsubscribe()

		other, err := h.engine.Submit(ctx, Spec{Kind: "test", Script: h.script("b.sh")})
		if err != nil {
			t.Fatalf("Submit other: %v", err)
		}
		h.waitForStatus(other, store.StatusSucceeded)

		deadline := time.After(500 * time.Millisecond)
		for {
			select {
			case ev, ok := <-events:
				if !ok {
					return
				}
				if ev.RunID != wanted {
					t.Fatalf("received an event for %q on a stream filtered to %q", ev.RunID, wanted)
				}
			case <-deadline:
				return
			}
		}
	})

	t.Run("unsubscribing twice is safe", func(t *testing.T) {
		h := newHarness(t)
		_, unsubscribe := h.engine.Subscribe("")
		unsubscribe()
		unsubscribe()
	})

	t.Run("a slow subscriber never stalls the engine", func(t *testing.T) {
		// Events are notifications, not data, so dropping one costs latency,
		// not correctness — the consumer reads lines from the store by cursor.
		h := newHarness(t)
		_, unsubscribe := h.engine.Subscribe("") // never drained
		defer unsubscribe()

		var lines []string
		for i := range 500 {
			lines = append(lines, fmt.Sprintf("line-%d", i))
		}
		h.fake.WhenArgsContain("chatty.sh", strings.Join(lines, "\n")+"\n", 0)

		id, err := h.engine.Submit(ctx, Spec{Kind: "test", Script: h.script("chatty.sh")})
		if err != nil {
			t.Fatalf("Submit: %v", err)
		}
		h.waitForStatus(id, store.StatusSucceeded)

		// Every line must still be durable.
		got, err := h.store.ReadLogs(ctx, id, 0, 1000)
		if err != nil {
			t.Fatalf("ReadLogs: %v", err)
		}
		if len(got) != len(lines) {
			t.Errorf("persisted %d lines, want %d — a slow subscriber cost us data", len(got), len(lines))
		}
	})
}
