// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/snowops-oss/snowopslabs/internal/config"
	"github.com/snowops-oss/snowopslabs/internal/labtest"
	"github.com/snowops-oss/snowopslabs/internal/platform"
	platsvc "github.com/snowops-oss/snowopslabs/internal/service/platform"
	"github.com/snowops-oss/snowopslabs/internal/store"
)

// writeProvider creates platform/<relDir>/install.sh so the registry scan
// discovers a provider at that path.
func writeProvider(t *testing.T, root, relDir string) {
	t.Helper()
	dir := filepath.Join(root, "platform", relDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "install.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestPrereqProviders(t *testing.T) {
	root := t.TempDir()
	writeProvider(t, root, "cost/opencost")                 // category/provider
	writeProvider(t, root, "monitoring/metrics/prometheus") // sub-category with a provider
	writeProvider(t, root, "ingress/traefik")               // bare category, provider owns its ns
	writeProvider(t, root, "ingress/nginx")                 // second mutually-exclusive provider
	reg := platform.NewRegistry(root)

	tests := []struct {
		name     string
		prereq   string
		expected []string
	}{
		{name: "category/provider resolves to that provider", prereq: "cost/opencost", expected: []string{"opencost"}},
		{name: "sub-category resolves to its provider", prereq: "monitoring/metrics", expected: []string{"prometheus"}},
		{name: "bare category resolves to every provider", prereq: "ingress", expected: []string{"nginx", "traefik"}},
		{name: "unknown prereq resolves to nothing", prereq: "does/not/exist", expected: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := []string{}
			for _, p := range prereqProviders(reg, tt.prereq) {
				got = append(got, p.Name)
			}
			sort.Strings(got)
			if !slices.Equal(got, tt.expected) {
				t.Errorf("prereqProviders(%q) = %v, want %v", tt.prereq, got, tt.expected)
			}
		})
	}
}

// stubKubectl puts a fake kubectl first on PATH that answers `get namespace`
// successfully only for the namespaces listed.
func stubKubectl(t *testing.T, namespaces ...string) {
	t.Helper()
	dir := t.TempDir()
	var script strings.Builder
	script.WriteString("#!/bin/sh\ncase \"$*\" in\n")
	for _, ns := range namespaces {
		script.WriteString("  \"get namespace " + ns + " --no-headers\") exit 0 ;;\n")
	}
	script.WriteString("esac\nexit 1\n")
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(script.String()), 0o755); err != nil { //nolint:gosec // test stub must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestEnsurePlatformPrereqs(t *testing.T) {
	root := t.TempDir()
	writeProvider(t, root, "cost/opencost")
	oldReg := reg
	t.Cleanup(func() { reg = oldReg })
	reg = platform.NewRegistry(root)

	tests := []struct {
		name      string
		installed []string
		prereqs   []string
		wantErr   []string
	}{
		{name: "installed", installed: []string{"opencost"}, prereqs: []string{"cost/opencost"}},
		{
			name:    "missing is an error with the install command",
			prereqs: []string{"cost/opencost"},
			wantErr: []string{"not installed: cost/opencost", "labctl platform up cost/opencost", "--deploy-prereqs"},
		},
		{name: "unknown to the registry is left to preflight", prereqs: []string{"does/not/exist"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubKubectl(t, tt.installed...)
			var out bytes.Buffer
			err := ensurePlatformPrereqs(t.Context(), &out, tt.prereqs, false)
			if len(tt.wantErr) == 0 {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			for _, want := range tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Errorf("error should mention %q, got %v", want, err)
				}
			}
		})
	}
}

// A cluster that does not answer is reported as that. It is never mistaken
// for a lab with nothing deployed, and nothing is changed or recorded.
func TestClusterDown_CommandsSayTheClusterIsUnreachable(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"scenario up", []string{"scenario", "up", labtest.Scenario}},
		{"incident inject", []string{"incident", "inject", labtest.Fault}},
		{"incident inject --random", []string{"incident", "inject", "--random"}},
		{"challenge start", []string{"challenge", "start", labtest.Challenge}},
		{"lab snapshot", []string{"lab", "snapshot", "before"}},
		{"lab reset", []string{"lab", "reset", "--yes"}},
		{"platform status --live", []string{"platform", "status", "--live"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newTerminal(t)
			l.clusterDown()
			r := l.run(tt.args...)
			if r.exitCode() != 1 {
				t.Fatalf("exit code = %d, want 1\nstdout:\n%s", r.exitCode(), r.stdout)
			}
			for _, want := range []string{"cannot reach the cluster", "was refused", "labctl status"} {
				if !strings.Contains(r.stderr, want) {
					t.Errorf("stderr does not say %q:\n%s", want, r.stderr)
				}
			}
			for _, wrong := range []string{"not deployed", "labctl app build", "Saved.", "Lab reset to post-init state"} {
				if strings.Contains(r.stdout+r.stderr, wrong) {
					t.Errorf("the output says %q about a cluster that did not answer:\n%s%s", wrong, r.stdout, r.stderr)
				}
			}
			if l.exists(faultMarker) {
				t.Error("the fault was injected into a cluster that did not answer")
			}
			if r := l.run("lab", "snapshots"); !strings.Contains(r.stdout, "No snapshots") {
				t.Errorf("a snapshot of an unreachable cluster was saved:\n%s", r.stdout)
			}
		})
	}
}

// `labctl status` tells a stopped cluster from one that was never created.
func TestStatus_ClusterDown(t *testing.T) {
	l := newTerminal(t)
	l.clusterDown()
	r := l.mustRun("status")
	if !strings.Contains(r.stdout, "UNREACHABLE") || !strings.Contains(r.stdout, "was refused") {
		t.Errorf("status does not report the cluster as unreachable with kubectl's reason:\n%s", r.stdout)
	}
	l.golden("status_cluster_down", r)
}

// With --deploy-prereqs a missing component is installed as a run through the
// platform service, as `platform up` does, and the learner is told which one
// and why before it starts. A failed install stops with the run that failed.
func TestEnsurePlatformPrereqs_AutoInstall(t *testing.T) {
	tests := []struct {
		name       string
		exitCode   int
		wantErr    string
		wantStatus store.Status
	}{
		{name: "installs it as a recorded run", wantStatus: store.StatusSucceeded},
		{
			name:       "a failed install stops and names the run",
			exitCode:   1,
			wantErr:    "installing platform prerequisite ingress/traefik: install ingress/traefik failed (run ",
			wantStatus: store.StatusFailed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, st := fakePlatformEnv(t)
			f.WhenArgsContain("ingress/traefik/install.sh", "installing traefik\n", tt.exitCode)
			root := t.TempDir()
			writeProvider(t, root, "ingress/traefik")
			oldReg := reg
			t.Cleanup(func() { reg = oldReg })
			reg = platform.NewRegistry(root)
			stubKubectl(t)

			var out bytes.Buffer
			err := ensurePlatformPrereqs(t.Context(), &out, []string{"ingress/traefik"}, true)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("ensurePlatformPrereqs: %v\n%s", err, out.String())
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("error = %v, want it to say %q", err, tt.wantErr)
			}
			for _, want := range []string{
				"Platform prerequisite ingress/traefik is not installed - installing ingress/traefik...",
				"install ingress/traefik started (run ",
				"installing traefik",
			} {
				if !strings.Contains(out.String(), want) {
					t.Errorf("output does not say %q:\n%s", want, out.String())
				}
			}
			runs, err := st.ListRuns(t.Context(), store.RunFilter{Kind: platsvc.KindInstall})
			if err != nil {
				t.Fatal(err)
			}
			if len(runs) != 1 || runs[0].Target != "ingress/traefik" || runs[0].Status != tt.wantStatus {
				t.Fatalf("want one %s install run of ingress/traefik, got %+v", tt.wantStatus, runs)
			}
		})
	}
}

// A prerequisite naming a category with several providers, none of them
// chosen, stops before anything installs and says how to pick one.
func TestEnsurePlatformPrereqs_AutoInstallNeedsAProvider(t *testing.T) {
	_, st := fakePlatformEnv(t)
	root := t.TempDir()
	writeProvider(t, root, "mesh/istio")
	writeProvider(t, root, "mesh/linkerd")
	oldReg, oldCfg := reg, cfg
	t.Cleanup(func() { reg, cfg = oldReg, oldCfg })
	reg = platform.NewRegistry(root)
	cfg = &config.Config{}
	stubKubectl(t)

	err := ensurePlatformPrereqs(t.Context(), &bytes.Buffer{}, []string{"mesh"}, true)
	for _, want := range []string{"resolving platform prerequisite mesh", "labctl platform up mesh/istio"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to say %q", err, want)
		}
	}
	if runs, _ := st.ListRuns(t.Context(), store.RunFilter{}); len(runs) != 0 {
		t.Errorf("nothing should install, got %+v", runs)
	}
}
