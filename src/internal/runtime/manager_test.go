// SPDX-License-Identifier: Apache-2.0

package runtime

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/snowops-oss/snowopslabs/internal/executor"
	"github.com/snowops-oss/snowopslabs/internal/labtest"
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
			return slices.Contains(knownCtxs, name), nil
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
		content := strings.Join(envLines, "\n") + "\n"
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
	// NOT be treated as active - we cannot reliably guess the context name.
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

// fixtureManager is a Manager over the fixture lab, with the executor that
// runs its scripts and the buffer their output goes to.
func fixtureManager(t *testing.T) (*Manager, *executor.Executor, *labtest.Lab, *bytes.Buffer) {
	t.Helper()
	l := labtest.New(t)
	l.Write(t, "runtimes/k3d/up.sh", "#!/usr/bin/env bash\necho \"up $1\"\n")
	l.Write(t, "runtimes/k3d/down.sh", "#!/usr/bin/env bash\necho \"down $1\"\n")
	ex := executor.New(l.Root)
	var out bytes.Buffer
	ex.Stdout, ex.Stderr = &out, &out
	return NewManager(l.Root, "lab"), ex, l, &out
}

func TestNewManager_FindsRuntimesWithAnUpScript(t *testing.T) {
	m, _, l, _ := fixtureManager(t)
	l.Write(t, "runtimes/_lib/docker.sh", "#!/usr/bin/env bash\n")
	l.Write(t, "runtimes/kind/up.sh", "#!/usr/bin/env bash\n")
	l.Write(t, "runtimes/README.md", "not a runtime\n")
	m = NewManager(m.ProjectRoot, "lab")
	if want := []string{"k3d", "kind"}; !slices.Equal(m.Names(), want) {
		t.Errorf("Names() = %v, want %v", m.Names(), want)
	}
	if got := NewManager(t.TempDir(), "lab").Names(); len(got) != 0 {
		t.Errorf("a project with no runtimes/ lists %v", got)
	}
}

// The manager asks the real kubectl for the current context and whether the
// runtime's context exists.
func TestList_AsksKubectl(t *testing.T) {
	tests := []struct {
		name        string
		kubectl     string
		wantCurrent bool
		wantActive  bool
	}{
		{"the runtime's context is current", "echo k3d-lab\n", true, true},
		{"the context exists but another is current",
			"case \"$*\" in \"config current-context\") echo kind-other ;; \"config get-contexts k3d-lab --no-headers\") echo 'k3d-lab k3d-lab admin' ;; esac\n", false, true},
		{"the context does not exist",
			"case \"$*\" in \"config current-context\") echo kind-other ;; *) exit 1 ;; esac\n", false, false},
		{"kubectl has no context at all", "exit 1\n", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, _, l, _ := fixtureManager(t)
			l.Stub(t, "kubectl", "#!/bin/sh\n"+tt.kubectl)
			got := m.List()
			if len(got) != 1 || got[0].Name != "k3d" || got[0].Current != tt.wantCurrent || got[0].Active != tt.wantActive {
				t.Errorf("List() = %+v, want k3d with current=%v active=%v", got, tt.wantCurrent, tt.wantActive)
			}
		})
	}
}

func TestActivateAndDeactivate(t *testing.T) {
	steps := []struct {
		name string
		run  func(m *Manager, ex *executor.Executor, runtime string) error
		want string
	}{
		{"Activate", func(m *Manager, ex *executor.Executor, rt string) error { return m.Activate(rt, ex) }, "up lab"},
		{"ActivateWith", func(m *Manager, ex *executor.Executor, rt string) error { return m.ActivateWith("job-1", rt, ex) }, "up lab"},
		{"Deactivate", func(m *Manager, ex *executor.Executor, rt string) error { return m.Deactivate(rt, ex) }, "down lab"},
		{"DeactivateWith", func(m *Manager, ex *executor.Executor, rt string) error { return m.DeactivateWith("job-2", rt, ex) }, "down lab"},
	}
	for _, step := range steps {
		t.Run(step.name+" runs the runtime's script with the cluster name", func(t *testing.T) {
			m, ex, _, out := fixtureManager(t)
			if err := step.run(m, ex, "k3d"); err != nil {
				t.Fatalf("%s() error = %v", step.name, err)
			}
			if !strings.Contains(out.String(), step.want) {
				t.Errorf("%s() output = %q, want it to contain %q", step.name, out.String(), step.want)
			}
		})
		t.Run(step.name+" refuses a runtime that does not exist", func(t *testing.T) {
			m, ex, _, out := fixtureManager(t)
			err := step.run(m, ex, "ghost")
			if err == nil || !strings.Contains(err.Error(), `runtime "ghost" not found`) {
				t.Errorf("%s(ghost) error = %v", step.name, err)
			}
			if out.Len() != 0 {
				t.Errorf("%s(ghost) ran a script: %q", step.name, out.String())
			}
		})
		t.Run(step.name+" reports a script that fails", func(t *testing.T) {
			m, ex, l, _ := fixtureManager(t)
			for _, s := range []string{"up.sh", "down.sh"} {
				l.Write(t, "runtimes/k3d/"+s, "#!/usr/bin/env bash\nexit 4\n")
			}
			if err := step.run(m, ex, "k3d"); err == nil {
				t.Errorf("%s() returned no error for a script that exited 4", step.name)
			}
		})
	}
}
