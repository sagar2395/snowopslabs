// SPDX-License-Identifier: Apache-2.0

package scenario

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sagar2395/snowopslabs/internal/run"
	scn "github.com/sagar2395/snowopslabs/internal/scenario"
	"github.com/sagar2395/snowopslabs/internal/store"
	"github.com/sagar2395/snowopslabs/internal/toolchain"
	"github.com/sagar2395/snowopslabs/pkg/checks"
)

type harness struct {
	t    *testing.T
	svc  *Service
	eng  *run.Engine
	st   *store.Store
	f    *toolchain.Fake
	root string
}

const demoScenario = `apiVersion: scenario.snowops.net/v2
name: demo
displayName: Demo Scenario
description: a hermetic demo
components:
  - name: setup
    type: script
    script: setup.sh
    namespace: demo-ns
  - name: web
    type: helm
    chart: nginx
    namespace: demo-ns
checks:
  - name: web-ready
    type: kubectl
    resource: deployment/web
    namespace: demo-ns
`

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()

	root := t.TempDir()
	// A scenario with a single script component.
	scDir := filepath.Join(root, "scenarios", "demo")
	if err := os.MkdirAll(scDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(scDir, "scenario.yaml"), demoScenario)
	writeFile(t, filepath.Join(scDir, "setup.sh"), "#!/usr/bin/env bash\necho setting up\n")

	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	resolver, err := toolchain.NewResolver(root)
	if err != nil {
		t.Fatal(err)
	}
	f := toolchain.NewFake()
	eng, err := run.New(st, f, resolver, run.WithWorkers(2))
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = eng.Shutdown(sctx)
	})

	scenes := scn.NewEngine(root, "k3d.local", "k3d")
	svc, err := New(eng, st, scenes, f, root)
	if err != nil {
		t.Fatal(err)
	}
	return &harness{t: t, svc: svc, eng: eng, st: st, f: f, root: root}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
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

func TestActivate_RecordsRunAndComponents(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	id, err := h.svc.Activate(ctx, "demo", false)
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	rec := h.waitFor(id, store.StatusSucceeded)
	if rec.Kind != KindActivate || rec.LockKey != "scenario:demo" || rec.Target != "demo" {
		t.Errorf("record = %+v", rec)
	}
	// The activation ran the component's script through the adapter.
	if !h.f.Called("scenarios/demo/setup.sh") {
		t.Errorf("component script not executed; calls: %v", h.f.CallStrings())
	}
	// The component was written to the inventory.
	comp, err := h.st.GetComponent(ctx, "scenario:demo/setup")
	if err != nil {
		t.Fatalf("component not recorded: %v", err)
	}
	if comp.Status != store.ComponentInstalled || comp.Owner != "demo" || comp.Ref != "setup" {
		t.Errorf("component = %+v", comp)
	}
	if comp.Namespace != "demo-ns" {
		t.Errorf("namespace = %q, want demo-ns", comp.Namespace)
	}
}

func TestActivate_UnknownScenarioRejected(t *testing.T) {
	h := newHarness(t)
	if _, err := h.svc.Activate(context.Background(), "ghost", false); err == nil {
		t.Fatal("expected an error for an unknown scenario")
	}
}

func TestActivate_InvalidNameRejected(t *testing.T) {
	h := newHarness(t)
	if _, err := h.svc.Activate(context.Background(), "../evil", false); err == nil {
		t.Fatal("expected an error for an invalid name")
	}
}

func TestActivate_FailedComponentFailsTheRun(t *testing.T) {
	h := newHarness(t)
	// Make the component's script fail: bash <script> returns non-zero.
	h.f.WhenArgsContainStderr("setup.sh", "", "boom\n", 1)

	id, err := h.svc.Activate(context.Background(), "demo", false)
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	rec := h.waitFor(id, store.StatusFailed)
	if rec.Status != store.StatusFailed {
		t.Errorf("status = %q, want failed", rec.Status)
	}
	// A failed activation must not leave the component recorded as installed.
	if _, err := h.st.GetComponent(context.Background(), "scenario:demo/setup"); err == nil {
		t.Error("a failed activation must not record its components")
	}
}

func TestDeactivate_MarksComponentsRemoved(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	act, _ := h.svc.Activate(ctx, "demo", false)
	h.waitFor(act, store.StatusSucceeded)

	deact, err := h.svc.Deactivate(ctx, "demo")
	if err != nil {
		t.Fatalf("Deactivate: %v", err)
	}
	rec := h.waitFor(deact, store.StatusSucceeded)
	if rec.Kind != KindDeactivate {
		t.Errorf("kind = %q, want %q", rec.Kind, KindDeactivate)
	}
	comp, err := h.st.GetComponent(ctx, "scenario:demo/setup")
	if err != nil {
		t.Fatal(err)
	}
	if comp.Status != store.ComponentRemoved {
		t.Errorf("component status = %q, want removed", comp.Status)
	}
}

func TestActivate_ConflictsPerScenario(t *testing.T) {
	h := newHarness(t)
	h.f.WhenArgsContainBlock("setup.sh", 3*time.Second)

	first, err := h.svc.Activate(context.Background(), "demo", false)
	if err != nil {
		t.Fatalf("first Activate: %v", err)
	}
	h.waitFor(first, store.StatusRunning)

	_, err = h.svc.Deactivate(context.Background(), "demo")
	var conflict *run.LockConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("second op err = %v, want *run.LockConflictError", err)
	}
}

func TestCancel_StopsActivation(t *testing.T) {
	h := newHarness(t)
	h.f.WhenArgsContainBlock("setup.sh", 30*time.Second)

	id, err := h.svc.Activate(context.Background(), "demo", false)
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	h.waitFor(id, store.StatusRunning)
	if err := h.svc.Cancel(context.Background(), id); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	h.waitFor(id, store.StatusCancelled)
	if _, held, err := h.st.ActiveRunForLock(context.Background(), "scenario:demo"); err != nil || held {
		t.Fatalf("lock still held after cancel (held=%v err=%v)", held, err)
	}
}

func TestStatus_Transitions(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	if st, _ := h.svc.Status(ctx, "demo"); st.State != StateInactive {
		t.Errorf("initial state = %q, want inactive", st.State)
	}
	act, _ := h.svc.Activate(ctx, "demo", false)
	h.waitFor(act, store.StatusSucceeded)
	if st, _ := h.svc.Status(ctx, "demo"); st.State != StateActive || st.RunID != act {
		t.Errorf("after activate = %+v, want active run %s", st, act)
	}
	deact, _ := h.svc.Deactivate(ctx, "demo")
	h.waitFor(deact, store.StatusSucceeded)
	if st, _ := h.svc.Status(ctx, "demo"); st.State != StateInactive {
		t.Errorf("after deactivate state = %q, want inactive", st.State)
	}
}

func TestActivate_RunsHelmComponentThroughAdapter(t *testing.T) {
	h := newHarness(t)
	id, err := h.svc.Activate(context.Background(), "demo", false)
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	h.waitFor(id, store.StatusSucceeded)
	// The helm component drives RunCommandStreamed → the toolchain runner.
	if !h.f.Called("helm") || !h.f.Called("upgrade") {
		t.Errorf("helm component not installed via the adapter; calls: %v", h.f.CallStrings())
	}
	if _, err := h.st.GetComponent(context.Background(), "scenario:demo/web"); err != nil {
		t.Errorf("helm component not recorded: %v", err)
	}
}

func TestVerify_InvalidNameRejected(t *testing.T) {
	h := newHarness(t)
	if _, err := h.svc.Verify(context.Background(), "../evil", &checks.Runner{}); err == nil {
		t.Fatal("expected an error for an invalid name")
	}
}

func TestNew_RequiresDeps(t *testing.T) {
	if _, err := New(nil, nil, nil, nil, ""); err == nil {
		t.Fatal("expected an error when dependencies are nil")
	}
}
