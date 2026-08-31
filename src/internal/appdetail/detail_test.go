// SPDX-License-Identifier: Apache-2.0
package appdetail

import (
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot resolves the repository root from this package's directory so the
// test reads the real apps/ layout it describes.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	return root
}

func TestBuild_GoAPI(t *testing.T) {
	d := Build(repoRoot(t), "go-api", "docker", "helm", "", "values-dev.yaml")

	if d.Namespace != "go-api" {
		t.Errorf("Namespace default: got %q, want go-api", d.Namespace)
	}
	if d.Dockerfile == nil || !strings.HasPrefix(d.Dockerfile.Path, "apps/go-api/Dockerfile") {
		t.Errorf("Dockerfile: got %+v, want apps/go-api/Dockerfile", d.Dockerfile)
	}
	if d.ChartYAML == nil || !strings.Contains(d.ChartYAML.Content, "name: go-api") {
		t.Errorf("ChartYAML missing or wrong content: %+v", d.ChartYAML)
	}
	if d.ValuesFile == nil || d.ValuesFile.Path != "apps/go-api/deploy/helm/values-dev.yaml" {
		t.Errorf("ValuesFile: got %+v, want the app.env values-dev.yaml", d.ValuesFile)
	}
	if d.HelmChartPath != "apps/go-api/deploy/helm" {
		t.Errorf("HelmChartPath: got %q", d.HelmChartPath)
	}
	if !contains(d.Tech, "Go") || !contains(d.Tech, "Docker") || !contains(d.Tech, "Helm") {
		t.Errorf("Tech detection incomplete: %v", d.Tech)
	}
	if len(d.Templates) == 0 {
		t.Errorf("expected Helm templates to be listed")
	}
	for _, tpl := range d.Templates {
		if !strings.HasPrefix(tpl, "apps/go-api/deploy/helm/templates/") {
			t.Errorf("template path not repo-rooted: %q", tpl)
		}
	}
}

// A values file that doesn't exist should fall back to values.yaml, not 404.
func TestBuild_ValuesFallback(t *testing.T) {
	d := Build(repoRoot(t), "go-api", "docker", "helm", "", "values-nonexistent.yaml")
	if d.ValuesFile == nil || d.ValuesFile.Path != "apps/go-api/deploy/helm/values.yaml" {
		t.Errorf("expected fallback to values.yaml, got %+v", d.ValuesFile)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
