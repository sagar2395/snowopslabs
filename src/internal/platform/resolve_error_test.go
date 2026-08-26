// SPDX-License-Identifier: Apache-2.0
package platform

import (
	"os"
	"path/filepath"
	"testing"
)

// writeProvider creates a minimal provider directory.
// The scanner expects: platform/<category>/<name>/install.sh
// The provider is stored with category key = <category>.
func writeProvider(t *testing.T, root, category, name string, scripts ...string) {
	t.Helper()
	dir := filepath.Join(root, "platform", category, name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, s := range scripts {
		if err := os.WriteFile(filepath.Join(dir, s), []byte("#!/usr/bin/env bash\nexit 0\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGetProvider_UnknownName_ReturnsError(t *testing.T) {
	root := t.TempDir()
	writeProvider(t, root, "ingress", "traefik", "install.sh")

	r := NewRegistry(root)
	_, err := r.GetProvider("ingress", "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown provider name, got nil")
	}
}

func TestGetProvider_UnknownCategory_ReturnsError(t *testing.T) {
	root := t.TempDir()
	writeProvider(t, root, "ingress", "traefik", "install.sh")

	r := NewRegistry(root)
	_, err := r.GetProvider("nonexistent-category", "traefik")
	if err == nil {
		t.Fatal("expected error for unknown category, got nil")
	}
}

func TestGetProviders_UnknownCategory_ReturnsEmptySlice(t *testing.T) {
	root := t.TempDir()
	writeProvider(t, root, "ingress", "traefik", "install.sh")

	r := NewRegistry(root)
	providers := r.GetProviders("nonexistent-category")
	if len(providers) != 0 {
		t.Errorf("expected empty slice for unknown category, got %d providers", len(providers))
	}
}

func TestProviderNamespace_MonitoringCategory(t *testing.T) {
	tests := []struct {
		category string
		name     string
		ns       string
		wantNS   string
	}{
		{"monitoring", "prometheus", "custom-monitoring", "custom-monitoring"},
		{"logging", "loki", "custom-monitoring", "custom-monitoring"},
		{"tracing", "tempo", "custom-monitoring", "custom-monitoring"},
		{"ingress", "traefik", "custom-monitoring", "traefik"}, // non-monitoring uses provider name
	}

	for _, tt := range tests {
		t.Run(tt.category+"/"+tt.name, func(t *testing.T) {
			p := Provider{Category: tt.category, Name: tt.name, monitoringNS: tt.ns}
			got := p.Namespace()
			if got != tt.wantNS {
				t.Errorf("Namespace() = %q, want %q", got, tt.wantNS)
			}
		})
	}
}

func TestProviderNamespace_DefaultMonitoringWhenEmpty(t *testing.T) {
	p := Provider{Category: "monitoring", Name: "grafana", monitoringNS: ""}
	if got := p.Namespace(); got != "monitoring" {
		t.Errorf("Namespace() = %q, want %q", got, "monitoring")
	}
}

func TestProviderHasScript(t *testing.T) {
	root := t.TempDir()
	writeProvider(t, root, "ingress", "traefik", "install.sh", "status.sh")
	// uninstall.sh deliberately not written

	r := NewRegistry(root)
	providers := r.GetProviders("ingress")
	if len(providers) == 0 {
		t.Fatal("expected at least one ingress provider to be discovered")
	}

	p := providers[0]
	if !p.HasScript("install.sh") {
		t.Error("HasScript(install.sh) should be true")
	}
	if !p.HasScript("status.sh") {
		t.Error("HasScript(status.sh) should be true")
	}
	if p.HasScript("uninstall.sh") {
		t.Error("HasScript(uninstall.sh) should be false")
	}
}
