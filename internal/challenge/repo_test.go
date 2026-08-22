// SPDX-License-Identifier: Apache-2.0
package challenge

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRepo_AllChallengesValid validates every challenge in the repository's
// challenges/ directory.  Fails CI if any challenge.yaml is invalid.
func TestRepo_AllChallengesValid(t *testing.T) {
	root := findRepoRoot(t)
	cDir := filepath.Join(root, "challenges")

	entries, err := os.ReadDir(cDir)
	if os.IsNotExist(err) {
		t.Skip("challenges/ directory not found")
	}
	if err != nil {
		t.Fatalf("readdir challenges/: %v", err)
	}

	e := New(cDir, t.TempDir(), "")
	validated := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		t.Run(name, func(t *testing.T) {
			if _, err := e.Load(name); err != nil {
				t.Errorf("invalid challenge %q: %v", name, err)
				return
			}
			validated++
		})
	}
	if validated == 0 && len(entries) > 0 {
		t.Error("no valid challenges found — check challenges/ directory structure")
	}
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "challenges")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "Makefile")); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skip("repository root not found")
		}
		dir = parent
	}
}
