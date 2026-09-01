// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/sagar2395/snowopslabs/internal/platform"
)

// writeProvider creates platform/<relDir>/install.sh so the registry scan
// discovers a provider at that path.
func writeProvider(t *testing.T, root, relDir string) {
	t.Helper()
	dir := filepath.Join(root, "platform", relDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "install.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestPrereqNamespaces(t *testing.T) {
	root := t.TempDir()
	writeProvider(t, root, "cost/opencost")                 // category/provider
	writeProvider(t, root, "monitoring/metrics/prometheus") // sub-category with a provider
	writeProvider(t, root, "ingress/traefik")               // bare category, provider owns its ns
	writeProvider(t, root, "ingress/nginx")                 // second mutually-exclusive provider
	reg := platform.NewRegistry(root)

	tests := []struct {
		name   string
		prereq string
		want   []string
	}{
		{"category/provider resolves to provider namespace", "cost/opencost", []string{"opencost"}},
		{"sub-category resolves to shared monitoring namespace", "monitoring/metrics", []string{"monitoring"}},
		{"bare category resolves to every provider namespace", "ingress", []string{"nginx", "traefik"}},
		{"unknown prereq resolves to nothing", "does/not/exist", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := prereqNamespaces(reg, tc.prereq)
			sort.Strings(got)
			sort.Strings(tc.want)
			if len(got) != len(tc.want) {
				t.Fatalf("prereqNamespaces(%q) = %v, want %v", tc.prereq, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("prereqNamespaces(%q) = %v, want %v", tc.prereq, got, tc.want)
				}
			}
		})
	}
}
