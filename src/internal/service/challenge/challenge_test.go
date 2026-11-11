// SPDX-License-Identifier: Apache-2.0

package challenge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snowops-oss/snowopslabs/internal/challenge"
	"github.com/snowops-oss/snowopslabs/internal/config"
	"github.com/snowops-oss/snowopslabs/internal/executor"
	"github.com/snowops-oss/snowopslabs/internal/incident"
	"github.com/snowops-oss/snowopslabs/internal/scenario"
)

const script = "#!/usr/bin/env bash\nset -eu\n"

// The lab is broken while a BROKEN marker exists: the fault's inject.sh and
// the scenario's setup.sh create it, resolve.sh and teardown.sh remove it, and
// every grading check passes only once it is gone.
var labFiles = map[string]string{
	"incidents/demo-fault/fault.yaml": `name: demo-fault
displayName: "Demo fault"
description: "the demo app is broken"
category: workload
severity: low
target:
  namespace: demo
  workload: demo
detection:
  name: marker-gone
  type: script
  script: checks/resolved.sh
  timeoutSeconds: 5
`,
	"incidents/demo-fault/inject.sh":          script + "touch \"$PROJECT_ROOT/BROKEN\"\n",
	"incidents/demo-fault/resolve.sh":         script + "rm -f \"$PROJECT_ROOT/BROKEN\"\necho resolved >>\"$PROJECT_ROOT/CLEANED\"\n",
	"incidents/demo-fault/checks/resolved.sh": script + "[ ! -f \"$PROJECT_ROOT/BROKEN\" ]\n",
	"incidents/demo-fault/hints.md":           "## Hint 1\nlook at the pods\n",
	"incidents/demo-fault/solution.md":        "# Solution\nroll it back\n",

	// A fault aimed at a namespace of its own: its check needs the target the
	// fault declares, as `incident status` passes it.
	"incidents/batch-fault/fault.yaml": `name: batch-fault
displayName: "Batch fault"
description: "the batch tenant is broken"
category: resources
severity: low
target:
  namespace: batch
  workload: report
detection:
  name: batch-marker-gone
  type: script
  script: checks/resolved.sh
  timeoutSeconds: 5
`,
	"incidents/batch-fault/inject.sh":          script + "touch \"$PROJECT_ROOT/BATCH\"\n",
	"incidents/batch-fault/resolve.sh":         script + "rm -f \"$PROJECT_ROOT/BATCH\"\necho resolved >>\"$PROJECT_ROOT/CLEANED\"\n",
	"incidents/batch-fault/checks/resolved.sh": script + "[ \"${TARGET_NAMESPACE:-}/${TARGET_WORKLOAD:-}\" = batch/report ] && [ ! -f \"$PROJECT_ROOT/BATCH\" ]\n",
	"incidents/batch-fault/hints.md":           "## Hint 1\nlook at the batch namespace\n## Hint 2\nthe report job\n",
	"incidents/batch-fault/solution.md":        "# Solution\nstop it\n",

	"scenarios/demo-scenario/scenario.yaml": `apiVersion: scenario.snowops.net/v2
name: demo-scenario
displayName: Demo scenario
description: A scenario
category: reliability
components:
  - name: setup
    type: script
    script: setup.sh
    uninstallScript: teardown.sh
checks:
  - name: marker-gone
    type: script
    script: checks/fixed.sh
    timeoutSeconds: 5
    remediation: fix it
`,
	"scenarios/demo-scenario/setup.sh":        script + "touch \"$PROJECT_ROOT/BROKEN\"\n",
	"scenarios/demo-scenario/teardown.sh":     script + "rm -f \"$PROJECT_ROOT/BROKEN\"\necho down >>\"$PROJECT_ROOT/CLEANED\"\n",
	"scenarios/demo-scenario/checks/fixed.sh": script + "[ ! -f \"$PROJECT_ROOT/BROKEN\" ]\n",

	"checks/fixed.sh":  script + "[ ! -f \"$PROJECT_ROOT/BROKEN\" ]\n",
	"checks/always.sh": script + "exit 0\n",

	"challenges/by-detection/challenge.yaml": `name: by-detection
displayName: "Graded by the fault's check"
parTime: "10m"
setup: {type: incident, ref: demo-fault}
grading: {useDetectionCheck: true}
`,
	"challenges/by-checks/challenge.yaml": `name: by-checks
displayName: "Graded by its own checks"
parTime: "10m"
setup: {type: incident, ref: demo-fault}
grading:
  checks:
    - {name: always, type: script, script: checks/always.sh, timeoutSeconds: 5}
    - {name: fixed, type: script, script: checks/fixed.sh, timeoutSeconds: 5}
`,
	"challenges/by-scenario/challenge.yaml": `name: by-scenario
displayName: "Set up by a scenario"
parTime: "10m"
setup: {type: scenario, ref: demo-scenario}
grading:
  checks:
    - {name: fixed, type: script, script: checks/fixed.sh, timeoutSeconds: 5}
`,
	"challenges/batch/challenge.yaml": `name: batch
displayName: "Graded by a fault with its own target"
parTime: "10m"
setup: {type: incident, ref: batch-fault}
grading: {useDetectionCheck: true}
`,
	"challenges/shift/challenge.yaml": `name: shift
displayName: "Two faults at once"
parTime: "10m"
silent: true
setup: {type: incident, refs: [demo-fault, batch-fault]}
grading: {useDetectionCheck: true}
`,
	"challenges/no-checks/challenge.yaml": `name: no-checks
displayName: "A detection check on a scenario resolves to nothing"
setup: {type: scenario, ref: demo-scenario}
grading: {useDetectionCheck: true}
`,
	"challenges/ghost-fault/challenge.yaml": `name: ghost-fault
displayName: "Names a fault that does not exist"
setup: {type: incident, ref: ghost}
grading: {useDetectionCheck: true}
`,
	"challenges/odd-setup/challenge.yaml": `name: odd-setup
displayName: "Names a setup type nothing implements"
setup: {type: wizardry, ref: demo-fault}
grading:
  checks:
    - {name: always, type: script, script: checks/always.sh, timeoutSeconds: 5}
`,
}

type lab struct {
	root   string
	grader *Grader
}

func newLab(t *testing.T) *lab {
	t.Helper()
	root := t.TempDir()
	for rel, content := range labFiles {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(rel, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(path, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
	state := filepath.Join(root, ".labctl")
	cfg := &config.Config{ProjectRoot: root, StateDir: state, DomainSuffix: "lab.test", MonitoringNamespace: "monitoring"}
	ex := executor.New(root)
	ex.SetEnv("PROJECT_ROOT", root)
	return &lab{root: root, grader: &Grader{
		Engine:    challenge.New(filepath.Join(root, "challenges"), filepath.Join(state, "challenges"), filepath.Join(state, "history")),
		Scenarios: scenario.NewEngine(root, cfg.DomainSuffix, "k3d"),
		Incidents: incident.NewEngine(root, cfg.DomainSuffix),
		Config:    cfg,
		Exec:      ex,
	}}
}

// start sets the lab up for a challenge and starts its timer, as `labctl
// challenge start` does.
func (l *lab) start(t *testing.T, name string) *challenge.Challenge {
	t.Helper()
	c, err := l.grader.Engine.Load(name)
	if err != nil {
		t.Fatalf("Load(%s): %v", name, err)
	}
	if err := l.grader.Setup(c); err != nil {
		t.Fatalf("Setup(%s): %v", name, err)
	}
	if _, err := l.grader.Engine.Start(name, false); err != nil {
		t.Fatalf("Start(%s): %v", name, err)
	}
	return c
}

func (l *lab) exists(rel string) bool {
	_, err := os.Stat(filepath.Join(l.root, rel))
	return err == nil
}

// fix is the learner repairing the lab by hand.
func (l *lab) fix(t *testing.T) {
	t.Helper()
	if err := os.Remove(filepath.Join(l.root, "BROKEN")); err != nil {
		t.Fatalf("fixing the lab: %v", err)
	}
}

func TestGrader_Submit(t *testing.T) {
	tests := []struct {
		name      string
		challenge string
		fixed     bool
		wantPass  bool
		wantOK    int
		wantTotal int
	}{
		{"detection check, lab still broken", "by-detection", false, false, 0, 1},
		{"detection check, lab fixed", "by-detection", true, true, 1, 1},
		{"own checks, lab still broken: the check that always passes earns no pass", "by-checks", false, false, 1, 2},
		{"own checks, lab fixed", "by-checks", true, true, 2, 2},
		{"scenario setup, lab still broken", "by-scenario", false, false, 0, 1},
		{"scenario setup, lab fixed", "by-scenario", true, true, 1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newLab(t)
			l.start(t, tt.challenge)
			if !l.exists("BROKEN") {
				t.Fatal("the setup did not break the lab; the test would prove nothing")
			}
			if tt.fixed {
				l.fix(t)
			}

			sub, err := l.grader.Submit(t.Context(), "olive")
			if err != nil {
				t.Fatalf("Submit() error = %v", err)
			}
			if sub.Passed != tt.wantPass {
				t.Errorf("Passed = %v, want %v", sub.Passed, tt.wantPass)
			}
			if sub.Record.ChecksPassed != tt.wantOK || sub.Record.ChecksTotal != tt.wantTotal {
				t.Errorf("checks = %d/%d, want %d/%d", sub.Record.ChecksPassed, sub.Record.ChecksTotal, tt.wantOK, tt.wantTotal)
			}
			if len(sub.Checks) != tt.wantTotal {
				t.Errorf("got %d check results, want %d", len(sub.Checks), tt.wantTotal)
			}

			active, err := l.grader.Engine.Active()
			if err != nil {
				t.Fatal(err)
			}
			history, err := l.grader.Engine.History()
			if err != nil {
				t.Fatal(err)
			}
			if tt.wantPass {
				if sub.Record.Outcome != "passed" || sub.Record.Score != 100 {
					t.Errorf("record = %s with score %d, want passed with 100", sub.Record.Outcome, sub.Record.Score)
				}
				if active != nil {
					t.Error("the run is still active after a pass")
				}
				if len(history) != 1 || history[0].Outcome != "passed" {
					t.Errorf("history = %+v, want one passed run", history)
				}
				if sub.CleanupErr != nil {
					t.Errorf("CleanupErr = %v", sub.CleanupErr)
				}
				if !l.exists("CLEANED") {
					t.Error("the setup was not undone after a pass")
				}
				return
			}
			if sub.Record.Outcome != "failed" || sub.Record.Score == 100 {
				t.Errorf("record = %s with score %d, want failed with less than 100", sub.Record.Outcome, sub.Record.Score)
			}
			if active == nil {
				t.Error("a failed attempt ended the run; the learner could not submit again")
			}
			if len(history) != 0 {
				t.Errorf("a failed attempt was recorded in history: %+v", history)
			}
			if l.exists("CLEANED") {
				t.Error("a failed attempt undid the setup")
			}
		})
	}
}

func TestGrader_Submit_RetryAfterAFailedAttemptPasses(t *testing.T) {
	l := newLab(t)
	l.start(t, "by-detection")

	first, err := l.grader.Submit(t.Context(), "olive")
	if err != nil || first.Passed {
		t.Fatalf("first Submit() = passed %v, err %v; want a failed attempt", first != nil && first.Passed, err)
	}
	l.fix(t)
	second, err := l.grader.Submit(t.Context(), "olive")
	if err != nil {
		t.Fatalf("second Submit() error = %v", err)
	}
	if !second.Passed {
		t.Error("the fixed lab did not pass on the second submit")
	}
}

func TestGrader_Submit_Refused(t *testing.T) {
	tests := []struct {
		name      string
		challenge string // started without its setup; "" starts nothing
		wantErr   error
		wantText  string
	}{
		{"no challenge is running", "", challenge.ErrNoActive, ""},
		{"grading resolves to no checks", "no-checks", ErrNoChecks, "no-checks"},
		{"the fault it grades with is gone", "ghost-fault", nil, "loading detection check for ghost"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newLab(t)
			if tt.challenge != "" {
				if _, err := l.grader.Engine.Start(tt.challenge, false); err != nil {
					t.Fatal(err)
				}
			}
			sub, err := l.grader.Submit(t.Context(), "olive")
			if err == nil {
				t.Fatalf("Submit() = %+v, want an error", sub)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("Submit() error = %v, want %v", err, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantText) {
				t.Errorf("Submit() error = %q, want it to mention %q", err, tt.wantText)
			}
			if history, _ := l.grader.Engine.History(); len(history) != 0 {
				t.Errorf("a refused submit was recorded: %+v", history)
			}
		})
	}
}

// A request that is cancelled mid-grading fails its checks; that must never
// be recorded as a pass.
func TestGrader_Submit_CancelledContextIsNotAPass(t *testing.T) {
	l := newLab(t)
	l.start(t, "by-detection")
	l.fix(t)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	sub, err := l.grader.Submit(ctx, "olive")
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if sub.Passed {
		t.Error("a cancelled submit was recorded as a pass")
	}
	if active, _ := l.grader.Engine.Active(); active == nil {
		t.Error("a cancelled submit ended the run")
	}
}

func TestGrader_Submit_CleanupFailureKeepsTheScore(t *testing.T) {
	l := newLab(t)
	l.start(t, "by-detection")
	l.fix(t)
	resolve := filepath.Join(l.root, "incidents", "demo-fault", "resolve.sh")
	if err := os.WriteFile(resolve, []byte(script+"exit 3\n"), 0o755); err != nil { //nolint:gosec // a test script must be executable
		t.Fatal(err)
	}

	sub, err := l.grader.Submit(t.Context(), "olive")
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if !sub.Passed || sub.CleanupErr == nil {
		t.Errorf("Passed = %v, CleanupErr = %v; want a pass that reports the failed cleanup", sub.Passed, sub.CleanupErr)
	}
	if history, _ := l.grader.Engine.History(); len(history) != 1 {
		t.Errorf("history has %d runs, want the pass recorded", len(history))
	}
}

func TestGrader_Checks(t *testing.T) {
	l := newLab(t)
	tests := []struct {
		name      string
		challenge string
		wantNames []string
		wantDir   string
	}{
		{"a detection check runs from the fault's directory", "by-detection", []string{"marker-gone"}, filepath.Join(l.root, "incidents", "demo-fault")},
		{"own checks run from the project root", "by-checks", []string{"always", "fixed"}, l.root},
		{"a scenario challenge's checks run from the project root", "by-scenario", []string{"fixed"}, l.root},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := l.grader.Engine.Load(tt.challenge)
			if err != nil {
				t.Fatal(err)
			}
			cs, err := l.grader.Checks(c)
			if err != nil {
				t.Fatalf("Checks() error = %v", err)
			}
			var names []string
			for _, k := range cs {
				names = append(names, k.Check.Name)
				if k.Dir != tt.wantDir {
					t.Errorf("%s runs from %q, want %q", k.Check.Name, k.Dir, tt.wantDir)
				}
			}
			if strings.Join(names, ",") != strings.Join(tt.wantNames, ",") {
				t.Errorf("checks = %v, want %v", names, tt.wantNames)
			}
		})
	}
}

// Template variables in a challenge's own checks are expanded by the engine
// that runs its setup.
func TestGrader_Checks_ExpandsTemplates(t *testing.T) {
	l := newLab(t)
	for _, setup := range []string{"incident", "scenario"} {
		t.Run(setup, func(t *testing.T) {
			c := &challenge.Challenge{
				Setup: challenge.SetupSpec{Type: setup, Ref: "demo-fault"},
				Grading: challenge.GradingSpec{Checks: []challenge.GCheck{{
					Name: "reachable", Type: "http", URL: "http://demo.{{.DomainSuffix}}/health", ExpectStatus: 200,
				}}},
			}
			cs, err := l.grader.Checks(c)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := cs[0].Check.URL, "http://demo.lab.test/health"; got != want {
				t.Errorf("url = %q, want %q", got, want)
			}
		})
	}
}

func TestGrader_SetupAndCleanup(t *testing.T) {
	tests := []struct {
		name      string
		challenge string
		wantErr   string
	}{
		{"incident", "by-detection", ""},
		{"scenario", "by-scenario", ""},
		{"a setup type nothing implements", "odd-setup", `unknown setup type "wizardry"`},
		{"a fault that does not exist", "ghost-fault", "ghost"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newLab(t)
			c, err := l.grader.Engine.Load(tt.challenge)
			if err != nil {
				t.Fatal(err)
			}
			setupErr, cleanupErr := l.grader.Setup(c), error(nil)
			if tt.wantErr == "" {
				if setupErr != nil {
					t.Fatalf("Setup() error = %v", setupErr)
				}
				if !l.exists("BROKEN") {
					t.Fatal("Setup() did not break the lab")
				}
			}
			cleanupErr = l.grader.Cleanup(c)
			if tt.wantErr == "" {
				if cleanupErr != nil {
					t.Fatalf("Cleanup() error = %v", cleanupErr)
				}
				if l.exists("BROKEN") {
					t.Error("Cleanup() left the lab broken")
				}
				return
			}
			for step, err := range map[string]error{"Setup": setupErr, "Cleanup": cleanupErr} {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("%s() error = %v, want it to mention %q", step, err, tt.wantErr)
				}
			}
		})
	}
}

// A detection check reads the fault's target from TARGET_NAMESPACE and
// TARGET_WORKLOAD. Graded without them it checks the default app, and a
// genuine fix to a fault aimed anywhere else is never graded as one.
func TestGrader_Submit_GivesTheDetectionCheckItsFaultsTarget(t *testing.T) {
	l := newLab(t)
	l.start(t, "batch")
	if !l.exists("BATCH") {
		t.Fatal("the setup did not break the lab")
	}
	if err := os.Remove(filepath.Join(l.root, "BATCH")); err != nil {
		t.Fatal(err)
	}
	sub, err := l.grader.Submit(t.Context(), "olive")
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if !sub.Passed {
		t.Errorf("a fixed lab did not pass: %+v", sub.Checks)
	}
}

func TestGrader_SeveralFaults(t *testing.T) {
	tests := []struct {
		name     string
		fix      []string // markers the learner removes before submitting
		wantOK   int
		wantPass bool
	}{
		{"nothing fixed", nil, 0, false},
		{"one of two fixed: half the checks, and the run goes on", []string{"BROKEN"}, 1, false},
		{"both fixed", []string{"BROKEN", "BATCH"}, 2, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newLab(t)
			l.start(t, "shift")
			if !l.exists("BROKEN") || !l.exists("BATCH") {
				t.Fatal("the setup did not inject both faults")
			}
			active, err := l.grader.Incidents.Active()
			if err != nil || active == nil || !active.Silent {
				t.Fatalf("active incident = %+v (err %v), want a silent one", active, err)
			}
			for _, m := range tt.fix {
				if err := os.Remove(filepath.Join(l.root, m)); err != nil {
					t.Fatal(err)
				}
			}
			sub, err := l.grader.Submit(t.Context(), "olive")
			if err != nil {
				t.Fatalf("Submit() error = %v", err)
			}
			if sub.Record.ChecksPassed != tt.wantOK || sub.Record.ChecksTotal != 2 || sub.Passed != tt.wantPass {
				t.Errorf("checks %d/%d passed=%v, want %d/2 passed=%v",
					sub.Record.ChecksPassed, sub.Record.ChecksTotal, sub.Passed, tt.wantOK, tt.wantPass)
			}
			if tt.wantOK == 1 && sub.Record.Score != 50 {
				t.Errorf("score = %d, want 50 for one fault of two inside par", sub.Record.Score)
			}
			if !tt.wantPass {
				return
			}
			cleaned, _ := os.ReadFile(filepath.Join(l.root, "CLEANED"))
			if n := strings.Count(string(cleaned), "resolved"); n != 2 {
				t.Errorf("cleanup resolved %d faults, want both", n)
			}
		})
	}
}

func TestGrader_SeveralFaults_CleanupAfterAFailedSecondInject(t *testing.T) {
	l := newLab(t)
	inject := filepath.Join(l.root, "incidents", "batch-fault", "inject.sh")
	if err := os.WriteFile(inject, []byte(script+"exit 4\n"), 0o755); err != nil { //nolint:gosec // a test script must be executable
		t.Fatal(err)
	}
	c, err := l.grader.Engine.Load("shift")
	if err != nil {
		t.Fatal(err)
	}
	if err := l.grader.Setup(c); err == nil {
		t.Fatal("Setup() passed although the second fault failed to inject")
	}
	if !l.exists("BROKEN") {
		t.Fatal("the first fault was not injected; the test would prove nothing")
	}
	if err := l.grader.Cleanup(c); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
	if l.exists("BROKEN") {
		t.Error("Cleanup() left the first fault in")
	}
}

func TestGrader_NextHint(t *testing.T) {
	t.Run("several faults take turns, a rung of each", func(t *testing.T) {
		l := newLab(t)
		l.start(t, "shift")
		want := []string{"look at the pods", "look at the batch namespace", "the report job"}
		for i, text := range want {
			h, err := l.grader.NextHint()
			if err != nil {
				t.Fatalf("hint %d: %v", i+1, err)
			}
			if h.Text != text || h.Index != i+1 || h.Total != 3 {
				t.Errorf("hint %d = %+v, want %q as %d/3", i+1, h, text, i+1)
			}
		}
		if _, err := l.grader.NextHint(); !errors.Is(err, incident.ErrNoMoreHints) {
			t.Errorf("hint 4 error = %v, want ErrNoMoreHints", err)
		}
		if run, _ := l.grader.Engine.Active(); run == nil || run.HintsUsed != 3 {
			t.Errorf("run = %+v, want 3 hints charged", run)
		}
	})
	t.Run("one fault uses its own ladder", func(t *testing.T) {
		l := newLab(t)
		l.start(t, "by-detection")
		h, err := l.grader.NextHint()
		if err != nil || h.Text != "look at the pods" {
			t.Fatalf("NextHint() = %+v, %v", h, err)
		}
		if run, _ := l.grader.Engine.Active(); run.HintsUsed != 1 {
			t.Errorf("hints charged = %d, want 1", run.HintsUsed)
		}
	})
	t.Run("a scenario challenge has none", func(t *testing.T) {
		l := newLab(t)
		l.start(t, "by-scenario")
		if _, err := l.grader.NextHint(); err == nil || !strings.Contains(err.Error(), "not available") {
			t.Errorf("NextHint() error = %v, want hints not available", err)
		}
	})
	t.Run("no challenge running", func(t *testing.T) {
		l := newLab(t)
		if _, err := l.grader.NextHint(); !errors.Is(err, challenge.ErrNoActive) {
			t.Errorf("NextHint() error = %v, want ErrNoActive", err)
		}
	})
}

// A silent challenge's check names and messages name its faults, so they are
// withheld until the run passes. Which faults are still broken is shown.
func TestGrader_Submit_SilentWithholdsTheFaultsUntilAPass(t *testing.T) {
	l := newLab(t)
	l.start(t, "shift")
	if err := os.Remove(filepath.Join(l.root, "BROKEN")); err != nil {
		t.Fatal(err)
	}
	sub, err := l.grader.Submit(t.Context(), "olive")
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		name string
		pass bool
	}{{"fault 1 of 2", true}, {"fault 2 of 2", false}}
	for i, w := range want {
		r := sub.Checks[i]
		if r.Name != w.name || r.Pass != w.pass {
			t.Errorf("check %d = %q pass=%v, want %q pass=%v", i, r.Name, r.Pass, w.name, w.pass)
		}
		for _, leak := range []string{"marker", "batch", "BATCH"} {
			if strings.Contains(r.Name+r.Error+r.Explanation+r.Got+r.Want+r.Remediation, leak) {
				t.Errorf("check %d names the fault (%q): %+v", i, leak, r)
			}
		}
	}

	if err := os.Remove(filepath.Join(l.root, "BATCH")); err != nil {
		t.Fatal(err)
	}
	sub, err = l.grader.Submit(t.Context(), "olive")
	if err != nil || !sub.Passed {
		t.Fatalf("Submit() = %+v, %v; want a pass", sub, err)
	}
	if sub.Checks[0].Name != "marker-gone" || sub.Checks[1].Name != "batch-marker-gone" {
		t.Errorf("a passing run still hides its checks: %q, %q", sub.Checks[0].Name, sub.Checks[1].Name)
	}
}
