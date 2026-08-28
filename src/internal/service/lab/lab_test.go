// SPDX-License-Identifier: Apache-2.0

package lab

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

// harness wires a lab Service to a real (temp) store, a fake runner, and a
// content root holding stub runtime scripts. Every test is hermetic: no
// cluster, no network, no real binaries.
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
	for _, rt := range []string{"k3d", "kind"} {
		for _, script := range []string{"up.sh", "down.sh"} {
			writeScript(t, filepath.Join(root, "runtimes", rt, script))
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

	svc, err := New(eng, st, "snowops-test", opts...)
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

// --- submission -------------------------------------------------------------

func TestUp_SubmitsLockedRunAndPassesClusterArg(t *testing.T) {
	h := newHarness(t)
	id, err := h.svc.Up(context.Background(), "k3d")
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	rec := h.waitFor(id, store.StatusSucceeded)
	if rec.Kind != KindUp {
		t.Errorf("Kind = %q, want %q", rec.Kind, KindUp)
	}
	if rec.LockKey != LockKey {
		t.Errorf("LockKey = %q, want %q", rec.LockKey, LockKey)
	}
	if rec.Target != "k3d" {
		t.Errorf("Target = %q, want k3d", rec.Target)
	}
	if len(rec.Argv) != 1 || rec.Argv[0] != "snowops-test" {
		t.Errorf("Argv = %v, want [snowops-test]", rec.Argv)
	}
	if !h.f.Called("runtimes/k3d/up.sh") {
		t.Errorf("up.sh was not executed; calls: %v", h.f.CallStrings())
	}
}

func TestDown_SubmitsDownKind(t *testing.T) {
	h := newHarness(t)
	id, err := h.svc.Down(context.Background(), "k3d")
	if err != nil {
		t.Fatalf("Down: %v", err)
	}
	rec := h.waitFor(id, store.StatusSucceeded)
	if rec.Kind != KindDown {
		t.Errorf("Kind = %q, want %q", rec.Kind, KindDown)
	}
}

func TestUp_InvalidRuntimeRejected(t *testing.T) {
	h := newHarness(t)
	if _, err := h.svc.Up(context.Background(), "../evil"); err == nil {
		t.Fatal("expected an error for an invalid runtime name")
	}
	if h.f.CallCount("") != 0 {
		t.Errorf("nothing should have run; calls: %v", h.f.CallStrings())
	}
}

func TestUp_UnknownRuntimeScriptFails(t *testing.T) {
	h := newHarness(t)
	// A syntactically valid name with no script on disk: the engine's resolver
	// refuses it up front, before any run record is created.
	if _, err := h.svc.Up(context.Background(), "nope"); err == nil {
		t.Fatal("expected an error for a runtime with no up.sh")
	}
}

// --- concurrency ------------------------------------------------------------

func TestUp_ConflictsWithInFlightOperation(t *testing.T) {
	h := newHarness(t)
	h.f.WhenArgsContainBlock("up.sh", 2*time.Second)

	first, err := h.svc.Up(context.Background(), "k3d")
	if err != nil {
		t.Fatalf("first Up: %v", err)
	}
	h.waitFor(first, store.StatusRunning)

	// A second lab operation while one is in flight is refused, not queued.
	_, err = h.svc.Down(context.Background(), "k3d")
	var conflict *run.LockConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("second op err = %v, want *run.LockConflictError", err)
	}
	if conflict.Holder.ID != first {
		t.Errorf("conflict names run %q, want %q", conflict.Holder.ID, first)
	}
}

// --- cancellation -----------------------------------------------------------

func TestCancel_LeavesNoInFlightRunAndReleasesLock(t *testing.T) {
	h := newHarness(t)
	// A long-blocking up we will cancel mid-flight. The engine cancels the whole
	// process group (ADR-0003); with the Fake, cancellation surfaces as the run
	// reaching a terminal cancelled state rather than an orphaned child.
	h.f.WhenArgsContainBlock("up.sh", 30*time.Second)

	id, err := h.svc.Up(context.Background(), "k3d")
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	h.waitFor(id, store.StatusRunning)

	if err := h.svc.Cancel(context.Background(), id); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	h.waitFor(id, store.StatusCancelled)

	// The lock must be freed: a fresh operation is accepted, not refused.
	h.f.Reset()
	if _, held, err := h.st.ActiveRunForLock(context.Background(), LockKey); err != nil || held {
		t.Fatalf("lock still held after cancel (held=%v, err=%v)", held, err)
	}
}

// --- status -----------------------------------------------------------------

func TestStatus_UnknownWhenNoHistory(t *testing.T) {
	h := newHarness(t)
	st, err := h.svc.Status(context.Background(), false)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.State != StateUnknown {
		t.Errorf("State = %q, want unknown", st.State)
	}
}

func TestStatus_UpAfterSuccessfulUp(t *testing.T) {
	h := newHarness(t)
	id, _ := h.svc.Up(context.Background(), "k3d")
	h.waitFor(id, store.StatusSucceeded)

	st, err := h.svc.Status(context.Background(), false)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.State != StateUp {
		t.Errorf("State = %q, want up", st.State)
	}
	if st.Runtime != "k3d" || st.RunID != id {
		t.Errorf("Status = %+v, want runtime k3d run %s", st, id)
	}
}

func TestStatus_DownIsMostRecentWins(t *testing.T) {
	h := newHarness(t)
	up, _ := h.svc.Up(context.Background(), "k3d")
	h.waitFor(up, store.StatusSucceeded)
	down, _ := h.svc.Down(context.Background(), "k3d")
	h.waitFor(down, store.StatusSucceeded)

	st, _ := h.svc.Status(context.Background(), false)
	if st.State != StateDown {
		t.Errorf("State = %q, want down", st.State)
	}
	if st.RunID != down {
		t.Errorf("RunID = %q, want the down run %q", st.RunID, down)
	}
}

func TestStatus_ProvisioningWhileUpRunning(t *testing.T) {
	h := newHarness(t)
	h.f.WhenArgsContainBlock("up.sh", 2*time.Second)
	id, _ := h.svc.Up(context.Background(), "k3d")
	h.waitFor(id, store.StatusRunning)

	st, _ := h.svc.Status(context.Background(), false)
	if st.State != StateProvisioning {
		t.Errorf("State = %q, want provisioning", st.State)
	}
	if st.RunID != id {
		t.Errorf("RunID = %q, want %q", st.RunID, id)
	}
}

func TestStatus_ErrorAfterFailedUp(t *testing.T) {
	h := newHarness(t)
	h.f.WhenArgsContain("up.sh", "", 1) // exit non-zero
	id, _ := h.svc.Up(context.Background(), "k3d")
	h.waitFor(id, store.StatusFailed)

	st, _ := h.svc.Status(context.Background(), false)
	if st.State != StateError {
		t.Errorf("State = %q, want error", st.State)
	}
}

func TestStatus_LiveProbeAttached(t *testing.T) {
	probed := false
	prober := func(ctx context.Context) (Liveness, error) {
		probed = true
		return Liveness{Reachable: true, Context: "k3d-snowops-test", Detail: "v1.33"}, nil
	}
	h := newHarness(t, WithProber(prober))

	st, err := h.svc.Status(context.Background(), true)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !probed {
		t.Fatal("prober was not called for a live status")
	}
	if st.Live == nil || !st.Live.Reachable {
		t.Fatalf("Live = %+v, want a reachable probe", st.Live)
	}
}

func TestStatus_LiveProbeErrorIsReportedNotFatal(t *testing.T) {
	prober := func(ctx context.Context) (Liveness, error) {
		return Liveness{}, errors.New("cluster unreachable")
	}
	h := newHarness(t, WithProber(prober))

	st, err := h.svc.Status(context.Background(), true)
	if err != nil {
		t.Fatalf("Status must not fail on a probe error: %v", err)
	}
	if st.Live == nil || st.Live.Reachable {
		t.Fatalf("Live = %+v, want an unreachable probe", st.Live)
	}
	if st.Live.Detail == "" {
		t.Error("expected the probe error to be reported in Detail")
	}
}

func TestStatus_FastFromStore(t *testing.T) {
	h := newHarness(t)
	id, _ := h.svc.Up(context.Background(), "k3d")
	h.waitFor(id, store.StatusSucceeded)

	start := time.Now()
	if _, err := h.svc.Status(context.Background(), false); err != nil {
		t.Fatalf("Status: %v", err)
	}
	// The store-backed status must be well under the 200ms budget (W3-T01 AC).
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Errorf("store-backed Status took %s, want < 200ms", elapsed)
	}
}

// --- construction -----------------------------------------------------------

func TestNew_RequiresEngineAndStore(t *testing.T) {
	if _, err := New(nil, nil, "c"); err == nil {
		t.Fatal("expected an error when engine and store are nil")
	}
}
