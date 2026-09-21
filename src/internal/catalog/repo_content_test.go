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
	// This file is in src/internal/catalog; the repo root is three levels up.
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
}

// TestRepoContentValidates checks that all content under scenarios/,
// incidents/, learn/ and challenges/ passes validation, reporting failures as
// `labctl validate` does.
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

// TestVerifiedContentSet pins which scenarios and incidents are marked
// verified, so changing a verified flag requires updating this test too.
func TestVerifiedContentSet(t *testing.T) {
	c, err := Load(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}

	// Every scenario has been reviewed on a live cluster. A new scenario is
	// listed here until its review passes.
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

	// Every fault has been reviewed on a live cluster. A new fault is listed
	// here until its review passes.
	unverifiedIncidents := map[string]bool{}
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
	if verifiedIncidents != 6 {
		t.Errorf("verified incidents = %d, want 6 (every shipped fault, all review-confirmed)", verifiedIncidents)
	}
}
