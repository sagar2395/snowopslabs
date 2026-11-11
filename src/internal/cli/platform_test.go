// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snowops-oss/snowopslabs/internal/config"
	"github.com/snowops-oss/snowopslabs/internal/platform"
)

func TestResolveProvider(t *testing.T) {
	root := t.TempDir()
	writeProvider(t, root, "mesh/istio")
	writeProvider(t, root, "mesh/linkerd")
	writeProvider(t, root, "cost/opencost")
	oldReg, oldCfg := reg, cfg
	t.Cleanup(func() { reg, cfg = oldReg, oldCfg })
	reg = platform.NewRegistry(root)

	tests := []struct {
		name     string
		cfg      config.Config
		category string
		expected string
		wantErr  []string
	}{
		{name: "the configured provider", cfg: config.Config{MeshProvider: "linkerd"}, category: "mesh", expected: "linkerd"},
		{name: "the only provider", category: "cost", expected: "opencost"},
		{
			name:     "several and none configured names both ways to choose",
			category: "mesh",
			wantErr:  []string{"istio, linkerd", "labctl platform up mesh/istio", "MESH_PROVIDER=istio in .env"},
		},
		{name: "unknown category", category: "nope", wantErr: []string{`unknown platform category "nope"`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := tt.cfg
			cfg = &c
			got, err := resolveProvider(tt.category)
			if len(tt.wantErr) == 0 {
				if err != nil || got != tt.expected {
					t.Fatalf("resolveProvider(%q) = %q, %v; want %q", tt.category, got, err, tt.expected)
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

// clusterWithout makes the fixture cluster stop listing namespace, as it does
// once the component in it is uninstalled. Every other kubectl call answers as
// before. Call it at most once per test.
func clusterWithout(t *testing.T, l *terminal, namespace string) {
	t.Helper()
	if err := os.Rename(filepath.Join(l.Bin, "kubectl"), filepath.Join(l.Bin, "kubectl.fixture")); err != nil {
		t.Fatal(err)
	}
	l.Stub(t, "kubectl", `#!/bin/sh
fixture="$(dirname "$0")/kubectl.fixture"
case "$*" in
  "get namespaces -o name") "$fixture" "$@" | grep -vx "namespace/`+namespace+`" ;;
  *) exec "$fixture" "$@" ;;
esac
`)
}

// One platform component at the terminal: install it, see it in the status,
// uninstall it. Then the whole baseline in one command each way.
func TestPlatform_Journey(t *testing.T) {
	l := newTerminal(t)
	var steps []cliResult
	step := func(args ...string) cliResult {
		t.Helper()
		r := l.mustRun(args...)
		steps = append(steps, r)
		return r
	}

	step("platform", "status")
	if r := step("platform", "up", "ingress"); !strings.Contains(r.stdout, "installing traefik") {
		t.Errorf("up ingress did not run traefik's install script:\n%s", r.stdout)
	}
	if r := step("platform", "status"); !strings.Contains(r.stdout, "ingress/traefik") || !strings.Contains(r.stdout, "installed") {
		t.Errorf("status does not list the installed component:\n%s", r.stdout)
	}
	step("platform", "status", "ingress")
	step("platform", "up", "monitoring/metrics/prometheus")
	if r := step("platform", "down", "ingress"); !strings.Contains(r.stdout, "removing traefik") {
		t.Errorf("down ingress did not run traefik's uninstall script:\n%s", r.stdout)
	}
	clusterWithout(t, l, "traefik")
	if r := step("platform", "status"); !strings.Contains(r.stdout, "ingress/traefik                 not installed") {
		t.Errorf("status still lists the uninstalled component as installed:\n%s", r.stdout)
	}

	up := step("platform", "up")
	for _, want := range []string{"installing traefik", "installing prometheus", "installing grafana", "Platform installed successfully."} {
		if !strings.Contains(up.stdout, want) {
			t.Errorf("up does not print %q:\n%s", want, up.stdout)
		}
	}
	step("platform", "status", "--live")
	down := step("platform", "down")
	for _, want := range []string{"removing grafana", "removing prometheus", "removing traefik", "Platform uninstalled."} {
		if !strings.Contains(down.stdout, want) {
			t.Errorf("down does not print %q:\n%s", want, down.stdout)
		}
	}
	l.golden("platform_journey", steps...)
}

func TestPlatform_Failures(t *testing.T) {
	failing := func(rel string) func(t *testing.T, l *terminal) {
		return func(t *testing.T, l *terminal) {
			l.Write(t, rel, "#!/usr/bin/env bash\necho 'helm: timed out' >&2\nexit 6\n")
		}
	}
	tests := []struct {
		name    string
		prepare func(t *testing.T, l *terminal)
		args    []string
		wantErr string
		wantOut string // also printed before the failure
	}{
		{"up a target that does not exist", nil, []string{"platform", "up", "ghost"}, `unknown platform target "ghost"`, ""},
		{"down a target that does not exist", nil, []string{"platform", "down", "ghost"}, `unknown platform target "ghost"`, ""},
		{"status of a target that does not exist", nil, []string{"platform", "status", "ghost", "--live"}, "ghost", ""},
		{"up one component whose install fails", failing("platform/ingress/traefik/install.sh"),
			[]string{"platform", "up", "ingress"}, "install ingress/traefik failed", ""},
		{"down one component whose uninstall fails", failing("platform/ingress/traefik/uninstall.sh"),
			[]string{"platform", "down", "ingress"}, "uninstall ingress/traefik failed", ""},
		{"up everything when the ingress fails stops there", failing("platform/ingress/traefik/install.sh"),
			[]string{"platform", "up"}, "ingress install failed", ""},
		{"up everything when metrics fails still tries grafana", failing("platform/monitoring/metrics/prometheus/install.sh"),
			[]string{"platform", "up"}, "metrics (prometheus)", "installing grafana"},
		{"down everything when an uninstall fails still removes the rest", failing("platform/monitoring/metrics/prometheus/uninstall.sh"),
			[]string{"platform", "down"}, "metrics (prometheus)", "removing traefik"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newTerminal(t)
			if tt.prepare != nil {
				tt.prepare(t, l)
			}
			r := l.run(tt.args...)
			if r.exitCode() != 1 {
				t.Fatalf("exit code = %d, want 1\nstdout:\n%s", r.exitCode(), r.stdout)
			}
			if !strings.Contains(r.stderr, tt.wantErr) {
				t.Errorf("stderr = %q, want it to say %q", r.stderr, tt.wantErr)
			}
			if !strings.Contains(r.stdout, tt.wantOut) {
				t.Errorf("stdout does not show %q:\n%s", tt.wantOut, r.stdout)
			}
			for _, lie := range []string{"Platform installed successfully.", "Platform uninstalled."} {
				if strings.Contains(r.stdout, lie) {
					t.Errorf("the output says %q after a failure:\n%s", lie, r.stdout)
				}
			}
		})
	}
}

// `platform teardown` removes everything the inventory says is installed.
func TestPlatform_Teardown(t *testing.T) {
	l := newTerminal(t)
	l.mustRun("platform", "up", "ingress")
	l.mustRun("platform", "up", "monitoring/grafana")
	r := l.mustRun("platform", "teardown")
	for _, want := range []string{"removing traefik", "removing grafana"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("teardown does not print %q:\n%s", want, r.stdout)
		}
	}
	clusterWithout(t, l, "traefik")
	status := l.mustRun("platform", "status")
	for line := range strings.Lines(status.stdout) {
		f := strings.Fields(line)
		if len(f) >= 2 && (f[0] == "ingress/traefik" || f[0] == "monitoring/grafana") && f[1] == "installed" {
			t.Errorf("%s is still listed as installed after teardown:\n%s", f[0], status.stdout)
		}
	}
	l.golden("platform_teardown", r, status)
}
