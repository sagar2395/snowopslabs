// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/snowops-oss/snowopslabs/internal/inventory"
	"github.com/snowops-oss/snowopslabs/internal/platform"
	"github.com/snowops-oss/snowopslabs/internal/run"
	platsvc "github.com/snowops-oss/snowopslabs/internal/service/platform"
	"github.com/snowops-oss/snowopslabs/internal/store"
	"github.com/snowops-oss/snowopslabs/internal/toolchain"
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
	// The cluster cannot be read unless a test says what it holds.
	stubCluster(t, nil, errors.New("cannot reach the cluster"))
	t.Cleanup(func() {
		platformEngineFactory = prev
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = eng.Shutdown(sctx)
		_ = st.Close()
	})
	return f, st
}

// stubCluster makes installedComponents report the given components, or err,
// for the duration of a test.
func stubCluster(t *testing.T, components []string, err error) {
	t.Helper()
	prev := installedComponents
	installedComponents = func(context.Context) (map[string]bool, error) {
		if err != nil {
			return nil, err
		}
		m := map[string]bool{}
		for _, c := range components {
			m[c] = true
		}
		return m, nil
	}
	t.Cleanup(func() { installedComponents = prev })
}

// clusterFromKubectl makes installedComponents read the cluster through
// platsvc.Installed, as status does, from a fake kubectl that runs script. The
// registry holds traefik and prometheus, as fakePlatformEnv does.
func clusterFromKubectl(t *testing.T, script string) {
	t.Helper()
	root := t.TempDir()
	writeProvider(t, root, "ingress/traefik")
	writeProvider(t, root, "monitoring/metrics/prometheus")
	oldReg := reg
	reg = platform.NewRegistry(root)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "kubectl"), []byte("#!/bin/sh\n"+script), 0o755); err != nil { //nolint:gosec // test stub must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	prev := installedComponents
	installedComponents = func(ctx context.Context) (map[string]bool, error) {
		return platsvc.Installed(ctx, reg)
	}
	t.Cleanup(func() {
		installedComponents = prev
		reg = oldReg
	})
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

// failedPrometheusRelease is a cluster where a prometheus install failed: the
// monitoring namespace and a release Secret with status=failed are left.
const failedPrometheusRelease = `case "$*" in
  *"get namespaces"*) printf 'namespace/default\nnamespace/monitoring\n' ;;
  *"owner=helm,status=deployed"*) exit 0 ;;
  *"owner=helm"*) echo 'monitoring   prometheus' ;;
esac`

func TestPlatformStatusFromStore_ReadsTheCluster(t *testing.T) {
	tests := []struct {
		name      string
		installed string // what labctl installed first, through platform up
		failed    bool   // whether that labctl install failed
		cluster   []string
		kubectl   string   // when set, the cluster is read from a fake kubectl instead
		want      []string // lines the table must hold
		dontWant  []string
	}{
		{
			name:    "a component installed outside platform up is listed",
			cluster: []string{"autoscaling/keda", "secrets/vault"},
			want:    []string{"autoscaling/keda   installed   -", "secrets/vault      installed   -"},
		},
		{
			name:      "the cluster wins over a stale install run",
			installed: "ingress/traefik",
			want:      []string{"ingress/traefik   not installed"},
			dontWant:  []string{"traefik   installed"},
		},
		{
			name:      "a run in the history and a release in the cluster share one row",
			installed: "ingress/traefik",
			cluster:   []string{"ingress/traefik"},
			want:      []string{"ingress/traefik   installed"},
		},
		{
			name:      "a failed install that left nothing shows the error",
			installed: "ingress/traefik",
			failed:    true,
			want:      []string{"ingress/traefik   error"},
		},
		{
			name:      "a failed install whose namespace is still there shows the error",
			installed: "ingress/traefik",
			failed:    true,
			cluster:   []string{"ingress/traefik"},
			want:      []string{"ingress/traefik   error"},
			dontWant:  []string{"traefik   installed"},
		},
		{
			name:      "a failed install that left a failed release Secret shows the error",
			installed: "monitoring/metrics/prometheus",
			failed:    true,
			kubectl:   failedPrometheusRelease,
			want:      []string{"monitoring/metrics/prometheus   error"},
		},
		{
			name:    "a release that never deployed is not listed",
			kubectl: failedPrometheusRelease,
			want:    []string{"No platform components are installed in the cluster."},
		},
		{
			name: "an empty cluster says so",
			want: []string{"No platform components are installed in the cluster."},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, _ := fakePlatformEnv(t)
			if tt.kubectl != "" {
				clusterFromKubectl(t, tt.kubectl)
			} else {
				stubCluster(t, tt.cluster, nil)
			}
			exit := 0
			if tt.failed {
				exit = 1
			}
			f.WhenArgsContain("install.sh", "", exit)
			if tt.installed != "" {
				cat, prov := splitComponent(tt.installed)
				// A failed install returns an error; the table must still show it.
				_ = runPlatformComponentOp(dummyCmd(&bytes.Buffer{}), "install", cat, prov,
					func(ctx context.Context, svc *platsvc.Service) (string, error) {
						return svc.Install(ctx, cat, prov)
					})
			}

			var buf bytes.Buffer
			if err := platformStatusFromStore(dummyCmd(&buf), nil); err != nil {
				t.Fatalf("status: %v", err)
			}
			out := buf.String()
			for _, w := range tt.want {
				if !strings.Contains(out, w) {
					t.Errorf("status should contain %q:\n%s", w, out)
				}
			}
			for _, w := range tt.dontWant {
				if strings.Contains(out, w) {
					t.Errorf("status should not contain %q:\n%s", w, out)
				}
			}
			if strings.Count(out, "ingress/traefik") > 1 {
				t.Errorf("a component should have one row:\n%s", out)
			}
		})
	}
}

func TestPlatformStatusFromStore_UnreadableClusterFallsBackToHistory(t *testing.T) {
	f, _ := fakePlatformEnv(t)
	f.WhenArgsContain("install.sh", "", 0)
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
	if !strings.Contains(out, "Could not read the cluster (cannot reach the cluster)") ||
		!strings.Contains(out, "labctl status") {
		t.Errorf("status should say the cluster could not be read and how to check it:\n%s", out)
	}
	if !strings.Contains(out, "ingress/traefik   installed") {
		t.Errorf("status should fall back to the run history:\n%s", out)
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
