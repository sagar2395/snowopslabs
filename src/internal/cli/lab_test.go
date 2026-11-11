// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/snowops-oss/snowopslabs/internal/config"
	"github.com/snowops-oss/snowopslabs/internal/lab"
	"github.com/snowops-oss/snowopslabs/internal/labtest"
)

// The lab's own lifecycle at the terminal: bring the cluster up, snapshot
// what runs, restore the snapshot, reset, and take the cluster down.
func TestLab_Journey(t *testing.T) {
	l := newTerminal(t)
	var steps []cliResult
	step := func(args ...string) cliResult {
		t.Helper()
		r := l.mustRun(args...)
		steps = append(steps, r)
		return r
	}

	step("lab", "status")
	step("lab", "up")
	if r := step("lab", "status"); !strings.Contains(r.stdout, "State:     up") {
		t.Errorf("status after up does not say the lab is up:\n%s", r.stdout)
	}
	if r := step("lab", "status", "--live"); !strings.Contains(r.stdout, "Cluster:   reachable") {
		t.Errorf("status --live does not report the answering cluster:\n%s", r.stdout)
	}

	step("lab", "snapshots")
	l.mustRun("scenario", "up", labtest.Scenario)
	taken := step("lab", "snapshot", "before-drill")
	for _, want := range []string{"apps:      demo-app, go-api", "scenarios: demo-scenario"} {
		if !strings.Contains(taken.stdout, want) {
			t.Errorf("the snapshot does not record %q:\n%s", want, taken.stdout)
		}
	}
	step("lab", "snapshots")

	restored := step("lab", "restore", "before-drill")
	for _, want := range []string{"app-deploy demo-app", "app-deploy go-api", "Restore complete"} {
		if !strings.Contains(restored.stdout, want) {
			t.Errorf("restore does not print %q:\n%s", want, restored.stdout)
		}
	}

	l.stdin = "no\n"
	if r := step("lab", "reset"); !strings.Contains(r.stdout, "Aborted.") {
		t.Errorf("reset ran without confirmation:\n%s", r.stdout)
	}
	if r := l.mustRun("scenario", "status", labtest.Scenario); !strings.Contains(r.stdout, "active") {
		t.Errorf("a declined reset deactivated the scenario:\n%s", r.stdout)
	}
	reset := step("lab", "reset", "--yes")
	for _, want := range []string{"scenario-down demo-scenario", "app-destroy demo-app", "traffic-stop", "Lab reset to post-init state."} {
		if !strings.Contains(reset.stdout, want) {
			t.Errorf("reset does not print %q:\n%s", want, reset.stdout)
		}
	}
	if r := l.mustRun("scenario", "status", labtest.Scenario); !strings.Contains(r.stdout, "inactive") {
		t.Errorf("reset left the scenario active:\n%s", r.stdout)
	}

	step("lab", "delete", "before-drill")
	step("lab", "snapshots")
	step("lab", "down")
	if r := step("lab", "status"); !strings.Contains(r.stdout, "State:     down") {
		t.Errorf("status after down does not say the lab is down:\n%s", r.stdout)
	}
	l.golden("lab_journey", steps...)
}

// A reset also clears an active incident, so the next inject is not refused.
func TestLab_ResetClearsTheActiveIncident(t *testing.T) {
	l := newTerminal(t)
	l.mustRun("incident", "inject", labtest.Fault)
	if r := l.mustRun("lab", "reset", "--yes"); !strings.Contains(r.stdout, "Deactivated incident: "+labtest.Fault) {
		t.Errorf("reset does not say it cleared the incident:\n%s", r.stdout)
	}
	if r := l.mustRun("incident", "status"); !strings.Contains(r.stdout, "No incident is active") {
		t.Errorf("an incident is still active after the reset:\n%s", r.stdout)
	}
}

// A reset step that fails does not stop the rest, and the command reports it.
func TestLab_ResetKeepsGoingPastAFailedStep(t *testing.T) {
	l := newTerminal(t)
	l.Write(t, "src/engine/deploy.sh", "#!/usr/bin/env bash\nif [ \"$2\" = demo-app ]; then exit 5; fi\necho deploy \"$@\"\n")
	r := l.run("lab", "reset", "--yes")
	if r.exitCode() != 1 {
		t.Errorf("exit code = %d, want 1 when a step failed", r.exitCode())
	}
	if !strings.Contains(r.stdout, "deploy destroy go-api") || !strings.Contains(r.stdout, "traffic stopped") {
		t.Errorf("the steps after the failed one did not run:\n%s", r.stdout)
	}
	if strings.Contains(r.stdout, "Lab reset to post-init state.") {
		t.Errorf("a reset with a failed step reports the lab as reset:\n%s", r.stdout)
	}
}

func TestLab_Failures(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, l *terminal)
		args    []string
		wantErr string
	}{
		{"restore a snapshot that does not exist", nil, []string{"lab", "restore", "ghost"}, `snapshot "ghost" not found`},
		{"delete a snapshot that does not exist", nil, []string{"lab", "delete", "ghost"}, `snapshot "ghost" not found`},
		{"a snapshot name that is not a valid name", nil, []string{"lab", "snapshot", "../escape"}, "escape"},
		{"up for a runtime that does not exist", nil, []string{"lab", "up", "ghost"}, "ghost"},
		{"up when the runtime script fails",
			func(t *testing.T, l *terminal) {
				l.Write(t, "runtimes/k3d/up.sh", "#!/usr/bin/env bash\necho 'docker is not running' >&2\nexit 2\n")
			},
			[]string{"lab", "up"}, "lab up failed"},
		{"restore when a step fails",
			func(t *testing.T, l *terminal) {
				l.mustRun("lab", "snapshot", "s")
				l.Write(t, "src/engine/deploy.sh", "#!/usr/bin/env bash\nexit 5\n")
			},
			[]string{"lab", "restore", "s"}, "app-deploy demo-app"},
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
		})
	}
}

// A snapshot with nothing in it restores nothing, and says so instead of
// printing an empty plan.
func TestLab_RestoreEmptySnapshot(t *testing.T) {
	l := newTerminal(t)
	stateDir, err := config.StateDir("clitest")
	if err != nil {
		t.Fatal(err)
	}
	if err := lab.NewStore(stateDir).Save(&lab.Snapshot{Name: "empty", TakenAt: time.Now()}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	r := l.mustRun("lab", "restore", "empty")
	if !strings.Contains(r.stdout, "Snapshot is empty - nothing to restore.") {
		t.Errorf("restore of an empty snapshot does not say so:\n%s", r.stdout)
	}
	if strings.Contains(r.stdout, "Restoring") {
		t.Errorf("restore of an empty snapshot started a plan:\n%s", r.stdout)
	}
}
