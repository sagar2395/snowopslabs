// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"strings"
	"testing"

	"github.com/snowops-oss/snowopslabs/internal/labtest"
)

func TestService_Journey(t *testing.T) {
	l := newTerminal(t)
	steps := []cliResult{
		l.mustRun("service", "list"),
		l.mustRun("service", "up", labtest.Service),
		l.mustRun("service", "status"),
		l.mustRun("service", "status", labtest.Service),
		l.mustRun("service", "down", labtest.Service),
	}
	for i, want := range []string{labtest.Service, "installing pager", "running", "running", "removing pager"} {
		if !strings.Contains(steps[i].stdout, want) {
			t.Errorf("`labctl %s` does not print %q:\n%s", strings.Join(steps[i].args, " "), want, steps[i].stdout)
		}
	}
	l.golden("service_journey", steps...)
}

func TestService_Failures(t *testing.T) {
	failing := func(script string) func(t *testing.T, l *terminal) {
		return func(t *testing.T, l *terminal) {
			l.Write(t, "src/services/"+labtest.Service+"/"+script, "#!/usr/bin/env bash\necho 'no such release' >&2\nexit 7\n")
		}
	}
	tests := []struct {
		name    string
		prepare func(t *testing.T, l *terminal)
		args    []string
		wantErr string
	}{
		{"up a service that does not exist", nil, []string{"service", "up", "ghost"}, `service "ghost" not found`},
		{"down a service that does not exist", nil, []string{"service", "down", "ghost"}, `service "ghost" not found`},
		{"status of a service that does not exist", nil, []string{"service", "status", "ghost"}, `service "ghost" not found`},
		{"up when the install script fails", failing("install.sh"), []string{"service", "up", labtest.Service}, "service install failed"},
		{"down when the uninstall script fails", failing("uninstall.sh"), []string{"service", "down", labtest.Service}, "service uninstall failed"},
		{"up with no name", nil, []string{"service", "up"}, "accepts 1 arg(s), received 0"},
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
			for _, lie := range []string{"installed.", "uninstalled."} {
				if strings.Contains(r.stdout, lie) {
					t.Errorf("the output says %q after a failure:\n%s", lie, r.stdout)
				}
			}
		})
	}
}

// A lab with no shared services says so instead of printing an empty table.
func TestService_NoneDefined(t *testing.T) {
	l := newTerminal(t)
	l.remove("src/services/" + labtest.Service + "/install.sh")
	l.remove("src/services/" + labtest.Service + "/uninstall.sh")
	l.remove("src/services/" + labtest.Service + "/status.sh")
	l.remove("src/services/" + labtest.Service)
	for _, args := range [][]string{{"service", "list"}, {"service", "status"}} {
		if r := l.mustRun(args...); !strings.Contains(r.stdout, "No shared services found.") {
			t.Errorf("`labctl %s` = %q", strings.Join(args, " "), r.stdout)
		}
	}
}
