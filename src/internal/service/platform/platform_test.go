// SPDX-License-Identifier: Apache-2.0

package platform

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

// harness wires a platform Service to a temp store, a fake runner, and a content
// root holding stub component scripts under platform/<category>/<provider>/.
type harness struct {
	t   *testing.T
	svc *Service
	eng *run.Engine
	st  *store.Store
	f   *toolchain.Fake
}

// components created in the scratch root: category -> provider.
var fixtureComponents = map[string]string{
	"ingress":            "traefik",
	"monitoring/metrics": "prometheus",
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
	for category, provider := range fixtureComponents {
		for _, script := range []string{"install.sh", "uninstall.sh", "status.sh"} {
			writeScript(t, filepath.Join(root, "platform", category, provider, script))
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

// --- submission -------------------------------------------------------------

func TestInstall_SubmitsLockedRun(t *testing.T) {
	h := newHarness(t)
	id, err := h.svc.Install(context.Background(), "monitoring/metrics", "prometheus")
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	rec := h.waitFor(id, store.StatusSucceeded)
	if rec.Kind != KindInstall {
		t.Errorf("Kind = %q, want %q", rec.Kind, KindInstall)
	}
	if rec.LockKey != "platform:monitoring/metrics/prometheus" {
		t.Errorf("LockKey = %q", rec.LockKey)
	}
	if rec.Target != "monitoring/metrics/prometheus" {
		t.Errorf("Target = %q", rec.Target)
	}
	if !h.f.Called("platform/monitoring/metrics/prometheus/install.sh") {
		t.Errorf("install.sh not executed; calls: %v", h.f.CallStrings())
	}
}

func TestUninstall_SubmitsUninstallKind(t *testing.T) {
	h := newHarness(t)
	id, err := h.svc.Uninstall(context.Background(), "ingress", "traefik")
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	rec := h.waitFor(id, store.StatusSucceeded)
	if rec.Kind != KindUninstall {
		t.Errorf("Kind = %q, want %q", rec.Kind, KindUninstall)
	}
}

func TestProbe_TakesNoLockAndRunsStatusScript(t *testing.T) {
	h := newHarness(t)
	id, err := h.svc.Probe(context.Background(), "ingress", "traefik")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	rec := h.waitFor(id, store.StatusSucceeded)
	if rec.Kind != KindStatus {
		t.Errorf("Kind = %q, want %q", rec.Kind, KindStatus)
	}
	if rec.LockKey != "" {
		t.Errorf("Probe run took a lock (%q); it must not", rec.LockKey)
	}
	if !h.f.Called("platform/ingress/traefik/status.sh") {
		t.Errorf("status.sh not executed; calls: %v", h.f.CallStrings())
	}
}

func TestInstall_InvalidComponentRejected(t *testing.T) {
	h := newHarness(t)
	for _, tc := range []struct{ cat, prov string }{
		{"../evil", "traefik"},
		{"ingress", "../evil"},
		{"", "traefik"},
	} {
		if _, err := h.svc.Install(context.Background(), tc.cat, tc.prov); err == nil {
			t.Errorf("Install(%q,%q) = nil error, want rejection", tc.cat, tc.prov)
		}
	}
	if h.f.CallCount("") != 0 {
		t.Errorf("nothing should have run; calls: %v", h.f.CallStrings())
	}
}

func TestInstall_UnknownComponentScriptFails(t *testing.T) {
	h := newHarness(t)
	// Valid tokens, no script on disk: the resolver refuses it before any record.
	if _, err := h.svc.Install(context.Background(), "mesh", "istio"); err == nil {
		t.Fatal("expected an error for a component with no install.sh")
	}
}

// --- concurrency ------------------------------------------------------------

func TestInstall_ConflictsPerComponentButNotAcrossComponents(t *testing.T) {
	h := newHarness(t)
	h.f.WhenArgsContainBlock("install.sh", 3*time.Second)

	first, err := h.svc.Install(context.Background(), "ingress", "traefik")
	if err != nil {
		t.Fatalf("first install: %v", err)
	}
	h.waitFor(first, store.StatusRunning)

	// Same component, opposite op → refused.
	_, err = h.svc.Uninstall(context.Background(), "ingress", "traefik")
	var conflict *run.LockConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("same-component op err = %v, want *run.LockConflictError", err)
	}

	// A different component installs concurrently — different lock key.
	other, err := h.svc.Install(context.Background(), "monitoring/metrics", "prometheus")
	if err != nil {
		t.Fatalf("different-component install should be accepted, got: %v", err)
	}
	h.waitFor(other, store.StatusRunning)
}

// --- cancellation -----------------------------------------------------------

func TestCancel_TerminatesAndFreesTheComponentLock(t *testing.T) {
	h := newHarness(t)
	h.f.WhenArgsContainBlock("install.sh", 30*time.Second)

	id, err := h.svc.Install(context.Background(), "ingress", "traefik")
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	h.waitFor(id, store.StatusRunning)

	if err := h.svc.Cancel(context.Background(), id); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	h.waitFor(id, store.StatusCancelled)

	if _, held, err := h.st.ActiveRunForLock(context.Background(), LockKey("ingress", "traefik")); err != nil || held {
		t.Fatalf("component lock still held after cancel (held=%v, err=%v)", held, err)
	}
}

// --- status -----------------------------------------------------------------

func TestStatus_UnknownWhenNoHistory(t *testing.T) {
	h := newHarness(t)
	st, err := h.svc.Status(context.Background(), "ingress", "traefik")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.State != StateUnknown {
		t.Errorf("State = %q, want unknown", st.State)
	}
	if st.Component != "ingress/traefik" {
		t.Errorf("Component = %q", st.Component)
	}
}

func TestStatus_InstalledAfterInstall(t *testing.T) {
	h := newHarness(t)
	id, _ := h.svc.Install(context.Background(), "ingress", "traefik")
	h.waitFor(id, store.StatusSucceeded)

	st, _ := h.svc.Status(context.Background(), "ingress", "traefik")
	if st.State != StateInstalled {
		t.Errorf("State = %q, want installed", st.State)
	}
	if st.RunID != id {
		t.Errorf("RunID = %q, want %q", st.RunID, id)
	}
}

func TestStatus_IsPerComponent(t *testing.T) {
	h := newHarness(t)
	// Install traefik; prometheus stays unknown — state must not leak across
	// components that share the same run Kind.
	id, _ := h.svc.Install(context.Background(), "ingress", "traefik")
	h.waitFor(id, store.StatusSucceeded)

	other, _ := h.svc.Status(context.Background(), "monitoring/metrics", "prometheus")
	if other.State != StateUnknown {
		t.Errorf("prometheus State = %q, want unknown (traefik's install must not leak)", other.State)
	}
}

func TestStatus_RemovedAfterUninstall(t *testing.T) {
	h := newHarness(t)
	up, _ := h.svc.Install(context.Background(), "ingress", "traefik")
	h.waitFor(up, store.StatusSucceeded)
	down, _ := h.svc.Uninstall(context.Background(), "ingress", "traefik")
	h.waitFor(down, store.StatusSucceeded)

	st, _ := h.svc.Status(context.Background(), "ingress", "traefik")
	if st.State != StateRemoved {
		t.Errorf("State = %q, want removed", st.State)
	}
	if st.RunID != down {
		t.Errorf("RunID = %q, want the uninstall run", st.RunID)
	}
}

func TestStatus_InstallingWhileRunning(t *testing.T) {
	h := newHarness(t)
	h.f.WhenArgsContainBlock("install.sh", 2*time.Second)
	id, _ := h.svc.Install(context.Background(), "ingress", "traefik")
	h.waitFor(id, store.StatusRunning)

	st, _ := h.svc.Status(context.Background(), "ingress", "traefik")
	if st.State != StateInstalling {
		t.Errorf("State = %q, want installing", st.State)
	}
}

func TestStatus_ErrorAfterFailedInstall(t *testing.T) {
	h := newHarness(t)
	h.f.WhenArgsContain("install.sh", "", 1)
	id, _ := h.svc.Install(context.Background(), "ingress", "traefik")
	h.waitFor(id, store.StatusFailed)

	st, _ := h.svc.Status(context.Background(), "ingress", "traefik")
	if st.State != StateError {
		t.Errorf("State = %q, want error", st.State)
	}
}

func TestStatus_ProbeDoesNotChangeInstallState(t *testing.T) {
	h := newHarness(t)
	up, _ := h.svc.Install(context.Background(), "ingress", "traefik")
	h.waitFor(up, store.StatusSucceeded)
	// A later probe run must not flip the derived state away from installed.
	pid, _ := h.svc.Probe(context.Background(), "ingress", "traefik")
	h.waitFor(pid, store.StatusSucceeded)

	st, _ := h.svc.Status(context.Background(), "ingress", "traefik")
	if st.State != StateInstalled {
		t.Errorf("State = %q, want installed (a status probe must not change it)", st.State)
	}
	if st.RunID != up {
		t.Errorf("RunID = %q, want the install run %q (not the probe)", st.RunID, up)
	}
}

// --- construction -----------------------------------------------------------

func TestNew_RequiresEngineAndStore(t *testing.T) {
	if _, err := New(nil, nil); err == nil {
		t.Fatal("expected an error when engine and store are nil")
	}
}

func TestComponentAndLockKey(t *testing.T) {
	if got := Component("ingress", "traefik"); got != "ingress/traefik" {
		t.Errorf("Component = %q", got)
	}
	if got := Component("", "solo"); got != "solo" {
		t.Errorf("Component empty-category = %q", got)
	}
	if got := LockKey("mesh", "istio"); got != "platform:mesh/istio" {
		t.Errorf("LockKey = %q", got)
	}
}
