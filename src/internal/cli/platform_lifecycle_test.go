// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/sagar2395/snowopslabs/internal/inventory"
	"github.com/sagar2395/snowopslabs/internal/run"
	platsvc "github.com/sagar2395/snowopslabs/internal/service/platform"
	"github.com/sagar2395/snowopslabs/internal/store"
	"github.com/sagar2395/snowopslabs/internal/toolchain"
)

// fakePlatformEnv points platformEngineFactory at a Fake-backed engine and a
// temp store with stub component scripts, for the duration of a test.
func fakePlatformEnv(t *testing.T) (*toolchain.Fake, *store.Store) {
	t.Helper()
	ctx := context.Background()

	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	root := t.TempDir()
	for _, comp := range []string{"ingress/traefik", "monitoring/metrics/prometheus"} {
		for _, script := range []string{"install.sh", "uninstall.sh", "status.sh"} {
			p := filepath.Join(root, "platform", filepath.FromSlash(comp), script)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte("#!/usr/bin/env bash\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	resolver, err := toolchain.NewResolver(root)
	if err != nil {
		t.Fatal(err)
	}
	f := toolchain.NewFake()
	// Register the inventory recorder just as production newRunEngine does, so
	// successful installs/uninstalls keep the component inventory in step.
	eng, err := run.New(st, f, resolver, run.WithWorkers(2),
		run.WithFinishHook(inventory.NewRecorder(st).RunFinished))
	if err != nil {
		t.Fatal(err)
	}

	prev := platformEngineFactory
	platformEngineFactory = func(ctx context.Context) (*platsvc.Service, *store.Store, *run.Engine, func(), error) {
		svc, serr := platsvc.New(eng, st)
		if serr != nil {
			return nil, nil, nil, nil, serr
		}
		return svc, st, eng, func() {}, nil
	}
	t.Cleanup(func() {
		platformEngineFactory = prev
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = eng.Shutdown(sctx)
		_ = st.Close()
	})
	return f, st
}

// dummyCmd returns a cobra.Command wired to the given context with its output
// captured, so the lifecycle helpers can be driven without the full root tree
// (which would need cfg + a platform registry).
func dummyCmd(buf *bytes.Buffer) *cobra.Command {
	c := &cobra.Command{}
	c.SetOut(buf)
	c.SetErr(buf)
	c.SetContext(context.Background())
	return c
}

func TestPlatformInstall_RecordsAndStreamsRun(t *testing.T) {
	f, st := fakePlatformEnv(t)
	f.WhenArgsContain("install.sh", "installing traefik...\ndone\n", 0)

	var buf bytes.Buffer
	err := runPlatformComponentOp(dummyCmd(&buf), "install", "ingress", "traefik",
		func(ctx context.Context, svc *platsvc.Service) (string, error) {
			return svc.Install(ctx, "ingress", "traefik")
		})
	if err != nil {
		t.Fatalf("install: %v\noutput: %s", err, buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "install ingress/traefik started (run ") {
		t.Errorf("missing start line: %q", out)
	}
	if !strings.Contains(out, "done") || !strings.Contains(out, "Succeeded") {
		t.Errorf("transcript/summary missing: %q", out)
	}
	runs, _ := st.ListRuns(context.Background(), store.RunFilter{Kind: platsvc.KindInstall})
	if len(runs) != 1 || runs[0].Status != store.StatusSucceeded || runs[0].Target != "ingress/traefik" {
		t.Fatalf("expected one succeeded install of ingress/traefik, got %+v", runs)
	}
}

func TestPlatformUninstall_FailPropagates(t *testing.T) {
	f, _ := fakePlatformEnv(t)
	f.WhenArgsContainStderr("uninstall.sh", "", "boom\n", 1)

	var buf bytes.Buffer
	err := runPlatformComponentOp(dummyCmd(&buf), "uninstall", "ingress", "traefik",
		func(ctx context.Context, svc *platsvc.Service) (string, error) {
			return svc.Uninstall(ctx, "ingress", "traefik")
		})
	if err == nil {
		t.Fatalf("expected a non-nil error for a failed uninstall; output: %s", buf.String())
	}
	if !strings.Contains(err.Error(), "uninstall ingress/traefik failed") {
		t.Errorf("error should name the failed op: %v", err)
	}
}

func TestPlatformStatusFromStore_ShowsInstalledComponent(t *testing.T) {
	f, _ := fakePlatformEnv(t)
	f.WhenArgsContain("install.sh", "", 0)

	// Install first so there is history.
	if err := runPlatformComponentOp(dummyCmd(&bytes.Buffer{}), "install", "ingress", "traefik",
		func(ctx context.Context, svc *platsvc.Service) (string, error) {
			return svc.Install(ctx, "ingress", "traefik")
		}); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := platformStatusFromStore(dummyCmd(&buf), nil); err != nil {
		t.Fatalf("status: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "ingress/traefik") || !strings.Contains(out, "installed") {
		t.Errorf("status should show ingress/traefik installed: %q", out)
	}
}

func TestPlatformStatusFromStore_EmptyHistory(t *testing.T) {
	fakePlatformEnv(t)
	var buf bytes.Buffer
	if err := platformStatusFromStore(dummyCmd(&buf), nil); err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(buf.String(), "No platform components have been installed") {
		t.Errorf("expected empty-history message: %q", buf.String())
	}
}

func TestPlatformTeardown_RemovesInventoryAndReportsFailures(t *testing.T) {
	f, st := fakePlatformEnv(t)
	// traefik uninstall succeeds (Fake default); the prometheus uninstall fails.
	f.WhenArgsContainStderr("monitoring/metrics/prometheus/uninstall.sh", "", "stuck\n", 1)

	ctx := context.Background()
	// Record two installed components directly in the inventory (as the finish
	// hook would after successful installs).
	for _, c := range []store.Component{
		{ID: "platform:ingress/traefik", Kind: "platform", Ref: "ingress/traefik"},
		{ID: "platform:monitoring/metrics/prometheus", Kind: "platform", Ref: "monitoring/metrics/prometheus"},
	} {
		if err := st.RecordComponentInstalled(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	err := platformTeardown(dummyCmd(&buf))
	out := buf.String()

	// One failed component → non-nil error, but the other was still removed.
	if err == nil {
		t.Fatalf("expected an error naming the component that could not be removed; output: %s", out)
	}
	if !strings.Contains(out, "could not remove monitoring/metrics/prometheus") {
		t.Errorf("report should name the stuck component: %q", out)
	}
	if !strings.Contains(out, "Removed 1 of 2") {
		t.Errorf("report should say 1 of 2 removed: %q", out)
	}
	// traefik was removed → its inventory row is marked removed; prometheus stays installed.
	traefik, _ := st.GetComponent(ctx, "platform:ingress/traefik")
	if traefik.Status != store.ComponentRemoved {
		t.Errorf("traefik status = %q, want removed", traefik.Status)
	}
	prom, _ := st.GetComponent(ctx, "platform:monitoring/metrics/prometheus")
	if prom.Status != store.ComponentInstalled {
		t.Errorf("prometheus status = %q, want still installed (its uninstall failed)", prom.Status)
	}
}

func TestPlatformTeardown_EmptyInventory(t *testing.T) {
	fakePlatformEnv(t)
	var buf bytes.Buffer
	if err := platformTeardown(dummyCmd(&buf)); err != nil {
		t.Fatalf("teardown: %v", err)
	}
	if !strings.Contains(buf.String(), "Nothing to tear down") {
		t.Errorf("expected empty-inventory message: %q", buf.String())
	}
}

func TestSplitComponent(t *testing.T) {
	cases := map[string][2]string{
		"ingress/traefik":               {"ingress", "traefik"},
		"monitoring/metrics/prometheus": {"monitoring/metrics", "prometheus"},
		"solo":                          {"", "solo"},
	}
	for in, want := range cases {
		cat, prov := splitComponent(in)
		if cat != want[0] || prov != want[1] {
			t.Errorf("splitComponent(%q) = (%q,%q), want (%q,%q)", in, cat, prov, want[0], want[1])
		}
	}
}
