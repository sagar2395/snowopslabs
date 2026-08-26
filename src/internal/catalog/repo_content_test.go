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

// TestRepoContentValidates is the guard behind the W2 exit criterion "every file
// under scenarios/, incidents/, learn/, challenges/ passes validation". If an
// author adds or edits content that breaks the model, this fails with the exact
// file, line and reference — the same output `labctl validate` prints.
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
