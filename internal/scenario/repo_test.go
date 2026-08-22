// SPDX-License-Identifier: Apache-2.0
package scenario

// Repo-wide scenario validation: every scenario.yaml checked into this
// repository must parse, validate against the schema, and pass static
// preflight (asset files, prerequisite dirs). This runs in CI on every PR,
// so a broken or malformed scenario can never reach main.

import (
	"os"
	"path/filepath"
	"testing"
)

// repoRoot walks up from the package directory until it finds the project
// root (identified by the scenarios/ directory plus the Makefile, so Go
// package dirs can't be mistaken for it).
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if dirExists(filepath.Join(dir, "scenarios")) && dirExists(filepath.Join(dir, "platform")) && fileExists(filepath.Join(dir, "Makefile")) {
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

func TestRepoScenarios_AllLoadAndValidate(t *testing.T) {
	root := repoRoot(t)
	engine := NewEngine(root, "k3d.local", "") // empty profile: skip runtime gate

	// Any scenario that failed to load is a hard failure with its reason.
	for dir, err := range engine.LoadErrors() {
		t.Errorf("scenario %q failed to load: %v", dir, err)
	}

	// Every directory under scenarios/ must produce a loaded scenario —
	// catches scenarios silently skipped for any reason.
	entries, err := os.ReadDir(filepath.Join(root, "scenarios"))
	if err != nil {
		t.Fatalf("reading scenarios dir: %v", err)
	}
	loaded := map[string]bool{}
	for _, s := range engine.List() {
		loaded[filepath.Base(s.Dir)] = true
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if !loaded[e.Name()] {
			t.Errorf("scenarios/%s did not load (no scenario.yaml or it was skipped)", e.Name())
		}
	}
}

func TestRepoScenarios_PreflightPassesStatically(t *testing.T) {
	root := repoRoot(t)
	engine := NewEngine(root, "k3d.local", "")

	for _, s := range engine.List() {
		if err := engine.Preflight(s); err != nil {
			t.Errorf("scenario %q fails static preflight:\n%v", s.Name, err)
		}
	}
}

// TestRepoScenarios_ObservabilityIsV2 pins the task-040 acceptance criteria:
// the reference v2 migration must keep its stages, objectives, and checks.
func TestRepoScenarios_ObservabilityIsV2(t *testing.T) {
	root := repoRoot(t)
	engine := NewEngine(root, "k3d.local", "")

	s, err := engine.Get("observability-sre")
	if err != nil {
		t.Fatalf("observability-sre must exist: %v", err)
	}
	if len(s.Stages) < 1 {
		t.Errorf("observability-sre must declare at least 1 stage, has %d", len(s.Stages))
	}
	if len(s.Objectives) < 2 {
		t.Errorf("observability-sre must declare at least 2 objectives, has %d", len(s.Objectives))
	}
	if len(s.Checks) < 3 {
		t.Errorf("observability-sre must declare at least 3 checks, has %d", len(s.Checks))
	}
}
