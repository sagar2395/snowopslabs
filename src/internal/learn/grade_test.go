// SPDX-License-Identifier: Apache-2.0

package learn

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snowops-oss/snowopslabs/pkg/checks"
)

// gradeEngine is an engine over one path whose single module is done when a
// DONE file exists beside its check script.
func gradeEngine(t *testing.T) (*Engine, *Path, *Progress, string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "learn", "course")
	if err := os.MkdirAll(filepath.Join(dir, "checks"), 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := "name: course\ndisplayName: Course\nmodules:\n  - name: first\n    action: {type: command, ref: x}\n    check: {name: done, type: script, script: checks/done.sh, timeoutSeconds: 5}\n"
	if err := os.WriteFile(filepath.Join(dir, "path.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/usr/bin/env bash\n[ -f \"$(dirname \"$0\")/../DONE\" ] || { echo 'not done yet' >&2; exit 1; }\n"
	if err := os.WriteFile(filepath.Join(dir, "checks", "done.sh"), []byte(script), 0o755); err != nil { //nolint:gosec // a test script must be executable
		t.Fatal(err)
	}
	e := New(filepath.Join(root, "learn"), filepath.Join(root, "state"), filepath.Join(root, "history"))
	p, err := e.LoadPath("course")
	if err != nil {
		t.Fatal(err)
	}
	prog, err := e.StartPath("course")
	if err != nil {
		t.Fatal(err)
	}
	return e, p, prog, dir
}

func TestEngine_Grade(t *testing.T) {
	tests := []struct {
		name     string
		done     bool
		idx      int
		wantPass bool
		wantErr  string
	}{
		{"the task is not done", false, 0, false, ""},
		{"the task is done", true, 0, true, ""},
		{"a module the path does not have", true, 1, false, "out of range"},
		{"a negative module", true, -1, false, "out of range"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, p, prog, dir := gradeEngine(t)
			if tt.done {
				if err := os.WriteFile(filepath.Join(dir, "DONE"), nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			res, err := e.Grade(t.Context(), p, prog, tt.idx, checks.NewRunner(), func(s string) string { return s }, "olive")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Grade() error = %v, want it to mention %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Grade() error = %v", err)
			}
			if res.Pass != tt.wantPass {
				t.Errorf("check passed = %v, want %v (%s)", res.Pass, tt.wantPass, res.Error)
			}
			saved, err := e.Progress("course")
			if err != nil {
				t.Fatal(err)
			}
			if recorded := len(saved.CompletedIdxs) == 1; recorded != tt.wantPass {
				t.Errorf("module recorded complete = %v, want %v", recorded, tt.wantPass)
			}
		})
	}
}

// A request that is cancelled while grading fails the check and records
// nothing.
func TestEngine_Grade_CancelledContext(t *testing.T) {
	e, p, prog, dir := gradeEngine(t)
	if err := os.WriteFile(filepath.Join(dir, "DONE"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	res, err := e.Grade(ctx, p, prog, 0, checks.NewRunner(), func(s string) string { return s }, "")
	if err != nil || res.Pass {
		t.Fatalf("Grade(cancelled) = pass %v, err %v; want a failed check", res.Pass, err)
	}
	if saved, _ := e.Progress("course"); len(saved.CompletedIdxs) != 0 {
		t.Error("a cancelled grade recorded the module")
	}
}

// Each check type reaches the runner with every field it grades with.
func TestCheck_RunnerCheck(t *testing.T) {
	resolve := func(s string) string { return strings.ReplaceAll(s, "{{.App}}", "go-api") }
	tests := []struct {
		name string
		in   Check
		want checks.Check
	}{
		{"http", Check{Name: "c", Type: "http", URL: "http://{{.App}}/health", ExpectStatus: 204, TimeoutSeconds: 7},
			checks.Check{Name: "c", Type: "http", URL: "http://go-api/health", ExpectStatus: 204, TimeoutSeconds: 7}},
		{"http with the deprecated status field", Check{Name: "c", Type: "http", URL: "http://x", ExpectedStatus: 200},
			checks.Check{Name: "c", Type: "http", URL: "http://x", ExpectStatus: 200}},
		{"a relative script is under the path's directory", Check{Name: "c", Type: "script", Script: "checks/c.sh"},
			checks.Check{Name: "c", Type: "script", Script: "/lab/learn/p/checks/c.sh"}},
		{"an absolute script is kept", Check{Name: "c", Type: "script", Script: "/opt/c.sh"},
			checks.Check{Name: "c", Type: "script", Script: "/opt/c.sh"}},
		{"promql keeps its threshold", Check{Name: "c", Type: "promql", Query: `up{app="{{.App}}"}`, Operator: ">=", Value: "1"},
			checks.Check{Name: "c", Type: "promql", Query: `up{app="go-api"}`, Operator: ">=", Value: "1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.in.RunnerCheck("/lab/learn/p", resolve); got != tt.want {
				t.Errorf("RunnerCheck() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestExpandVars(t *testing.T) {
	t.Setenv("MY_HOST", "example.com")
	tests := []struct{ in, suffix, want string }{
		{"http://go-api.${DOMAIN_SUFFIX:-k3d.local}/health", "prod.internal", "http://go-api.prod.internal/health"},
		{"http://go-api.${DOMAIN_SUFFIX:-k3d.local}/health", "", "http://go-api.k3d.local/health"},
		{"http://x.${DOMAIN_SUFFIX}/y", "k3d.local", "http://x.k3d.local/y"},
		{"http://${MY_HOST}/z", "k3d.local", "http://example.com/z"},
		{"http://static/health", "k3d.local", "http://static/health"},
	}
	for _, tt := range tests {
		if got := ExpandVars(tt.in, tt.suffix); got != tt.want {
			t.Errorf("ExpandVars(%q, %q) = %q, want %q", tt.in, tt.suffix, got, tt.want)
		}
	}
}

func TestResolver(t *testing.T) {
	upper := func(s string) string { return strings.ReplaceAll(s, "{{.X}}", "filled") }
	if got := Resolver("lab.test", upper)("http://a.${DOMAIN_SUFFIX}/{{.X}}"); got != "http://a.lab.test/filled" {
		t.Errorf("Resolver = %q", got)
	}
	if got := Resolver("lab.test", nil)("{{.X}}.${DOMAIN_SUFFIX}"); got != "{{.X}}.lab.test" {
		t.Errorf("Resolver with no template step = %q", got)
	}
}

// A completed module goes into the results history named "<path>/<module>",
// with no score; a completion with no module name records nothing there.
func TestEngine_CompletionResultsRecord(t *testing.T) {
	e, p, prog, dir := gradeEngine(t)
	history := filepath.Join(filepath.Dir(filepath.Dir(dir)), "history", "results.jsonl")

	if err := e.MarkComplete(prog, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(history); err == nil {
		t.Error("MarkComplete with no module name wrote a results record")
	}

	prog, err := e.StartPath("course")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.MarkCompleteModule(p, prog, 0, "olive"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(history)
	if err != nil {
		t.Fatalf("no results record: %v", err)
	}
	for _, want := range []string{`"name":"course/first"`, `"score":-1`, `"user":"olive"`, `"outcome":"completed"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("results record %s does not contain %s", data, want)
		}
	}
	// Completing it again records nothing more.
	if err := e.MarkCompleteModule(p, prog, 0, "olive"); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(history); strings.Count(string(again), "\n") != strings.Count(string(data), "\n") {
		t.Error("completing a module twice wrote a second record")
	}
}

func TestEngine_ResetProgressOfAPathThatIsGone(t *testing.T) {
	e, _, _, dir := gradeEngine(t)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := e.ResetProgress("course"); err == nil {
		t.Error("ResetProgress of a path that no longer exists returned no error")
	}
}
