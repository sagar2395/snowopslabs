// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"strings"
	"testing"

	"github.com/snowops-oss/snowopslabs/internal/labtest"
)

func TestApp_Journey(t *testing.T) {
	l := newTerminal(t)
	steps := []cliResult{
		l.mustRun("app", "list"),
		l.mustRun("app", "capabilities"),
		l.mustRun("app", "verify", labtest.App, "--static"),
		l.mustRun("app", "build", labtest.App),
		l.mustRun("app", "deploy", labtest.App),
		l.mustRun("app", "destroy", labtest.App),
	}
	for i, want := range []string{"", "", "", "build demo-app", "deploy deploy demo-app", "deploy destroy demo-app"} {
		if !strings.Contains(steps[i].stdout, want) {
			t.Errorf("`labctl %s` did not run the engine script with %q:\n%s", strings.Join(steps[i].args, " "), want, steps[i].stdout)
		}
	}
	l.golden("app_journey", steps...)
}

// deployedAs makes kubectl answer the contract's questions about the
// Deployment: its ready replicas, its port and its probe paths.
func deployedAs(t *testing.T, l *terminal, port, health, ready string) {
	t.Helper()
	l.Stub(t, "kubectl", `#!/bin/sh
case "$*" in
  *"jsonpath={.status.readyReplicas}"*) printf 2 ;;
  *containerPort*) printf '%s' "`+port+`" ;;
  *livenessProbe*) printf '%s' "`+health+`" ;;
  *readinessProbe*) printf '%s' "`+ready+`" ;;
esac
`)
}

// An app is verified against what its Deployment really runs: ready replicas,
// the port and the probe paths its app.env declares.
func TestApp_Verify(t *testing.T) {
	tests := []struct {
		name                string
		port, health, ready string
		wantPass            bool
		wantFailed          string
	}{
		{"a Deployment that matches the declaration", "8080", "/health", "/ready", true, ""},
		{"a Deployment serving another port", "9090", "/health", "/ready", false, "serves-contract-port"},
		{"a Deployment probed on another path", "8080", "/healthz", "/ready", false, "liveness-probe-path"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newTerminal(t)
			deployedAs(t, l, tt.port, tt.health, tt.ready)
			r := l.run("app", "verify", labtest.App)
			confirmed := strings.Contains(r.stdout, "✓ demo-app honours its declared contract")
			if tt.wantPass {
				if r.err != nil || !confirmed {
					t.Errorf("err = %v; verify does not confirm the contract:\n%s%s", r.err, r.stdout, r.stderr)
				}
				return
			}
			if r.exitCode() != 1 || !strings.Contains(r.stderr, `app "demo-app" does not honour its declared contract`) {
				t.Errorf("exit %d, stderr %q; want the contract reported as broken", r.exitCode(), r.stderr)
			}
			if confirmed {
				t.Errorf("a Deployment that breaks the contract was confirmed:\n%s", r.stdout)
			}
			if !strings.Contains(r.stdout, "FAIL  "+tt.wantFailed) {
				t.Errorf("the output does not name the failed check %s:\n%s", tt.wantFailed, r.stdout)
			}
		})
	}
}

// With no Deployment to ask, nothing is confirmed.
func TestApp_VerifyWhenNotDeployed(t *testing.T) {
	l := newTerminal(t)
	l.undeploy(labtest.App)
	r := l.run("app", "verify", labtest.App)
	if r.exitCode() != 1 || strings.Contains(r.stdout, "honours its declared contract") {
		t.Errorf("exit %d; an app that is not deployed was verified:\n%s", r.exitCode(), r.stdout)
	}
	l.golden("app_verify_not_deployed", r)
}

// The capabilities an app declares are part of its printed contract.
func TestApp_VerifyStaticShowsDeclaredCapabilities(t *testing.T) {
	l := newTerminal(t)
	l.Write(t, "apps/metered/app.env", "APP_NAME=metered\nAPP_CAPABILITIES=prometheus-metrics\n")
	r := l.mustRun("app", "verify", "metered", "--static")
	for _, want := range []string{"capabilities:   prometheus-metrics", "metrics:", "request metric:"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("the declaration does not show %q:\n%s", want, r.stdout)
		}
	}
}

func TestApp_Failures(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, l *terminal)
		args    []string
		wantErr string
	}{
		{"build an app that does not exist", nil, []string{"app", "build", "ghost"}, "available apps: demo-app, go-api"},
		{"deploy an app that does not exist", nil, []string{"app", "deploy", "ghost"}, "available apps: demo-app, go-api"},
		{"destroy an app that does not exist", nil, []string{"app", "destroy", "ghost"}, "available apps: demo-app, go-api"},
		{"verify an app that does not exist", nil, []string{"app", "verify", "ghost", "--static"}, "available apps: demo-app, go-api"},
		{"an app that declares a capability nothing defines",
			func(t *testing.T, l *terminal) {
				l.Write(t, "apps/odd/app.env", "APP_NAME=odd\nAPP_CAPABILITIES=time-travel\n")
			},
			[]string{"app", "verify", "odd", "--static"}, "time-travel"},
		{"build when the engine script fails",
			func(t *testing.T, l *terminal) {
				l.Write(t, "src/engine/build.sh", "#!/usr/bin/env bash\necho 'docker build failed' >&2\nexit 3\n")
			},
			[]string{"app", "build", labtest.App}, "exit status 3"},
		{"deploy when the engine script fails",
			func(t *testing.T, l *terminal) {
				l.Write(t, "src/engine/deploy.sh", "#!/usr/bin/env bash\necho 'helm upgrade failed' >&2\nexit 3\n")
			},
			[]string{"app", "deploy", labtest.App}, "exit status 3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newTerminal(t)
			if tt.prepare != nil {
				tt.prepare(t, l)
			}
			r := l.run(tt.args...)
			if r.exitCode() != 1 || !strings.Contains(r.stderr, tt.wantErr) {
				t.Errorf("exit %d, stderr %q; want exit 1 saying %q", r.exitCode(), r.stderr, tt.wantErr)
			}
			if tt.prepare == nil && strings.Contains(r.stdout, "deploy ") {
				t.Errorf("the engine script was run for an app that does not exist:\n%s", r.stdout)
			}
		})
	}
}

func TestApp_ListWithNoApps(t *testing.T) {
	l := newTerminal(t)
	l.remove("apps/demo-app/app.env")
	l.remove("apps/demo-app")
	l.remove("apps/go-api/app.env")
	l.remove("apps/go-api")
	if r := l.mustRun("app", "list"); !strings.Contains(r.stdout, "No applications found in apps/") {
		t.Errorf("list with no apps = %q", r.stdout)
	}
}
