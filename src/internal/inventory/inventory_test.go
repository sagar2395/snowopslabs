// SPDX-License-Identifier: Apache-2.0

package inventory

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sagar2395/snowopslabs/internal/run"
	platsvc "github.com/sagar2395/snowopslabs/internal/service/platform"
	"github.com/sagar2395/snowopslabs/internal/store"
	"github.com/sagar2395/snowopslabs/internal/toolchain"
)

// harness wires an engine with the inventory recorder registered as a finish
// hook, plus a platform service, all over a Fake runner and a temp store — so a
// real install/uninstall run drives the real inventory, hermetically.
type harness struct {
	t   *testing.T
	st  *store.Store
	eng *run.Engine
	svc *platsvc.Service
	rec *Recorder
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()

	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	root := t.TempDir()
	for _, script := range []string{"install.sh", "uninstall.sh"} {
		p := filepath.Join(root, "platform", "ingress", "traefik", script)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("#!/usr/bin/env bash\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	resolver, err := toolchain.NewResolver(root)
	if err != nil {
		t.Fatal(err)
	}

	rec := NewRecorder(st)
	eng, err := run.New(st, toolchain.NewFake(), resolver,
		run.WithWorkers(2), run.WithFinishHook(rec.RunFinished))
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

	svc, err := platsvc.New(eng, st)
	if err != nil {
		t.Fatal(err)
	}
	return &harness{t: t, st: st, eng: eng, svc: svc, rec: rec}
}

func (h *harness) waitFor(id string, want store.Status) {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if rec, err := h.st.GetRun(context.Background(), id); err == nil && rec.Status == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.t.Fatalf("run %s did not reach %q", id, want)
}

func TestRecorder_InstallThenUninstallTracksInventory(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Install → the component appears as installed, pointing at its install run.
	id, err := h.svc.Install(ctx, "ingress", "traefik")
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	h.waitFor(id, store.StatusSucceeded)
	// Shutdown flushes the hook; but we assert without shutting down, so poll.
	if !waitFor(func() bool {
		got, gerr := h.st.GetComponent(ctx, "platform:ingress/traefik")
		return gerr == nil && got.Status == store.ComponentInstalled && got.InstallRun == id
	}) {
		got, _ := h.st.GetComponent(ctx, "platform:ingress/traefik")
		t.Fatalf("component not recorded installed after install run: %+v", got)
	}

	installed, err := h.rec.InstalledPlatform(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(installed) != 1 || installed[0].Ref != "ingress/traefik" {
		t.Fatalf("InstalledPlatform = %+v, want ingress/traefik", installed)
	}

	// Uninstall → the row is marked removed (kept as history).
	uid, err := h.svc.Uninstall(ctx, "ingress", "traefik")
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	h.waitFor(uid, store.StatusSucceeded)
	if !waitFor(func() bool {
		got, gerr := h.st.GetComponent(ctx, "platform:ingress/traefik")
		return gerr == nil && got.Status == store.ComponentRemoved && got.RemoveRun == uid
	}) {
		got, _ := h.st.GetComponent(ctx, "platform:ingress/traefik")
		t.Fatalf("component not marked removed after uninstall: %+v", got)
	}
	if installed, _ := h.rec.InstalledPlatform(ctx); len(installed) != 0 {
		t.Fatalf("InstalledPlatform after uninstall = %+v, want empty", installed)
	}
}

func TestRecorder_UnrecordedInstallRecordsNothing(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// An unknown component fails at the resolver before any run is created, so
	// nothing reaches the finish hook and the inventory stays empty.
	if _, err := h.svc.Install(ctx, "mesh", "istio"); err == nil {
		t.Fatal("expected the unknown-component install to fail")
	}
	if _, err := h.st.GetComponent(ctx, "platform:mesh/istio"); err == nil {
		t.Fatal("a failed install must not record a component")
	}
}

func waitFor(cond func() bool) bool {
	for i := 0; i < 200; i++ {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}
