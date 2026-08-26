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
		{"monitoring", false},         // absent field defaults to complementary
		{"monitoring/metrics", false}, // nested resolves to top-level "monitoring"
		{"nonexistent", false},        // missing interface file
	}
	for _, c := range cases {
		if got := r.IsExclusive(c.category); got != c.want {
			t.Errorf("IsExclusive(%q) = %v, want %v", c.category, got, c.want)
		}
	}
}

func TestIsExclusiveMalformedInterface(t *testing.T) {
	root := t.TempDir()
	// A malformed _interface.yaml must not panic or claim exclusivity — it
	// falls back to the complementary default.
	writeInterface(t, root, "broken", "name: broken\nselection: [not, a, string\n")
	r := NewRegistry(root)
	if r.IsExclusive("broken") {
		t.Errorf("IsExclusive on malformed interface = true, want false")
	}
}

func TestRegistryDiscovery(t *testing.T) {
	root := t.TempDir()
	writeProvider(t, root, "ingress", "traefik", "install.sh")
	writeProvider(t, root, "ingress", "nginx", "install.sh")
	writeProvider(t, root, "secrets", "vault", "install.sh")

	r := NewRegistry(root)

	// Categories are discovered from the directory layout.
	cats := r.Categories()
	if len(cats) != 2 {
		t.Fatalf("Categories() = %v, want 2 (ingress, secrets)", cats)
	}

	// GetProviders returns every provider in a category.
	if got := r.GetProviders("ingress"); len(got) != 2 {
		t.Errorf("GetProviders(ingress) = %d providers, want 2", len(got))
	}
	if got := r.GetProviders("nonexistent"); len(got) != 0 {
		t.Errorf("GetProviders(nonexistent) = %d, want 0", len(got))
	}

	// GetProvider resolves a named provider and errors on a miss.
	if p, err := r.GetProvider("secrets", "vault"); err != nil || p.Name != "vault" {
		t.Errorf("GetProvider(secrets, vault) = %v, %v", p, err)
	}
	if _, err := r.GetProvider("secrets", "missing"); err == nil {
		t.Errorf("GetProvider(secrets, missing) expected error, got nil")
	}
}
