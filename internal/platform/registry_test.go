// SPDX-License-Identifier: Apache-2.0
package platform

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func createTestProvider(t *testing.T, root, category, name string) {
	t.Helper()
	dir := filepath.Join(root, "platform", category, name)
	os.MkdirAll(dir, 0755)
	for _, script := range []string{"install.sh", "uninstall.sh", "status.sh"} {
		os.WriteFile(filepath.Join(dir, script), []byte("#!/bin/bash\necho ok"), 0755)
	}
	os.WriteFile(filepath.Join(dir, "values.yaml"), []byte("# test values"), 0644)
}

func TestNewRegistry_Discovery(t *testing.T) {
	root := t.TempDir()

	createTestProvider(t, root, "ingress", "traefik")
	createTestProvider(t, root, "ingress", "nginx")
	createTestProvider(t, root, "monitoring/metrics", "prometheus")

	reg := NewRegistry(root)

	cats := reg.Categories()
	sort.Strings(cats)

	if len(cats) < 2 {
		t.Fatalf("expected at least 2 categories, got %d: %v", len(cats), cats)
	}

	// Check ingress providers
	ingress := reg.GetProviders("ingress")
	if len(ingress) != 2 {
		t.Errorf("expected 2 ingress providers, got %d", len(ingress))
	}

	// Check monitoring/metrics providers
	metrics := reg.GetProviders("monitoring/metrics")
	if len(metrics) != 1 {
		t.Errorf("expected 1 metrics provider, got %d", len(metrics))
	}
}

func TestGetProvider(t *testing.T) {
	root := t.TempDir()
	createTestProvider(t, root, "ingress", "traefik")

	reg := NewRegistry(root)

	p, err := reg.GetProvider("ingress", "traefik")
	if err != nil {
		t.Fatalf("GetProvider: %v", err)
	}
	if p.Name != "traefik" {
		t.Errorf("Name: got %q, want %q", p.Name, "traefik")
	}
	if p.Category != "ingress" {
		t.Errorf("Category: got %q, want %q", p.Category, "ingress")
	}
}

func TestGetProvider_NotFound(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "platform"), 0755)

	reg := NewRegistry(root)

	_, err := reg.GetProvider("ingress", "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent provider")
	}
}

func TestProvider_HasScript(t *testing.T) {
	root := t.TempDir()
	createTestProvider(t, root, "ingress", "traefik")

	reg := NewRegistry(root)
	p, _ := reg.GetProvider("ingress", "traefik")

	if !p.HasScript("install.sh") {
		t.Error("expected HasScript(install.sh) = true")
	}
	if !p.HasScript("uninstall.sh") {
		t.Error("expected HasScript(uninstall.sh) = true")
	}
	if p.HasScript("nonexistent.sh") {
		t.Error("expected HasScript(nonexistent.sh) = false")
	}
}

func TestNewRegistry_EmptyPlatform(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "platform"), 0755)

	reg := NewRegistry(root)
	cats := reg.Categories()
	if len(cats) != 0 {
		t.Errorf("expected 0 categories, got %d", len(cats))
	}
}

func TestNewRegistry_SkipsNonProvider(t *testing.T) {
	root := t.TempDir()

	// Create a directory without install.sh — should not be treated as a provider
	dir := filepath.Join(root, "platform", "ingress", "miscdir")
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("not a provider"), 0644)

	// Create a real provider too
	createTestProvider(t, root, "ingress", "traefik")

	reg := NewRegistry(root)
	ingress := reg.GetProviders("ingress")
	if len(ingress) != 1 {
		t.Errorf("expected 1 ingress provider (ignoring miscdir), got %d", len(ingress))
	}
}

func TestNewRegistry_NestedCategoryLifecycle(t *testing.T) {
	root := t.TempDir()

	createTestProvider(t, root, "monitoring/metrics", "prometheus")
	createTestProvider(t, root, "monitoring", "grafana")

	reg := NewRegistry(root)

	// Verify prometheus is under monitoring/metrics, not monitoring
	metrics := reg.GetProviders("monitoring/metrics")
	if len(metrics) != 1 {
		t.Fatalf("expected 1 monitoring/metrics provider, got %d", len(metrics))
	}
	if metrics[0].Name != "prometheus" {
		t.Errorf("expected prometheus, got %s", metrics[0].Name)
	}
	if metrics[0].Category != "monitoring/metrics" {
		t.Errorf("expected category monitoring/metrics, got %s", metrics[0].Category)
	}

	// Verify grafana is under monitoring
	mon := reg.GetProviders("monitoring")
	if len(mon) != 1 {
		t.Fatalf("expected 1 monitoring provider, got %d", len(mon))
	}
	if mon[0].Name != "grafana" {
		t.Errorf("expected grafana, got %s", mon[0].Name)
	}

	// GetProvider lookups
	p, err := reg.GetProvider("monitoring/metrics", "prometheus")
	if err != nil {
		t.Fatalf("GetProvider monitoring/metrics prometheus: %v", err)
	}
	if p.Name != "prometheus" || p.Category != "monitoring/metrics" {
		t.Errorf("unexpected provider: %+v", p)
	}

	_, err = reg.GetProvider("monitoring", "prometheus")
	if err == nil {
		t.Error("expected error looking up prometheus under monitoring (should be monitoring/metrics)")
	}
}

func TestProvider_Namespace(t *testing.T) {
	tests := []struct {
		category string
		name     string
		wantNS   string
	}{
		{"ingress", "traefik", "traefik"},
		{"ingress", "nginx", "nginx"},
		{"monitoring", "grafana", "monitoring"},
		{"monitoring/metrics", "prometheus", "monitoring"},
		{"logging", "loki", "monitoring"},
		{"tracing", "tempo", "monitoring"},
		{"chaos", "chaos-mesh", "chaos-mesh"},
		{"gitops", "argocd", "argocd"},
	}

	for _, tt := range tests {
		t.Run(tt.category+"/"+tt.name, func(t *testing.T) {
			p := Provider{Category: tt.category, Name: tt.name}
			if got := p.Namespace(); got != tt.wantNS {
				t.Errorf("Namespace() = %q, want %q", got, tt.wantNS)
			}
		})
	}
}

func TestProvider_Namespace_CustomMonitoringNamespace(t *testing.T) {
	p := Provider{Category: "logging", Name: "loki", monitoringNS: "observability"}
	if got := p.Namespace(); got != "observability" {
		t.Errorf("Namespace() = %q, want %q", got, "observability")
	}
}
