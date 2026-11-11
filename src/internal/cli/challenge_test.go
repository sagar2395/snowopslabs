// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"strings"
	"testing"

	"github.com/snowops-oss/snowopslabs/internal/challenge"
	"github.com/snowops-oss/snowopslabs/internal/labtest"
)

// A timed challenge at the terminal: start breaks the lab, a submit on the
// broken lab is scored and keeps the clock running, a hint costs points, and
// a submit on the fixed lab records the pass and cleans up.
func TestChallenge_Journey(t *testing.T) {
	l := newTerminal(t)
	var steps []cliResult
	step := func(args ...string) cliResult {
		t.Helper()
		r := l.mustRun(args...)
		steps = append(steps, r)
		return r
	}

	step("challenge", "list")
	step("challenge", "info", labtest.Challenge)
	step("challenge", "status")
	step("challenge", "history")

	step("challenge", "start", labtest.Challenge)
	if !l.exists(faultMarker) {
		t.Fatal("start reported success but did not break the lab")
	}
	step("challenge", "status")

	failed := step("challenge", "submit")
	for _, want := range []string{"✗ marker-gone", "Checks: 0/1 passed", "Score:  0/100", "Outcome: failed", "Still running"} {
		if !strings.Contains(failed.stdout, want) {
			t.Errorf("a submit on the broken lab does not print %q:\n%s", want, failed.stdout)
		}
	}
	if r := step("challenge", "history"); !strings.Contains(r.stdout, "No challenge history yet") {
		t.Errorf("a failed submit was recorded in history:\n%s", r.stdout)
	}

	step("challenge", "hint")
	l.remove(faultMarker) // the learner's own fix
	passed := step("challenge", "submit")
	for _, want := range []string{"✓ marker-gone", "Checks: 1/1 passed", "Hints:  1 used", "Score:  95/100", "Outcome: passed"} {
		if !strings.Contains(passed.stdout, want) {
			t.Errorf("a submit on the fixed lab does not print %q:\n%s", want, passed.stdout)
		}
	}

	if r := step("challenge", "status"); !strings.Contains(r.stdout, "No challenge is currently active") {
		t.Errorf("the challenge is still running after it passed:\n%s", r.stdout)
	}
	if r := step("incident", "status"); !strings.Contains(r.stdout, "No incident is active") {
		t.Errorf("the challenge's fault is still active after it passed:\n%s", r.stdout)
	}
	if r := step("challenge", "history"); !strings.Contains(r.stdout, "passed") || !strings.Contains(r.stdout, "95") {
		t.Errorf("history does not show the pass with its score:\n%s", r.stdout)
	}
	l.golden("challenge_journey", steps...)
}

// Giving up undoes the setup and records the run as aborted with no score.
func TestChallenge_Abort(t *testing.T) {
	l := newTerminal(t)
	if r := l.mustRun("challenge", "abort"); !strings.Contains(r.stdout, "No active challenge") {
		t.Errorf("abort with nothing running = %q", r.stdout)
	}

	l.mustRun("challenge", "start", labtest.Challenge)
	abort := l.mustRun("challenge", "abort")
	if l.exists(faultMarker) {
		t.Error("abort left the lab broken")
	}
	history := l.mustRun("challenge", "history")
	if !strings.Contains(history.stdout, "aborted") {
		t.Errorf("history does not record the abort:\n%s", history.stdout)
	}
	status := l.mustRun("challenge", "status")
	l.golden("challenge_abort", abort, history, status)
}

// --force starts over an already running challenge; without it the second
// start is refused before it touches the lab.
func TestChallenge_StartWhileOneIsRunning(t *testing.T) {
	l := newTerminal(t)
	l.mustRun("challenge", "start", labtest.Challenge)

	second := l.run("challenge", "start", labtest.Challenge)
	if second.exitCode() != 1 || !strings.Contains(second.stderr, "already active") {
		t.Errorf("a second start = exit %d, stderr %q; want it refused as already active", second.exitCode(), second.stderr)
	}
	if r := l.mustRun("challenge", "status"); !strings.Contains(r.stdout, "Challenge: "+labtest.Challenge) {
		t.Errorf("the refused start ended the running challenge:\n%s", r.stdout)
	}
}

func TestChallenge_Failures(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, l *terminal)
		args    []string
		wantErr string
	}{
		{"info on a challenge that does not exist", nil, []string{"challenge", "info", "ghost"}, "see 'labctl challenge list'"},
		{"start a challenge that does not exist", nil, []string{"challenge", "start", "ghost"}, "see 'labctl challenge list'"},
		{"submit with nothing running", nil, []string{"challenge", "submit"}, "labctl challenge start <name>"},
		{"hint with nothing running", nil, []string{"challenge", "hint"}, "labctl challenge start <name>"},
		{"--app on a challenge", nil, []string{"challenge", "start", labtest.Challenge, "--app", labtest.App}, "--app does not apply to labctl challenge start"},
		{"a challenge whose YAML is broken",
			func(t *testing.T, l *terminal) { l.Write(t, "challenges/broken/challenge.yaml", "name: [unclosed") },
			[]string{"challenge", "info", "broken"}, "labctl validate"},
		{"a challenge missing required fields",
			func(t *testing.T, l *terminal) { l.Write(t, "challenges/thin/challenge.yaml", "name: thin\n") },
			[]string{"challenge", "info", "thin"}, "displayName is required"},
		{"start when the app is not deployed",
			func(_ *testing.T, l *terminal) { l.undeploy("go-api") },
			[]string{"challenge", "start", labtest.Challenge}, "labctl app build go-api && labctl app deploy go-api"},
		{"start when the setup fails",
			func(t *testing.T, l *terminal) {
				l.Write(t, "incidents/"+labtest.Fault+"/inject.sh", "#!/usr/bin/env bash\nexit 4\n")
			},
			[]string{"challenge", "start", labtest.Challenge}, "setup failed"},
		{"a hint on a challenge set up by a scenario",
			func(t *testing.T, l *terminal) {
				l.Write(t, "challenges/by-scenario/challenge.yaml",
					"name: by-scenario\ndisplayName: By scenario\nsetup: {type: scenario, ref: "+labtest.Scenario+"}\n"+
						"grading:\n  checks:\n    - {name: marker, type: script, script: scenarios/"+labtest.Scenario+"/checks/marker.sh}\n")
				l.mustRun("challenge", "start", "by-scenario")
			},
			[]string{"challenge", "hint"}, "hints not available for scenario-type challenges"},
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
			if strings.Contains(r.stderr, l.Root) {
				t.Errorf("the error shows a path inside the lab instead of what to do: %q", r.stderr)
			}
		})
	}
}

// A setup that fails part-way is undone, and no run is left recorded.
func TestChallenge_FailedSetupIsUndone(t *testing.T) {
	l := newTerminal(t)
	l.Write(t, "incidents/"+labtest.Fault+"/inject.sh",
		"#!/usr/bin/env bash\ntouch \"$(dirname \"$0\")/BROKEN\"\nexit 4\n")

	if r := l.run("challenge", "start", labtest.Challenge); r.err == nil {
		t.Fatal("the failing start reported success")
	}
	if l.exists(faultMarker) {
		t.Error("a failed setup left the lab broken")
	}
	if r := l.mustRun("challenge", "status"); !strings.Contains(r.stdout, "No challenge is currently active") {
		t.Errorf("a failed setup left a challenge running:\n%s", r.stdout)
	}
}

// A hint taken with `labctl incident hint` during a challenge costs the same
// as one taken with `labctl challenge hint`.
func TestChallenge_IncidentHintsAreCharged(t *testing.T) {
	l := newTerminal(t)
	l.mustRun("challenge", "start", labtest.Challenge)
	l.mustRun("incident", "hint")
	l.mustRun("challenge", "hint")
	if r := l.mustRun("challenge", "status"); !strings.Contains(r.stdout, "Hints used: 2") {
		t.Errorf("status does not count both hints:\n%s", r.stdout)
	}
}

func TestSetupSummary(t *testing.T) {
	tests := []struct {
		name string
		c    challenge.Challenge
		want string
	}{
		{"one fault", challenge.Challenge{Setup: challenge.SetupSpec{Type: "incident", Ref: "oom-kill"}}, "incident oom-kill"},
		{"several faults", challenge.Challenge{Setup: challenge.SetupSpec{Type: "incident", Refs: []string{"a", "b"}}}, "incident a, b"},
		{"silent: the count, not the names", challenge.Challenge{Silent: true, Setup: challenge.SetupSpec{Type: "incident", Refs: []string{"a", "b"}}},
			"2 incidents, not named: finding them is the exercise"},
		{"silent, one fault", challenge.Challenge{Silent: true, Setup: challenge.SetupSpec{Type: "incident", Ref: "a"}},
			"1 incident, not named: finding them is the exercise"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := setupSummary(&tt.c); got != tt.want {
				t.Errorf("setupSummary() = %q, want %q", got, tt.want)
			}
		})
	}
}
