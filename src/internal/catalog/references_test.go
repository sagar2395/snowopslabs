// SPDX-License-Identifier: Apache-2.0
package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeAsset writes a file at a nested path under an item directory, creating
// parent directories (the shared write helper assumes a flat item file).
func writeAsset(t *testing.T, root, dir, name, rel, body string) {
	t.Helper()
	full := filepath.Join(root, dir, name, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// M2: references + applyable snippets on scenarios and incidents. These tests
// pin the load-time guarantees: a dangling snippet path is caught (naming the
// file and reference), a malformed template in an inline snippet is caught, and
// well-formed references/snippets load clean.

func TestLoad_SnippetWithGoodPathAndRefs(t *testing.T) {
	root := t.TempDir()
	scn := validScenario + `references:
  - label: KEDA docs
    url: https://keda.sh/docs/
snippets:
  - label: apply the scaledobject
    path: manifests/so.yaml
  - label: inline note
    yaml: |
      apiVersion: v1
      kind: ConfigMap
`
	write(t, root, "scenarios", "s1", "scenario.yaml", scn)
	writeAsset(t, root, "scenarios", "s1", "manifests/so.yaml", "kind: ScaledObject\n")

	c, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !c.IsValid() {
		t.Fatalf("expected valid, got: %v", c.Problems())
	}
	s, ok := c.Scenario("s1")
	if !ok {
		t.Fatal("scenario s1 not indexed")
	}
	if len(s.References) != 1 || len(s.Snippets) != 2 {
		t.Fatalf("references=%d snippets=%d, want 1 and 2", len(s.References), len(s.Snippets))
	}
}

func TestLoad_SnippetPathDoesNotResolve(t *testing.T) {
	root := t.TempDir()
	scn := validScenario + `snippets:
  - label: ghost
    path: manifests/missing.yaml
`
	write(t, root, "scenarios", "s1", "scenario.yaml", scn)

	c, _ := Load(root)
	p := findProblem(t, c.Problems(), `path "manifests/missing.yaml" not found`)
	if p.Kind != KindScenario {
		t.Errorf("Kind = %s, want scenario", p.Kind)
	}
	if !strings.Contains(p.Message, `snippet "ghost"`) {
		t.Errorf("message should name the snippet: %q", p.Message)
	}
	if !strings.HasSuffix(p.File, filepath.Join("scenarios", "s1", "scenario.yaml")) {
		t.Errorf("File = %q, want the scenario.yaml", p.File)
	}
	if p.Line == 0 {
		t.Error("expected a non-zero line naming the offending path")
	}
}

func TestLoad_IncidentSnippetPathDoesNotResolve(t *testing.T) {
	root := t.TempDir()
	inc := validIncident + `snippets:
  - label: patch
    path: manifests/patch.yaml
`
	write(t, root, "incidents", "i1", "fault.yaml", inc)
	// Incident detection script must exist (contract from the loader).
	write(t, root, "incidents", "i1", "resolved.sh", "#!/usr/bin/env bash\n")

	c, _ := Load(root)
	p := findProblem(t, c.Problems(), `path "manifests/patch.yaml" not found`)
	if p.Kind != KindIncident {
		t.Errorf("Kind = %s, want incident", p.Kind)
	}
}

func TestLoad_SnippetBadTemplateIsCaught(t *testing.T) {
	root := t.TempDir()
	scn := validScenario + `snippets:
  - label: typo
    yaml: "namespace: {{.MonitoringNamspace}}"
`
	write(t, root, "scenarios", "s1", "scenario.yaml", scn)

	c, _ := Load(root)
	// The malformed template surfaces via the template-validation pass.
	findProblem(t, c.Problems(), "MonitoringNamspace")
}

func TestLoad_SnippetYamlAndPathMutuallyExclusive(t *testing.T) {
	root := t.TempDir()
	scn := validScenario + `snippets:
  - label: both
    yaml: "kind: ConfigMap"
    path: manifests/so.yaml
`
	write(t, root, "scenarios", "s1", "scenario.yaml", scn)

	c, _ := Load(root)
	findProblem(t, c.Problems(), "set either yaml or path, not both")
}

func TestLoad_ReferenceRequiresURL(t *testing.T) {
	root := t.TempDir()
	scn := validScenario + `references:
  - label: no url
`
	write(t, root, "scenarios", "s1", "scenario.yaml", scn)

	c, _ := Load(root)
	findProblem(t, c.Problems(), "url is required")
}
