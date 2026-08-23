// SPDX-License-Identifier: Apache-2.0
package platform

import (
	"os"
	"path/filepath"
	"testing"
)

// writeInterface writes platform/<category>/_interface.yaml under root.
func writeInterface(t *testing.T, root, category, body string) {
	t.Helper()
	dir := filepath.Join(root, "platform", category)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "_interface.yaml"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestIsExclusive(t *testing.T) {
	root := t.TempDir()
	writeInterface(t, root, "ingress", "name: ingress\nselection: exclusive\n")
	writeInterface(t, root, "mesh", "name: mesh\nselection: Exclusive\n") // case-insensitive
	writeInterface(t, root, "secrets", "name: secrets\nselection: multiple\n")
	writeInterface(t, root, "monitoring", "name: monitoring\n") // no selection field

	r := NewRegistry(root)

	cases := []struct {
		category string
		want     bool
	}{
		{"ingress", true},
		{"mesh", true},
		{"secrets", false},
		{"monitoring", false},        // absent field defaults to complementary
		{"monitoring/metrics", false}, // nested resolves to top-level "monitoring"
		{"nonexistent", false},        // missing interface file
	}
	for _, c := range cases {
		if got := r.IsExclusive(c.category); got != c.want {
			t.Errorf("IsExclusive(%q) = %v, want %v", c.category, got, c.want)
		}
	}
}
