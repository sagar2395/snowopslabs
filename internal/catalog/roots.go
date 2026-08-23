// SPDX-License-Identifier: Apache-2.0
package catalog

import (
	"os"
	"path/filepath"
	"strings"
)

// ContentPathEnv is the environment variable naming extra content roots,
// separated by the OS path-list separator (":" on Unix). Roots listed here are
// loaded after the in-repo root, so an external root can add new content or
// shadow in-repo items of the same name without forking the repository (W2-T09).
const ContentPathEnv = "SNOWOPS_CONTENT_PATH"

// ResolveRoots returns the ordered content roots to load: the project root
// first, then each existing directory named in SNOWOPS_CONTENT_PATH, in the
// order given. Non-existent, blank, or duplicate entries are skipped so a stale
// path entry never fails the whole load.
func ResolveRoots(projectRoot string) []string {
	return resolveRoots(projectRoot, os.Getenv(ContentPathEnv))
}

// resolveRoots is the testable core of ResolveRoots, taking the env value
// directly instead of reading the process environment.
func resolveRoots(projectRoot, contentPath string) []string {
	roots := []string{projectRoot}
	seen := map[string]bool{cleanAbs(projectRoot): true}
	for _, entry := range filepath.SplitList(contentPath) {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		key := cleanAbs(entry)
		if seen[key] {
			continue
		}
		info, err := os.Stat(entry) //nolint:gosec // G703: entry is a catalog root the operator configured, not untrusted input
		if err != nil || !info.IsDir() {
			continue
		}
		seen[key] = true
		roots = append(roots, entry)
	}
	return roots
}

func cleanAbs(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return filepath.Clean(p)
}
