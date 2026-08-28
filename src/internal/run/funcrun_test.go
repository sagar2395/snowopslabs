// SPDX-License-Identifier: Apache-2.0

package run

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/sagar2395/snowopslabs/internal/store"
)

func TestFuncRun_SucceedsAndStreamsTranscript(t *testing.T) {
	h := newHarness(t)
	id, err := h.engine.Submit(context.Background(), Spec{
		Kind: "scenario.activate", Target: "demo", LockKey: "scenario:demo",
		Func: func(_ context.Context, out io.Writer) error {
			fmt.Fprintln(out, "[1/2] installing thing")
			fmt.Fprintln(out, "[2/2] done")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	rec := h.waitForStatus(id, store.StatusSucceeded)
	if rec.Kind != "scenario.activate" || rec.LockKey != "scenario:demo" {
		t.Errorf("record = %+v", rec)
	}
	if log := h.logText(id); !strings.Contains(log, "installing thing") || !strings.Contains(log, "done") {
		t.Errorf("func output not in transcript: %q", log)
	}
}

func TestFuncRun_ErrorBecomesFailed(t *testing.T) {
	h := newHarness(t)
	id, err := h.engine.Submit(context.Background(), Spec{
		Kind: "scenario.activate",
		Func: func(_ context.Context, out io.Writer) error {
			fmt.Fprintln(out, "installing…")
			return errors.New("component xyz failed to install")
		},
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	rec := h.waitForStatus(id, store.StatusFailed)
	if !strings.Contains(rec.Error, "component xyz failed to install") {
		t.Errorf("run error = %q, want the func's error", rec.Error)
	}
}

func TestFuncRun_HonoursCancellation(t *testing.T) {
	h := newHarness(t)
	started := make(chan struct{})
	id, err := h.engine.Submit(context.Background(), Spec{
		Kind: "scenario.activate",
		Func: func(ctx context.Context, out io.Writer) error {
			close(started)
			<-ctx.Done() // block until cancelled
			return ctx.Err()
		},
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	<-started
	h.waitForStatus(id, store.StatusRunning)
	if err := h.engine.Cancel(context.Background(), id); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	rec := h.waitForStatus(id, store.StatusCancelled)
	if rec.Status != store.StatusCancelled {
		t.Errorf("status = %q, want cancelled", rec.Status)
	}
}

func TestFuncRun_PanicIsContained(t *testing.T) {
	h := newHarness(t)
	id, err := h.engine.Submit(context.Background(), Spec{
		Kind: "scenario.activate",
		Func: func(_ context.Context, _ io.Writer) error {
			panic("boom")
		},
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	rec := h.waitForStatus(id, store.StatusFailed)
	if !strings.Contains(rec.Error, "panicked") {
		t.Errorf("run error = %q, want a contained panic", rec.Error)
	}
	// The engine must still be alive: a second run completes normally.
	id2, err := h.engine.Submit(context.Background(), Spec{
		Kind: "scenario.activate",
		Func: func(_ context.Context, _ io.Writer) error { return nil },
	})
	if err != nil {
		t.Fatalf("second Submit after panic: %v", err)
	}
	h.waitForStatus(id2, store.StatusSucceeded)
}

func TestSubmit_RejectsBothScriptAndFunc(t *testing.T) {
	h := newHarness(t)
	script := h.script("runtimes/k3d/up.sh")
	_, err := h.engine.Submit(context.Background(), Spec{
		Kind: "lab.up", Script: script,
		Func: func(context.Context, io.Writer) error { return nil },
	})
	if err == nil {
		t.Fatal("expected an error when both Script and Func are set")
	}
}

func TestSubmit_RejectsNeitherScriptNorFunc(t *testing.T) {
	h := newHarness(t)
	if _, err := h.engine.Submit(context.Background(), Spec{Kind: "lab.up"}); err == nil {
		t.Fatal("expected an error when neither Script nor Func is set")
	}
}

// A Func run must still time out if it ignores cancellation past its deadline.
func TestFuncRun_TimesOut(t *testing.T) {
	h := newHarness(t, WithTimeouts(map[string]time.Duration{"slow": 50 * time.Millisecond}))
	id, err := h.engine.Submit(context.Background(), Spec{
		Kind: "slow",
		Func: func(ctx context.Context, _ io.Writer) error {
			<-ctx.Done()
			return ctx.Err()
		},
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	rec := h.waitForStatus(id, store.StatusTimedOut)
	if rec.Status != store.StatusTimedOut {
		t.Errorf("status = %q, want timed_out", rec.Status)
	}
}
