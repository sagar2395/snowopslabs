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

// Shared-namespace providers can't be detected by namespace existence (they all
// live in the monitoring namespace), so SharesNamespace must flag exactly the
// monitoring/logging/tracing categories — including nested ones like
// monitoring/metrics — and nothing else.
func TestProviderSharesNamespace(t *testing.T) {
	tests := []struct {
		category string
		name     string
		want     bool
	}{
		{"monitoring", "grafana", true},
		{"monitoring/metrics", "prometheus", true},
		{"logging", "loki", true},
		{"tracing", "tempo", true},
		{"ingress", "traefik", false},
		{"gitops", "argocd", false},
		{"secrets", "vault", false},
	}

	for _, tt := range tests {
		t.Run(tt.category+"/"+tt.name, func(t *testing.T) {
			p := Provider{Category: tt.category, Name: tt.name}
			if got := p.SharesNamespace(); got != tt.want {
				t.Errorf("SharesNamespace() = %v, want %v", got, tt.want)
			}
		})
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

// A provider whose control plane does not live in a namespace named after it
// must declare that namespace in _interface.yaml. Inferring it from the
// directory name made istio (istio-system) and nginx (ingress-nginx)
// permanently undetectable, so `scenario up` warned that an installed
// prerequisite was missing on a correctly-provisioned cluster.
func TestProviderNamespace_DeclaredWins(t *testing.T) {
	tests := []struct {
		name       string
		category   string
		provider   string
		declaredNS string
		wantNS     string
	}{
		{"declared namespace wins over the provider name", "mesh", "istio", "istio-system", "istio-system"},
		{"undeclared falls back to the provider name", "mesh", "linkerd", "", "linkerd"},
		{"declared namespace on an ingress provider", "ingress", "nginx", "ingress-nginx", "ingress-nginx"},
		// Monitoring-family providers share one namespace, so a stray
		// declaration must not split them apart.
		{"monitoring family ignores a declaration", "monitoring", "grafana", "grafana-own", "monitoring"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Provider{Category: tt.category, Name: tt.provider, declaredNS: tt.declaredNS}
			if got := p.Namespace(); got != tt.wantNS {
				t.Errorf("Namespace() = %q, want %q", got, tt.wantNS)
			}
		})
	}
}

// The declaration has to survive the registry scan, not just the struct field:
// the real defect was that nothing ever populated it.
func TestRegistryScan_PopulatesDeclaredNamespace(t *testing.T) {
	root := t.TempDir()
	provDir := filepath.Join(root, "platform", "mesh", "istio")
	if err := os.MkdirAll(provDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(provDir, "install.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	iface := `
name: mesh
implementations:
  istio:
    charts: [istio/base, istio/istiod]
    namespace: istio-system
`
	if err := os.WriteFile(filepath.Join(root, "platform", "mesh", "_interface.yaml"), []byte(iface), 0o644); err != nil {
		t.Fatal(err)
	}

	p, err := NewRegistry(root).GetProvider("mesh", "istio")
	if err != nil {
		t.Fatalf("GetProvider(mesh/istio): %v", err)
	}
	if got := p.Namespace(); got != "istio-system" {
		t.Errorf("Namespace() = %q, want %q — the scan did not read the declaration", got, "istio-system")
	}
}
