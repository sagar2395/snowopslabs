// SPDX-License-Identifier: Apache-2.0

package incident

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sagar2395/snowopslabs/internal/run"
	"github.com/sagar2395/snowopslabs/internal/store"
	"github.com/sagar2395/snowopslabs/internal/toolchain"
)

type harness struct {
	t   *testing.T
	svc *Service
	eng *run.Engine
	st  *store.Store
	f   *toolchain.Fake
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
	for _, name := range []string{"oom-kill", "crashloop-bad-config"} {
		for _, script := range []string{"inject.sh", "resolve.sh"} {
			writeScript(t, filepath.Join(root, "incidents", name, script))
		}
	}
	resolver, err := toolchain.NewResolver(root)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	f := toolchain.NewFake()
	eng, err := run.New(st, f, resolver, run.WithWorkers(2))
	if err != nil {
		t.Fatalf("run.New: %v", err)
	}
	if err := eng.Start(ctx); err != nil {
		t.Fatalf("engine.Start: %v", err)
	}
	t.Cleanup(func() {
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = eng.Shutdown(sctx)
	})

	svc, err := New(eng, st, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return &harness{t: t, svc: svc, eng: eng, st: st, f: f}
}

func writeScript(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte("#!/usr/bin/env bash\n"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func (h *harness) waitFor(id string, want store.Status) store.Run {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var last store.Run
	for time.Now().Before(deadline) {
		if rec, err := h.st.GetRun(context.Background(), id); err == nil {
			last = rec
			if rec.Status == want {
				return rec
			}
			if rec.Status.Terminal() && want.Terminal() && rec.Status != want {
				h.t.Fatalf("run %s reached %q, want %q (err %q)", id, rec.Status, want, rec.Error)
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.t.Fatalf("run %s did not reach %q in 10s (last %q)", id, want, last.Status)
	return last
}

var oomTarget = Target{Namespace: "echo-server", Workload: "echo-server"}

// --- submission -------------------------------------------------------------

func TestInject_SubmitsLockedRunWithTargetEnv(t *testing.T) {
	h := newHarness(t)
	id, err := h.svc.Inject(context.Background(), "oom-kill", oomTarget)
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	rec := h.waitFor(id, store.StatusSucceeded)
	if rec.Kind != KindInject {
		t.Errorf("Kind = %q, want %q", rec.Kind, KindInject)
	}
	if rec.LockKey != LockKey {
		t.Errorf("LockKey = %q, want %q", rec.LockKey, LockKey)
	}
	if rec.Target != "oom-kill" {
		t.Errorf("Target = %q", rec.Target)
	}
	if !h.f.Called("incidents/oom-kill/inject.sh") {
		t.Errorf("inject.sh not executed; calls: %v", h.f.CallStrings())
	}
	// The fault's target must reach the script's environment (TARGET_*). Env is
	// on the recorded Command, not its rendered argv, so inspect it directly.
	calls := h.f.Calls()
	if len(calls) == 0 {
		t.Fatal("no command was recorded")
	}
	env := calls[0].Env
	if env["TARGET_NAMESPACE"] != "echo-server" || env["TARGET_WORKLOAD"] != "echo-server" {
		t.Errorf("target env not passed to inject.sh: %v", env)
	}
}

func TestResolve_SubmitsResolveKind(t *testing.T) {
	h := newHarness(t)
	id, err := h.svc.Resolve(context.Background(), "oom-kill", oomTarget)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	rec := h.waitFor(id, store.StatusSucceeded)
	if rec.Kind != KindResolve {
		t.Errorf("Kind = %q, want %q", rec.Kind, KindResolve)
	}
}

func TestInject_InvalidNameRejected(t *testing.T) {
	h := newHarness(t)
	if _, err := h.svc.Inject(context.Background(), "../evil", oomTarget); err == nil {
		t.Fatal("expected an error for an invalid fault name")
	}
	if h.f.CallCount("") != 0 {
		t.Errorf("nothing should have run; calls: %v", h.f.CallStrings())
	}
}

func TestInject_UnknownFaultScriptFails(t *testing.T) {
	h := newHarness(t)
	if _, err := h.svc.Inject(context.Background(), "does-not-exist", oomTarget); err == nil {
		t.Fatal("expected an error for a fault with no inject.sh")
	}
}

// --- concurrency ------------------------------------------------------------

func TestInject_OnlyOneIncidentAtATime(t *testing.T) {
	h := newHarness(t)
	h.f.WhenArgsContainBlock("inject.sh", 3*time.Second)

	first, err := h.svc.Inject(context.Background(), "oom-kill", oomTarget)
	if err != nil {
		t.Fatalf("first inject: %v", err)
	}
	h.waitFor(first, store.StatusRunning)

	// A different fault, or a resolve, is refused while one is in flight.
	_, err = h.svc.Inject(context.Background(), "crashloop-bad-config", Target{Namespace: "go-api", Workload: "go-api"})
	var conflict *run.LockConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("second inject err = %v, want *run.LockConflictError", err)
	}
}

// --- cancellation -----------------------------------------------------------

func TestCancel_TerminatesAndFreesTheIncidentLock(t *testing.T) {
	h := newHarness(t)
	h.f.WhenArgsContainBlock("inject.sh", 30*time.Second)

	id, err := h.svc.Inject(context.Background(), "oom-kill", oomTarget)
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	h.waitFor(id, store.StatusRunning)

	if err := h.svc.Cancel(context.Background(), id); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	h.waitFor(id, store.StatusCancelled)

	if _, held, err := h.st.ActiveRunForLock(context.Background(), LockKey); err != nil || held {
		t.Fatalf("incident lock still held after cancel (held=%v, err=%v)", held, err)
	}
}

// --- status -----------------------------------------------------------------

func TestStatus_NoneWhenNoHistory(t *testing.T) {
	h := newHarness(t)
	st, err := h.svc.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.State != StateNone {
		t.Errorf("State = %q, want none", st.State)
	}
}

func TestStatus_InjectedThenResolved(t *testing.T) {
	h := newHarness(t)
	inj, _ := h.svc.Inject(context.Background(), "oom-kill", oomTarget)
	h.waitFor(inj, store.StatusSucceeded)

	st, _ := h.svc.Status(context.Background())
	if st.State != StateInjected || st.Fault != "oom-kill" || st.RunID != inj {
		t.Fatalf("after inject Status = %+v, want injected oom-kill run %s", st, inj)
	}

	res, _ := h.svc.Resolve(context.Background(), "oom-kill", oomTarget)
	h.waitFor(res, store.StatusSucceeded)

	st, _ = h.svc.Status(context.Background())
	if st.State != StateResolved || st.RunID != res {
		t.Fatalf("after resolve Status = %+v, want resolved run %s", st, res)
	}
}

func TestStatus_InjectingWhileRunning(t *testing.T) {
	h := newHarness(t)
	h.f.WhenArgsContainBlock("inject.sh", 2*time.Second)
	id, _ := h.svc.Inject(context.Background(), "oom-kill", oomTarget)
	h.waitFor(id, store.StatusRunning)

	st, _ := h.svc.Status(context.Background())
	if st.State != StateInjecting {
		t.Errorf("State = %q, want injecting", st.State)
	}
}

func TestStatus_ErrorAfterFailedInject(t *testing.T) {
	h := newHarness(t)
	h.f.WhenArgsContain("inject.sh", "", 1)
	id, _ := h.svc.Inject(context.Background(), "oom-kill", oomTarget)
	h.waitFor(id, store.StatusFailed)

	st, _ := h.svc.Status(context.Background())
	if st.State != StateError {
		t.Errorf("State = %q, want error", st.State)
	}
}

// --- construction -----------------------------------------------------------

func TestNew_RequiresEngineAndStore(t *testing.T) {
	if _, err := New(nil, nil); err == nil {
		t.Fatal("expected an error when engine and store are nil")
	}
}
