// SPDX-License-Identifier: Apache-2.0

package incident

// These tests check every fault in incidents/: it must meet the fault
// contract, and its inject, resolve and detection scripts must run against a
// stub kubectl.

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/snowops-oss/snowopslabs/internal/tmpl"
	"github.com/snowops-oss/snowopslabs/internal/workload"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if dirExists(filepath.Join(dir, "incidents")) && dirExists(filepath.Join(dir, "scenarios")) && fileExists(filepath.Join(dir, "Makefile")) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skip("repository root not found (running outside the repo checkout)")
		}
		dir = parent
	}
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

var shippedFaults = []string{
	"bad-deploy-rollout",
	"cpu-throttling",
	"crashloop-bad-config",
	"dns-egress-blocked",
	"liveness-probe-kill",
	"missing-secret-key",
	"network-blackhole",
	"noisy-neighbor",
	"oom-kill",
	"pending-unschedulable",
	"service-selector-broken",
	"stray-pod-behind-service",
}

func TestRepoFaults_AllLoadAndValidate(t *testing.T) {
	e := NewEngine(repoRoot(t), "k3d.local")

	for dir, err := range e.LoadErrors() {
		t.Errorf("fault %q failed to load: %v", dir, err)
	}
	loaded := map[string]bool{}
	for _, f := range e.List() {
		loaded[f.Name] = true
	}
	for _, want := range shippedFaults {
		if !loaded[want] {
			t.Errorf("shipped fault %q did not load", want)
		}
	}
}

func TestRepoFaults_HintsHaveProgression(t *testing.T) {
	root := repoRoot(t)
	for _, name := range shippedFaults {
		data, err := os.ReadFile(filepath.Join(root, "incidents", name, "hints.md"))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if n := strings.Count(string(data), "## Hint "); n < 3 {
			t.Errorf("%s: %d hints, contract requires at least 3", name, n)
		}
	}
}

const stubKubectl = `#!/usr/bin/env bash
# Stub: log calls, return empty for reads, succeed for writes.
echo "$@" >> "$KUBECTL_LOG"
if [ "$1" = "apply" ]; then cat > /dev/null 2>&1 || true; fi
exit 0
`

// runWithStubKubectl executes a fault script with a stub kubectl on PATH.
func runWithStubKubectl(t *testing.T, script string) (string, string, error) {
	t.Helper()
	binDir := t.TempDir()
	logFile := filepath.Join(binDir, "kubectl.log")
	if err := os.WriteFile(filepath.Join(binDir, "kubectl"), []byte(stubKubectl), 0755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", script)
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"KUBECTL_LOG="+logFile,
		// With no cluster, a script waiting for its symptom or sampling the
		// workload would wait out each interval. These skip the waits.
		"FAULT_WAIT_SECONDS=0",
		"FAULT_SAMPLE_SECONDS=0",
	)
	out, err := cmd.CombinedOutput()
	log, _ := os.ReadFile(logFile)
	return string(out), string(log), err
}

// TestRepoFaults_ScriptsExecute runs every shipped fault's inject.sh,
// resolve.sh, and detection script against the stub. This catches syntax
// errors, set -e landmines, and unguarded commands without a cluster.
func TestRepoFaults_ScriptsExecute(t *testing.T) {
	root := repoRoot(t)
	e := NewEngine(root, "k3d.local")

	for _, name := range shippedFaults {
		f, err := e.Get(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		// inject/resolve must succeed outright; a detection script may
		// legitimately exit 1 (that just means "not resolved") - only
		// abnormal exits (syntax errors, missing commands) fail the test.
		scripts := map[string]bool{"inject.sh": false, "resolve.sh": false}
		if f.Detection.Script != "" {
			scripts[f.Detection.Script] = true
		}
		for script, exitOneOK := range scripts {
			t.Run(name+"/"+script, func(t *testing.T) {
				out, log, err := runWithStubKubectl(t, filepath.Join(f.Dir, script))
				if err != nil {
					var ee *exec.ExitError
					if exitOneOK && errors.As(err, &ee) && ee.ExitCode() == 1 {
						// fine: check reported "not resolved"
					} else {
						t.Fatalf("script failed: %v\noutput:\n%s", err, out)
					}
				}
				if log == "" {
					t.Fatalf("script never called kubectl - suspicious for a fault script:\n%s", out)
				}
			})
		}
	}
}

// TestRepoFaults_InjectIdempotent runs inject.sh twice; both runs must
// succeed. The stub returns empty output, so both take the not-yet-injected
// path.
func TestRepoFaults_InjectTwiceSucceeds(t *testing.T) {
	root := repoRoot(t)
	e := NewEngine(root, "k3d.local")
	for _, name := range shippedFaults {
		f, _ := e.Get(name)
		for i := range 2 {
			if out, _, err := runWithStubKubectl(t, filepath.Join(f.Dir, "inject.sh")); err != nil {
				t.Fatalf("%s inject run %d: %v\n%s", name, i+1, err, out)
			}
		}
	}
}

// A shared script library lives alongside the faults; it is not a fault and
// must not be reported as one missing its fault.yaml.
func TestRepoFaults_UnderscoreDirsAreNotFaults(t *testing.T) {
	e := NewEngine(repoRoot(t), "k3d.local")
	for _, name := range []string{"_lib"} {
		if _, err := e.Get(name); err == nil {
			t.Errorf("%q loaded as a fault", name)
		}
		if _, reported := e.LoadErrors()[name]; reported {
			t.Errorf("%q was reported as a failed fault load", name)
		}
	}
}

// unexpanded matches one of labctl's own template variables left in rendered
// text. Other template syntaxes a fault carries are not labctl's.
var unexpanded = regexp.MustCompile(`\{\{\s*\.(` + strings.Join(tmpl.FieldNames(), "|") + `)\b`)

// TestRepoFaults_RenderForAnyWorkload renders everything a learner reads of
// each fault (its card, hints and solution) for an app that is not the default
// one. A template variable left in the output, or the default app's name in a
// fault that follows the binding, means that text ignores the binding.
func TestRepoFaults_RenderForAnyWorkload(t *testing.T) {
	root := repoRoot(t)
	e := NewEngine(root, "lab.test")
	bound := workload.Workload{Name: "zz-app", Namespace: "zz-ns", Port: "9999", Metric: "zz_metric"}
	e.Workload = bound

	faults := e.ListBound(bound)
	if len(faults) != len(shippedFaults) {
		t.Fatalf("rendered %d faults, the repository ships %d", len(faults), len(shippedFaults))
	}
	for _, f := range faults {
		card, err := json.Marshal(f)
		if err != nil {
			t.Fatalf("%s: marshal: %v", f.Name, err)
		}
		texts := map[string]string{"fault.yaml": string(card)}
		for _, file := range []string{"hints.md", "solution.md"} {
			raw, err := os.ReadFile(filepath.Join(f.Dir, file))
			if err != nil {
				t.Fatalf("%s: %v", f.Name, err)
			}
			texts[file] = e.ResolveTemplate(string(raw))
		}
		for file, text := range texts {
			t.Run(f.Name+"/"+file, func(t *testing.T) {
				if m := unexpanded.FindString(text); m != "" {
					t.Errorf("an unexpanded template variable reaches the learner: %s", m)
				}
				// A fault whose target pins the default app names it on purpose.
				pinned := f.Target.Workload == workload.DefaultApp
				if i := strings.Index(text, workload.DefaultApp); i >= 0 && !pinned {
					t.Errorf("names %q although bound to %q: …%s…",
						workload.DefaultApp, bound.Name, text[max(0, i-60):min(len(text), i+40)])
				}
			})
		}
	}
}
