// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"strings"
	"testing"

	"github.com/snowops-oss/snowopslabs/internal/config"
	"github.com/snowops-oss/snowopslabs/internal/incident"
	"github.com/snowops-oss/snowopslabs/internal/labtest"
)

func TestLearnResolver(t *testing.T) {
	oldCfg, oldInc := cfg, incEng
	t.Cleanup(func() { cfg, incEng = oldCfg, oldInc })
	cfg = &config.Config{DomainSuffix: "lab.localhost", HTTPPort: "8080"}
	incEng = incident.NewEngine(t.TempDir(), "lab.localhost")
	incEng.IngressURLSuffix = cfg.IngressURLSuffix()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "a template carries the port", input: "http://go-api.{{.IngressURLSuffix}}/health", expected: "http://go-api.lab.localhost:8080/health"},
		{name: "an environment variable still expands", input: "http://go-api.${DOMAIN_SUFFIX}/health", expected: "http://go-api.lab.localhost/health"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := learnResolver()(tt.input); got != tt.expected {
				t.Errorf("resolve(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

// writeCourse adds a two-module path: the first module is done once the
// learner creates a marker, the second passes straight away.
func writeCourse(t *testing.T, l *terminal) {
	t.Helper()
	l.Write(t, "learn/course/path.yaml", `name: course
displayName: "Course"
description: "Two modules"
modules:
  - name: first
    displayName: "Make the marker"
    intro: intros/first.md
    action: {type: command, ref: "touch DONE"}
    check: {name: marker-there, type: script, script: checks/marker.sh, timeoutSeconds: 5}
  - name: second
    displayName: "Nothing to do"
    action: {type: scenario, ref: demo-scenario}
    check: {name: always, type: script, script: checks/always.sh, timeoutSeconds: 5}
`)
	l.Write(t, "learn/course/intros/first.md", "# First\nCreate the marker file.\n")
	l.Write(t, "learn/course/checks/marker.sh", "#!/usr/bin/env bash\nif [ ! -f \"$(dirname \"$0\")/../DONE\" ]; then\n  echo 'the marker is not there yet' >&2\n  exit 1\nfi\n")
	l.Write(t, "learn/course/checks/always.sh", "#!/usr/bin/env bash\nexit 0\n")
}

// A learning path at the terminal: a module stays open until its check
// passes, then the path moves on, and finishing every module finishes it.
func TestLearn_Journey(t *testing.T) {
	l := newTerminal(t)
	writeCourse(t, l)
	var steps []cliResult
	step := func(args ...string) cliResult {
		t.Helper()
		r := l.mustRun(args...)
		steps = append(steps, r)
		return r
	}

	step("learn", "list")
	step("learn", "progress", "course")
	step("learn", "start", "course")
	step("learn", "next", "course", "--show-only")

	notDone := step("learn", "next", "course")
	for _, want := range []string{"# First", "Module 1/2: Make the marker", "✗ Check not passing yet", "the marker is not there yet"} {
		if !strings.Contains(notDone.stdout, want) {
			t.Errorf("next before the task is done does not print %q:\n%s", want, notDone.stdout)
		}
	}
	if r := step("learn", "progress", "course"); !strings.Contains(r.stdout, "0/2 modules") {
		t.Errorf("a failing check advanced the path:\n%s", r.stdout)
	}

	l.Write(t, "learn/course/DONE", "") // the learner does the task
	if r := step("learn", "next", "course"); !strings.Contains(r.stdout, "✓ Check passed") || !strings.Contains(r.stdout, "Next: module 2 - Nothing to do") {
		t.Errorf("next after the task is done does not complete the module and name the next:\n%s", r.stdout)
	}
	if r := step("learn", "next", "course"); !strings.Contains(r.stdout, `All 2 modules complete! Path "course" finished.`) {
		t.Errorf("the last module did not finish the path:\n%s", r.stdout)
	}
	step("learn", "next", "course")
	if r := step("learn", "progress", "course"); strings.Count(r.stdout, "[✓]") != 2 {
		t.Errorf("progress does not tick both modules:\n%s", r.stdout)
	}
	step("learn", "list")
	step("learn", "progress")
	l.golden("learn_journey", steps...)
}

// `learn start` on a path already under way starts it again from the top.
func TestLearn_StartAgainRestartsThePath(t *testing.T) {
	l := newTerminal(t)
	l.mustRun("learn", "start", labtest.Path)
	l.mustRun("learn", "next", labtest.Path)
	if r := l.mustRun("learn", "progress", labtest.Path); !strings.Contains(r.stdout, "1/1 modules") {
		t.Fatalf("the module was not completed:\n%s", r.stdout)
	}
	l.mustRun("learn", "start", labtest.Path)
	if r := l.mustRun("learn", "progress", labtest.Path); !strings.Contains(r.stdout, "0/1 modules") {
		t.Errorf("a restart kept the old progress:\n%s", r.stdout)
	}
}

// With one path in the lab, `learn next` needs no name; with two it asks.
func TestLearn_NextWithoutAName(t *testing.T) {
	l := newTerminal(t)
	l.mustRun("learn", "start", labtest.Path)
	if r := l.mustRun("learn", "next"); !strings.Contains(r.stdout, "Module 1/1: Step one") {
		t.Errorf("next with the only path unnamed did not pick it:\n%s", r.stdout)
	}

	writeCourse(t, l)
	r := l.run("learn", "next")
	if r.exitCode() != 1 || !strings.Contains(r.stderr, "multiple paths available - specify one: course, demo-path") {
		t.Errorf("next with two paths = exit %d, stderr %q; want it to list both", r.exitCode(), r.stderr)
	}
}

func TestLearn_Failures(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, l *terminal)
		args    []string
		wantErr string
	}{
		{"start a path that does not exist", nil, []string{"learn", "start", "ghost"}, "see 'labctl learn list'"},
		{"next on a path that does not exist", nil, []string{"learn", "next", "ghost"}, "see 'labctl learn list'"},
		{"progress of a path that does not exist", nil, []string{"learn", "progress", "ghost"}, "see 'labctl learn list'"},
		{"next before the path is started", nil, []string{"learn", "next", labtest.Path}, "labctl learn start demo-path"},
		{"--app on a learning path", nil, []string{"learn", "start", labtest.Path, "--app", labtest.App}, "--app does not apply to labctl learn start"},
		{"a path whose YAML is broken",
			func(t *testing.T, l *terminal) { l.Write(t, "learn/broken/path.yaml", "name: [unclosed") },
			[]string{"learn", "start", "broken"}, "labctl validate"},
		{"a path with a check the runner cannot grade",
			func(t *testing.T, l *terminal) {
				l.Write(t, "learn/odd/path.yaml", "name: odd\ndisplayName: Odd\nmodules:\n  - name: m\n    action: {type: command, ref: x}\n    check: {name: c, type: kubectl}\n")
			},
			[]string{"learn", "start", "odd"}, `check.type "kubectl" is not supported`},
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

// `labctl validate` refuses a path whose check could never be graded.
func TestValidate_RejectsAnUngradableLearningCheck(t *testing.T) {
	l := newTerminal(t)
	l.Write(t, "learn/odd/path.yaml", "name: odd\ndisplayName: Odd\nmodules:\n  - name: m\n    action: {type: command, ref: x}\n    check: {name: c, type: promql, query: up}\n")
	r := l.run("validate")
	if r.exitCode() != 1 || !strings.Contains(r.stdout+r.stderr, "a promql check needs query, operator and value") {
		t.Errorf("validate = exit %d; want it to fail on the promql check with no threshold:\n%s%s", r.exitCode(), r.stdout, r.stderr)
	}
}
