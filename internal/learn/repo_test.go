// SPDX-License-Identifier: Apache-2.0
package learn

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRepo_AllPathsValid validates every learn path in the repository's
// learn/ directory.  This runs in CI and fails if any path.yaml is broken.
func TestRepo_AllPathsValid(t *testing.T) {
	root := findRepoRoot(t)
	learnDir := filepath.Join(root, "learn")

	entries, err := os.ReadDir(learnDir)
	if os.IsNotExist(err) {
		t.Skip("learn/ directory not found — no paths to validate")
	}
	if err != nil {
		t.Fatalf("readdir learn/: %v", err)
	}

	e := New(learnDir, t.TempDir(), "")
	validated := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		t.Run(name, func(t *testing.T) {
			p, err := e.LoadPath(name)
			if err != nil {
				t.Errorf("invalid path %q: %v", name, err)
				return
			}
			// Check intro files actually exist.
			for i, m := range p.Modules {
				if m.Intro == "" {
					continue
				}
				introPath := filepath.Join(p.Dir(), m.Intro)
				if _, err := os.Stat(introPath); err != nil {
					t.Errorf("modules[%d].intro %q not found: %v", i, m.Intro, err)
				}
			}
			validated++
		})
	}
	if validated == 0 && len(entries) > 0 {
		t.Error("no valid paths found — check learn/ directory structure")
	}
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "learn")); err == nil {
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
