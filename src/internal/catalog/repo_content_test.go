// SPDX-License-Identifier: Apache-2.0
package catalog

import (
	"path/filepath"
	"runtime"
	"testing"
)

// repoRoot returns the repository root by walking up from this test file.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine test file path")
	}
	// src/internal/catalog/repo_content_test.go -> ../../.. is the content root
	// (the Go source lives under src/, the content stays at the repo root).
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
}

// TestRepoContentValidates is the guard behind "every file under scenarios/,
// incidents/, learn/ and challenges/ passes validation". If an author adds or
// edits content that breaks the model, this fails with the exact file, line and
// reference — the same output `labctl validate` prints.
func TestRepoContentValidates(t *testing.T) {
	c, err := Load(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if !c.IsValid() {
		for _, p := range c.Problems() {
			t.Errorf("content problem: %s", p)
		}
		t.Fatalf("%d content problem(s); all in-repo content must validate", len(c.Problems()))
	}
	// Sanity: the repo actually has content, so a silently-empty load can't pass.
	counts := c.Counts()
	if counts[KindScenario] == 0 || counts[KindIncident] == 0 {
		t.Fatalf("expected in-repo scenarios and incidents, got %+v", counts)
	}
}

// TestVerifiedContentSet locks the curated verified set: the scenarios
// and incidents confirmed end-to-end are marked verified, and the ones that
// aren't (the disruptive drills; the less-exercised incidents) are explicitly
// unverified. If someone flips a flag, this fails so the change is deliberate.
func TestVerifiedContentSet(t *testing.T) {
	c, err := Load(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}

	// Empty, and meant to stay that way: every scenario has now been walked
	// on a live cluster, tamper-tested and scored. A new one belongs here only
	// while it is being written.
	unverifiedScenarios := map[string]bool{}
	verifiedScenarios := 0
	for _, s := range c.Scenarios() {
		if s.Verified {
			verifiedScenarios++
			if unverifiedScenarios[s.Name] {
				t.Errorf("scenario %q is marked verified but is in the unverified set", s.Name)
			}
		} else if !unverifiedScenarios[s.Name] {
			t.Errorf("scenario %q is unverified but not in the known unverified set — verify it or add it", s.Name)
		}
	}
	if verifiedScenarios != 13 {
		t.Errorf("verified scenarios = %d, want 13 (the confirmed set)", verifiedScenarios)
	}

	unverifiedIncidents := map[string]bool{
		"oom-kill": true,
	}
	verifiedIncidents := 0
	for _, f := range c.Incidents() {
		if f.Verified {
			verifiedIncidents++
			if unverifiedIncidents[f.Name] {
				t.Errorf("incident %q is marked verified but is in the unverified set", f.Name)
			}
		} else if !unverifiedIncidents[f.Name] {
			t.Errorf("incident %q is unverified but not in the known unverified set", f.Name)
		}
	}
	if verifiedIncidents != 5 {
		t.Errorf("verified incidents = %d, want 5 (the go-api-targeting confirmed set)", verifiedIncidents)
	}
}
