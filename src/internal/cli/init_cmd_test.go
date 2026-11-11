// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/snowops-oss/snowopslabs/internal/capacity"
	"github.com/snowops-oss/snowopslabs/internal/labtest"
	"github.com/snowops-oss/snowopslabs/internal/toolchain"
)

func TestPreflightDocker(t *testing.T) {
	fake := func(info string, exit int) *toolchain.Fake {
		f := toolchain.NewFake()
		f.WhenArgsContain("context show", "colima\n", 0)
		f.WhenArgsContain("info", info, exit)
		return f
	}
	macOS := capacity.Platform{GOOS: "darwin"}
	tests := []struct {
		name       string
		fake       *toolchain.Fake
		wantErr    []string
		wantOutput string
	}{
		{name: "enough resources pass", fake: fake("2 4294967296", 0), wantOutput: "Docker has 2 CPU / 4 GB"},
		{
			name: "a running vm that is too small is refused with the resize",
			fake: fake("2 2054160384", 0),
			wantErr: []string{
				"too small", "2 CPU / 1.9 GB", "does not resize",
				"colima stop && colima start --cpu 2 --memory 4",
			},
		},
		{
			name:    "a stopped daemon is refused with the start command",
			fake:    fake("", 1),
			wantErr: []string{"not running", "colima start --cpu 2 --memory 4"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			err := preflightDocker(t.Context(), &out, tt.fake, macOS)
			if len(tt.wantErr) == 0 {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if !strings.Contains(out.String(), tt.wantOutput) {
					t.Errorf("output = %q, want it to contain %q", out.String(), tt.wantOutput)
				}
				return
			}
			if err == nil {
				t.Fatal("expected an error")
			}
			for _, want := range tt.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error should mention %q:\n%v", want, err)
				}
			}
		})
	}
}

// quickWaits shortens how long init waits for the ingress and for Grafana, so
// a test of a lab that never answers finishes at once.
func quickWaits(t *testing.T) {
	t.Helper()
	savedIngress, savedDatasource := ingressWait, datasourceWait
	ingressWait, datasourceWait = 0, 0
	t.Cleanup(func() { ingressWait, datasourceWait = savedIngress, savedDatasource })
}

// `labctl init` on a new machine: tools, Docker check, cluster, platform,
// then the checks that prove the lab answers before it says "Lab is up".
func TestInit_BuildsTheLab(t *testing.T) {
	l := newTerminal(t)
	l.serveGrafana("OK")
	quickWaits(t)

	r := l.mustRun("init")
	want := []string{
		"tools ready for k3d", "Docker has 8 CPU", "cluster up",
		"installing traefik", "installing prometheus", "installing grafana",
		"API server ready; all nodes Ready.", "=== Lab is up ===",
		"Grafana:     http://grafana.clitest.localhost:",
	}
	at := 0
	for _, w := range want {
		i := strings.Index(r.stdout[at:], w)
		if i < 0 {
			t.Fatalf("init does not print %q after the previous step:\n%s", w, r.stdout)
		}
		at += i
	}
	l.golden("init_fresh_lab", r)
}

// Whatever goes wrong, init exits non-zero and never says the lab is up.
func TestInit_Failures(t *testing.T) {
	failing := func(rel, msg string) func(t *testing.T, l *terminal) {
		return func(t *testing.T, l *terminal) {
			l.Write(t, rel, "#!/usr/bin/env bash\necho '"+msg+"' >&2\nexit 3\n")
		}
	}
	kubectlFailingOn := func(pattern string) func(t *testing.T, l *terminal) {
		return func(t *testing.T, l *terminal) {
			if err := os.Rename(filepath.Join(l.Bin, "kubectl"), filepath.Join(l.Bin, "kubectl.healthy")); err != nil {
				t.Fatal(err)
			}
			l.Stub(t, "kubectl", "#!/bin/sh\ncase \"$*\" in "+pattern+") echo 'the server is not ready' >&2; exit 1 ;; esac\nexec \"$(dirname \"$0\")/kubectl.healthy\" \"$@\"\n")
		}
	}
	tests := []struct {
		name    string
		grafana string // what the datasource check answers; "" serves no Grafana at all
		prepare func(t *testing.T, l *terminal)
		wantErr string
	}{
		{"the tools cannot be installed", "OK", failing("bootstrap/setup-tools.sh", "curl: could not resolve host"), "setup-tools failed"},
		{"Docker is not running", "OK",
			func(t *testing.T, l *terminal) {
				l.Stub(t, "docker", "#!/bin/sh\necho 'Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?' >&2\nexit 1\n")
			}, "re-run 'labctl init'"},
		{"Docker is too small for the lab", "OK",
			func(t *testing.T, l *terminal) { l.Stub(t, "docker", "#!/bin/sh\necho '1 1000000000'\n") },
			"docker is too small for the lab"},
		{"the cluster cannot be created", "OK", failing("runtimes/k3d/up.sh", "port 6443 is in use"), "bringing up the cluster failed"},
		{"the ingress cannot be installed", "OK", failing("platform/ingress/traefik/install.sh", "helm: timed out"), "ingress install failed"},
		{"a monitoring component cannot be installed", "OK", failing("platform/monitoring/grafana/install.sh", "helm: timed out"), "the platform did not install cleanly"},
		{"the API server stops answering after the install", "OK", kubectlFailingOn("*readyz*"), "the cluster is not answering"},
		{"a node never becomes Ready", "OK", kubectlFailingOn("wait*"), "not every node became Ready"},
		{"the lab's port answers for something else and the ingress cannot be moved", "", nil, "moving the lab's ingress to free ports failed"},
		{"Grafana cannot query Prometheus even after it is re-applied", "ERROR", nil, "grafana still cannot query Prometheus"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newTerminal(t)
			quickWaits(t)
			if tt.grafana != "" {
				l.serveGrafana(tt.grafana)
			}
			if tt.prepare != nil {
				tt.prepare(t, l)
			}
			r := l.run("init")
			if r.exitCode() != 1 {
				t.Fatalf("exit code = %d, want 1\nstdout:\n%s", r.exitCode(), r.stdout)
			}
			if !strings.Contains(r.stderr, tt.wantErr) {
				t.Errorf("stderr = %q, want it to say %q", r.stderr, tt.wantErr)
			}
			if strings.Contains(r.stdout, "Lab is up") {
				t.Errorf("init said the lab is up after failing:\n%s", r.stdout)
			}
		})
	}
}

// A lab whose port is held by something else is moved to free ports, and init
// then reports the URLs on the new port.
func TestInit_MovesTheIngressOffATakenPort(t *testing.T) {
	l := newTerminal(t)
	quickWaits(t)
	// The move records the new port; Grafana answers only once it has run.
	var moved atomic.Bool
	l.ingress.HandleFunc("/api/health", func(w http.ResponseWriter, _ *http.Request) {
		if !moved.Load() {
			http.NotFound(w, nil)
			return
		}
		_, _ = w.Write([]byte(`{"database":"ok"}`))
	})
	l.ingress.HandleFunc("/api/datasources/uid/prometheus/health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"OK"}`))
	})
	l.ingress.HandleFunc("/moved", func(http.ResponseWriter, *http.Request) { moved.Store(true) })
	l.Write(t, "runtimes/k3d/move-ingress.sh",
		"#!/usr/bin/env bash\nset -eu\ncurl -s \"http://127.0.0.1:$HTTP_PORT/moved\" >/dev/null\necho \"moved the ingress of $1\"\n")

	r := l.mustRun("init")
	for _, want := range []string{"answers for something else", "moved the ingress of clitest", "=== Lab is up ==="} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("init does not print %q:\n%s", want, r.stdout)
		}
	}
}

// Grafana that cannot query Prometheus is re-applied, and init succeeds once
// the datasource answers.
func TestInit_ReappliesGrafanaWhenItCannotQueryPrometheus(t *testing.T) {
	l := newTerminal(t)
	quickWaits(t)
	l.ingress.HandleFunc("/api/health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"database":"ok"}`))
	})
	reapplied := filepath.Join(l.Root, "REAPPLIED")
	l.ingress.HandleFunc("/api/datasources/uid/prometheus/health", func(w http.ResponseWriter, _ *http.Request) {
		if _, err := os.Stat(reapplied); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"status":"ERROR","message":"dial tcp: lookup prometheus"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"OK"}`))
	})
	// The first install is init's own; the second is the re-apply.
	l.Write(t, "platform/monitoring/grafana/install.sh",
		"#!/usr/bin/env bash\nset -eu\nif [ -f \"$PROJECT_ROOT/INSTALLED\" ]; then touch \"$PROJECT_ROOT/REAPPLIED\"; fi\ntouch \"$PROJECT_ROOT/INSTALLED\"\necho installing grafana\n")

	r := l.mustRun("init")
	for _, want := range []string{"Grafana cannot query Prometheus (dial tcp: lookup prometheus)", "Re-applying Grafana", "=== Lab is up ==="} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("init does not print %q:\n%s", want, r.stdout)
		}
	}
}

// Teardown undoes everything in order and ends by destroying the cluster.
func TestTeardown(t *testing.T) {
	l := newTerminal(t)
	l.mustRun("scenario", "up", labtest.Scenario)
	l.mustRun("incident", "inject", labtest.Fault)

	r := l.mustRun("teardown")
	want := []string{
		"Deactivated scenarios: demo-scenario", "Deactivated incident: demo-fault",
		"deploy destroy demo-app", "removing grafana", "removing traefik", "cluster down",
	}
	at := 0
	for _, w := range want {
		i := strings.Index(r.stdout[at:], w)
		if i < 0 {
			t.Fatalf("teardown does not print %q after the previous step:\n%s", w, r.stdout)
		}
		at += i
	}
	if status := l.mustRun("scenario", "status"); !strings.Contains(status.stdout, "No scenarios are currently active") {
		t.Errorf("a scenario is still active after teardown:\n%s", status.stdout)
	}
	l.golden("teardown", r)
}

// A platform component that will not uninstall does not stop the teardown:
// the cluster is destroyed next, and the warning says what was left.
func TestTeardown_KeepsGoingWhenAnUninstallFails(t *testing.T) {
	l := newTerminal(t)
	l.Write(t, "platform/monitoring/grafana/uninstall.sh", "#!/usr/bin/env bash\nexit 3\n")
	r := l.mustRun("teardown")
	if !strings.Contains(r.stdout, "Warning: the platform was not fully uninstalled") || !strings.Contains(r.stdout, "cluster down") {
		t.Errorf("teardown does not warn about the failed uninstall and still destroy the cluster:\n%s", r.stdout)
	}
}

// `labctl reset` is teardown then init.
func TestReset_TearsDownThenBuilds(t *testing.T) {
	l := newTerminal(t)
	l.serveGrafana("OK")
	quickWaits(t)
	r := l.mustRun("reset")
	down, up := strings.Index(r.stdout, "cluster down"), strings.Index(r.stdout, "cluster up")
	if down < 0 || up < down || !strings.Contains(r.stdout, "=== Lab is up ===") {
		t.Errorf("reset does not tear down, then build, then report the lab up:\n%s", r.stdout)
	}
}
