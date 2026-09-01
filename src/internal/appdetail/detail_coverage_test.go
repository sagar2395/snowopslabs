// SPDX-License-Identifier: Apache-2.0
package appdetail

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeApp creates apps/<name>/<relpath> files under a temp project root and
// returns the root, so tests can drive Build against a controlled layout.
func writeApp(t *testing.T, name string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		full := filepath.Join(root, "apps", name, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// Description falls back to the Helm chart's description when there is no README.
func TestBuild_DescriptionFallsBackToChart(t *testing.T) {
	root := writeApp(t, "svc", map[string]string{
		"deploy/helm/Chart.yaml": "name: svc\ndescription: A synthetic service\nversion: 0.1.0\n",
	})
	d := Build(root, "svc", "docker", "helm", "", "values.yaml")
	if d.Description != "A synthetic service" {
		t.Errorf("Description = %q, want the Chart.yaml description", d.Description)
	}
}

// README prose wins over the chart description, skipping headings and badges.
func TestBuild_DescriptionPrefersReadmeProse(t *testing.T) {
	root := writeApp(t, "svc", map[string]string{
		"README.md":              "# Title\n\n![badge](x)\n[!note]\n\nThe real summary line.\n",
		"deploy/helm/Chart.yaml": "name: svc\ndescription: chart desc\n",
	})
	d := Build(root, "svc", "docker", "helm", "", "values.yaml")
	if d.Description != "The real summary line." {
		t.Errorf("Description = %q, want the README prose line", d.Description)
	}
}

// With neither a README nor a chart, description is empty (no error).
func TestBuild_DescriptionEmptyWhenNothing(t *testing.T) {
	root := writeApp(t, "svc", map[string]string{"main.py": "print('hi')\n"})
	d := Build(root, "svc", "docker", "helm", "", "values.yaml")
	if d.Description != "" {
		t.Errorf("Description = %q, want empty", d.Description)
	}
}

func TestDetectTech_NodeAndPython(t *testing.T) {
	rootNode := writeApp(t, "web", map[string]string{"package.json": "{}", "Dockerfile": "FROM node"})
	if tech := Build(rootNode, "web", "", "", "", "").Tech; !contains(tech, "Node.js") || !contains(tech, "Docker") {
		t.Errorf("Node app tech = %v", tech)
	}
	rootPy := writeApp(t, "api", map[string]string{"requirements.txt": "flask\n"})
	if tech := Build(rootPy, "api", "", "", "", "").Tech; !contains(tech, "Python") {
		t.Errorf("Python (requirements) tech = %v", tech)
	}
	rootPy2 := writeApp(t, "api", map[string]string{"pyproject.toml": "[project]\n"})
	if tech := Build(rootPy2, "api", "", "", "", "").Tech; !contains(tech, "Python") {
		t.Errorf("Python (pyproject) tech = %v", tech)
	}
}

// A file larger than maxFileBytes is truncated and flagged.
func TestReadFile_Truncates(t *testing.T) {
	big := strings.Repeat("x", maxFileBytes+500)
	root := writeApp(t, "svc", map[string]string{"Dockerfile": big})
	d := Build(root, "svc", "docker", "helm", "", "values.yaml")
	if d.Dockerfile == nil {
		t.Fatal("expected a Dockerfile FileRef")
	}
	if !d.Dockerfile.Truncated {
		t.Error("expected Truncated = true for an oversized file")
	}
	if len(d.Dockerfile.Content) != maxFileBytes {
		t.Errorf("truncated content len = %d, want %d", len(d.Dockerfile.Content), maxFileBytes)
	}
}

// A small file is returned whole and not flagged.
func TestReadFile_NotTruncated(t *testing.T) {
	root := writeApp(t, "svc", map[string]string{"Dockerfile": "FROM scratch\n"})
	d := Build(root, "svc", "docker", "helm", "", "values.yaml")
	if d.Dockerfile == nil || d.Dockerfile.Truncated {
		t.Errorf("small Dockerfile should be present and untruncated: %+v", d.Dockerfile)
	}
	if d.Dockerfile.Path != "apps/svc/Dockerfile" {
		t.Errorf("Dockerfile path = %q", d.Dockerfile.Path)
	}
}
