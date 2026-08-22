// SPDX-License-Identifier: Apache-2.0
package incident

// Repo-wide fault validation (runs in CI on every PR): every fault in
// incidents/ must satisfy the contract, and its inject/resolve/detection
// scripts must execute cleanly against a stub kubectl — so a fault that
// can't even run never reaches main.

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
	"crashloop-bad-config",
	"network-blackhole",
	"noisy-neighbor",
	"oom-kill",
	"service-selector-broken",
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
		// legitimately exit 1 (that just means "not resolved") — only
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
					t.Fatalf("script never called kubectl — suspicious for a fault script:\n%s", out)
				}
			})
		}
	}
}

// TestRepoFaults_InjectIdempotent re-runs inject.sh while "already injected"
// (the stub answers reads with empty output, so this exercises the
// not-yet-injected path twice — both runs must succeed).
func TestRepoFaults_InjectTwiceSucceeds(t *testing.T) {
	root := repoRoot(t)
	e := NewEngine(root, "k3d.local")
	for _, name := range shippedFaults {
		f, _ := e.Get(name)
		for i := 0; i < 2; i++ {
			if out, _, err := runWithStubKubectl(t, filepath.Join(f.Dir, "inject.sh")); err != nil {
				t.Fatalf("%s inject run %d: %v\n%s", name, i+1, err, out)
			}
		}
	}
}
