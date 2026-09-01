// SPDX-License-Identifier: Apache-2.0
package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProviderMeta_ReadsOwnInterface(t *testing.T) {
	dir := t.TempDir()
	provDir := filepath.Join(dir, "monitoring", "grafana")
	if err := os.MkdirAll(provDir, 0o755); err != nil {
		t.Fatal(err)
	}
	iface := `
description: Dashboard and visualization platform for observability data.
provides:
  - Web-based dashboard interface
  - Data source integration
requires:
  kubernetes_resources:
    - Namespace (monitoring)
  ports:
    - 80 (HTTP web interface)
  dependencies:
    - monitoring/metrics
implementations:
  grafana:
    chart: grafana/grafana
`
	if err := os.WriteFile(filepath.Join(provDir, "_interface.yaml"), []byte(iface), 0o644); err != nil {
		t.Fatal(err)
	}
	p := Provider{Category: "monitoring", Name: "grafana", Path: provDir}
	meta := p.Meta()
	if meta.Description == "" || meta.Chart != "grafana/grafana" {
		t.Fatalf("meta not parsed: %+v", meta)
	}
	if len(meta.Provides) != 2 || len(meta.Ports) != 1 || len(meta.Dependencies) != 1 {
		t.Errorf("meta lists wrong: %+v", meta)
	}
}

// When a provider has no _interface.yaml of its own, Meta falls back to the
// category directory above it (where some categories describe all providers).
func TestProviderMeta_FallsBackToCategoryDir(t *testing.T) {
	dir := t.TempDir()
	catDir := filepath.Join(dir, "ingress")
	provDir := filepath.Join(catDir, "traefik")
	if err := os.MkdirAll(provDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(catDir, "_interface.yaml"), []byte("description: Ingress controller.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := Provider{Category: "ingress", Name: "traefik", Path: provDir}
	if got := p.Meta().Description; got != "Ingress controller." {
		t.Errorf("fallback description = %q", got)
	}
}

func TestProviderInstallCommands_ExtractsHelmAndJoinsContinuations(t *testing.T) {
	dir := t.TempDir()
	script := `#!/usr/bin/env bash
set -euo pipefail
helm repo add grafana https://grafana.github.io/helm-charts
helm repo update
helm upgrade --install loki grafana/loki \
  --namespace "$NAMESPACE" \
  --create-namespace \
  -f values.yaml
kubectl rollout status statefulset/loki -n "$NAMESPACE"
kubectl apply -f extra.yaml
`
	if err := os.WriteFile(filepath.Join(dir, "install.sh"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	p := Provider{Name: "loki", Path: dir}
	cmds := p.InstallCommands()
	if len(cmds) != 2 {
		t.Fatalf("expected 2 install commands (helm upgrade + kubectl apply), got %d: %v", len(cmds), cmds)
	}
	if cmds[0] != `helm upgrade --install loki grafana/loki --namespace "$NAMESPACE" --create-namespace -f values.yaml` {
		t.Errorf("continuation not joined cleanly: %q", cmds[0])
	}
	if cmds[1] != "kubectl apply -f extra.yaml" {
		t.Errorf("kubectl apply not captured: %q", cmds[1])
	}
}
