// SPDX-License-Identifier: Apache-2.0
package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// newTestManager builds a Manager with injected context functions and a temp
// runtimes dir, bypassing kubectl entirely.
func newTestManager(t *testing.T, runtimesDir, clusterName, currentCtx string, knownCtxs []string) *Manager {
	t.Helper()
	m := &Manager{
		ProjectRoot: filepath.Dir(runtimesDir), // parent of "runtimes"
		ClusterName: clusterName,
		getCurrentCtx: func(_ context.Context) (string, error) {
			return currentCtx, nil
		},
		contextExists: func(_ context.Context, name string) (bool, error) {
			for _, c := range knownCtxs {
				if c == name {
					return true, nil
				}
			}
			return false, nil
		},
	}
	m.scan()
	return m
}

// makeRuntime creates a minimal runtime directory with an up.sh.
func makeRuntime(t *testing.T, base, name string, envLines []string) {
	t.Helper()
	dir := filepath.Join(base, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	// up.sh must exist for scan() to pick it up
	if err := os.WriteFile(filepath.Join(dir, "up.sh"), []byte("#!/bin/bash\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if len(envLines) > 0 {
		content := ""
		for _, l := range envLines {
			content += l + "\n"
		}
		if err := os.WriteFile(filepath.Join(dir, "runtime.env"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestList_K3dCurrentContext(t *testing.T) {
	root := t.TempDir()
	rtDir := filepath.Join(root, "runtimes")
	makeRuntime(t, rtDir, "k3d", nil)

	clusterName := "snowops"
	expectedCtx := "k3d-" + clusterName
	m := newTestManager(t, rtDir, clusterName, expectedCtx, []string{expectedCtx})

	list := m.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 runtime, got %d", len(list))
	}
	r := list[0]
	if r.Name != "k3d" {
		t.Errorf("name = %q, want k3d", r.Name)
	}
	if !r.Active {
		t.Error("want Active=true when context exists")
	}
	if !r.Current {
		t.Error("want Current=true when context is the current context")
	}
}

func TestList_K3dContextExistsButNotCurrent(t *testing.T) {
	root := t.TempDir()
	rtDir := filepath.Join(root, "runtimes")
	makeRuntime(t, rtDir, "k3d", nil)

	clusterName := "snowops"
	expectedCtx := "k3d-" + clusterName
	// current context is something else
	m := newTestManager(t, rtDir, clusterName, "other-context", []string{expectedCtx})

	list := m.List()
	r := list[0]
	if !r.Active {
		t.Error("want Active=true: context exists in kubeconfig")
	}
	if r.Current {
		t.Error("want Current=false: not the current context")
	}
}

func TestList_K3dContextMissing(t *testing.T) {
	root := t.TempDir()
	rtDir := filepath.Join(root, "runtimes")
	makeRuntime(t, rtDir, "k3d", nil)

	m := newTestManager(t, rtDir, "my-cluster", "", []string{})

	list := m.List()
	r := list[0]
	if r.Active {
		t.Error("want Active=false: no matching context in kubeconfig")
	}
	if r.Current {
		t.Error("want Current=false")
	}
}

func TestList_CloudRuntimeWithExplicitContext(t *testing.T) {
	root := t.TempDir()
	rtDir := filepath.Join(root, "runtimes")
	makeRuntime(t, rtDir, "kind", []string{
		"INGRESS_CLASS=nginx",
		"KUBECONFIG_CONTEXT=my-kind-cluster",
	})

	m := newTestManager(t, rtDir, "snowops", "my-kind-cluster", []string{"my-kind-cluster"})

	list := m.List()
	r := list[0]
	if !r.Active {
		t.Error("want Active=true: explicit context found in kubeconfig")
	}
	if !r.Current {
		t.Error("want Current=true: is the current context")
	}
}

func TestList_CloudRuntimeNoContextConfig(t *testing.T) {
	root := t.TempDir()
	rtDir := filepath.Join(root, "runtimes")
	// kind runtime.env has no KUBECONFIG_CONTEXT
	makeRuntime(t, rtDir, "kind", []string{
		"INGRESS_CLASS=nginx",
		"STORAGE_CLASS=managed-csi",
	})

	// Even if some context named "kind" happens to be in kubeconfig, it must
	// NOT be treated as active — we cannot reliably guess the context name.
	m := newTestManager(t, rtDir, "snowops", "kind", []string{"kind"})

	list := m.List()
	r := list[0]
	if r.Active {
		t.Errorf("want Active=false: no KUBECONFIG_CONTEXT in runtime.env, cannot guess context name")
	}
	if r.Current {
		t.Error("want Current=false")
	}
}

func TestList_MultipleRuntimes(t *testing.T) {
	root := t.TempDir()
	rtDir := filepath.Join(root, "runtimes")
	makeRuntime(t, rtDir, "k3d", nil)
	makeRuntime(t, rtDir, "kind", []string{"INGRESS_CLASS=nginx"}) // no KUBECONFIG_CONTEXT

	m := newTestManager(t, rtDir, "snowops", "k3d-snowops", []string{"k3d-snowops"})

	byName := map[string]RuntimeInfo{}
	for _, r := range m.List() {
		byName[r.Name] = r
	}

	k3d, ok := byName["k3d"]
	if !ok {
		t.Fatal("k3d not in list")
	}
	if !k3d.Active || !k3d.Current {
		t.Errorf("k3d: want Active=true Current=true, got Active=%v Current=%v", k3d.Active, k3d.Current)
	}

	kind, ok := byName["kind"]
	if !ok {
		t.Fatal("kind not in list")
	}
	if kind.Active || kind.Current {
		t.Errorf("kind: want Active=false Current=false, got Active=%v Current=%v", kind.Active, kind.Current)
	}
}

func TestReadEnvKey(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "runtime.env")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	f.WriteString("# comment\n")
	f.WriteString("INGRESS_CLASS=nginx\n")
	f.WriteString("KUBECONFIG_CONTEXT=my-cluster-ctx\n")
	f.WriteString("STORAGE_CLASS=managed-csi\n")

	if got := readEnvKey(f.Name(), "KUBECONFIG_CONTEXT"); got != "my-cluster-ctx" {
		t.Errorf("got %q, want my-cluster-ctx", got)
	}
	if got := readEnvKey(f.Name(), "MISSING"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}
