// SPDX-License-Identifier: Apache-2.0

package scenario

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/sagar2395/snowopslabs/internal/workload"
	"github.com/sagar2395/snowopslabs/pkg/checks"
)

// Every string a reader is shown must have its templates expanded. The test
// walks the whole response, so a newly added field is covered too.
func TestResolvedForDisplayLeavesNoTemplateAReaderCouldSee(t *testing.T) {
	e := NewEngine(t.TempDir(), "k3d.local", "k3d")
	s := &Scenario{
		Name:        "demo",
		DisplayName: "{{.WorkloadName}} demo",
		Description: "drives {{.WorkloadName}} in {{.WorkloadNamespace}}",
		Objectives:  []string{"watch {{.WorkloadName}} scale"},
		Prerequisites: Prerequisites{
			Platform: []string{"ingress"},
			Apps:     []string{"{{.WorkloadName}}"},
		},
		Components: []Component{{
			Name: "{{.WorkloadName}}-dash", Type: "grafana-dashboard",
			Namespace: "{{.MonitoringNamespace}}",
			Set:       map[string]string{"host": "app.{{.DomainSuffix}}"},
		}},
		Stages: []Stage{{
			Name:        "seed",
			Description: "seed {{.WorkloadName}}",
			Components:  []Component{{Name: "x", Namespace: "{{.WorkloadNamespace}}"}},
		}},
		Checks: []checks.Check{{
			Name: "up", Resource: "deploy/{{.WorkloadName}}", Namespace: "{{.WorkloadNamespace}}",
			Query: `rate({{.WorkloadMetric}}_count[5m])`, URL: "http://{{.WorkloadService}}:{{.WorkloadPort}}/",
			Remediation: "kubectl -n {{.WorkloadNamespace}} get deploy {{.WorkloadName}}",
		}},
		Explore: Explore{
			URLs:     []ExploreURL{{Label: "{{.WorkloadName}} app", URL: "http://app.{{.DomainSuffix}}"}},
			Commands: []ExploreCommand{{Label: "watch {{.WorkloadName}}", Command: "kubectl -n {{.WorkloadNamespace}} get po"}},
			Tips:     []string{"{{.WorkloadName}} exposes {{.WorkloadMetric}}"},
		},
		References: []Reference{{Label: "docs", URL: "https://example.invalid/", Note: "how {{.WorkloadName}} is wired"}},
		Snippets:   []Snippet{{Label: "patch {{.WorkloadName}}", Description: "in {{.WorkloadNamespace}}", YAML: "ns: {{.WorkloadNamespace}}"}},
	}

	got := e.ResolvedForDisplay(s, nil, workload.Default("java-api"))
	body, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Only labctl's own {{.Var}} placeholders. A Prometheus label template such
	// as {{ $labels.pod }} passes through deliberately.
	if left := regexp.MustCompile(`{{\.\w[^}]*}}`).FindAllString(string(body), -1); len(left) > 0 {
		t.Errorf("unresolved templates reached the reader: %v", left)
	}
	if !strings.Contains(got.Description, "java-api") {
		t.Errorf("resolved against the wrong binding: %q", got.Description)
	}
	if e.Workload.Name == "java-api" {
		t.Error("ResolvedForDisplay rebound the engine")
	}
	// The cached original, which other requests share, must be unchanged.
	if s.Description != "drives {{.WorkloadName}} in {{.WorkloadNamespace}}" {
		t.Errorf("the cached scenario was mutated: %q", s.Description)
	}
}

// A snippet file is rendered for the app the scenario runs against, not the
// engine's default binding.
func TestResolvedForDisplayRendersSnippetFilesForTheBinding(t *testing.T) {
	e := NewEngine(t.TempDir(), "k3d.local", "k3d")
	dir := t.TempDir()
	manifest := "# banner\nname: {{.WorkloadName}}\nthreshold: {{.Threshold}}\n"
	if err := os.WriteFile(filepath.Join(dir, "deploy.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Scenario{
		Name:       "demo",
		Dir:        dir,
		Parameters: []Parameter{{Name: "Threshold", Default: "25"}},
		Snippets:   []Snippet{{Label: "deploy", Path: "deploy.yaml"}},
	}

	got := e.ResolvedForDisplay(s, e.ParamDefaults(s), workload.Default("echo-server"))

	want := "name: echo-server\nthreshold: 25\n"
	if body := got.Snippets[0].YAML; body != want {
		t.Errorf("snippet body = %q, want %q", body, want)
	}
}

func TestRenderFile(t *testing.T) {
	e := NewEngine(t.TempDir(), "k3d.local", "k3d")
	e.Workload = workload.Default("echo-server")
	dir := t.TempDir()
	body := "# kept: this is what kubectl receives\nname: {{.WorkloadName}}-pdb\nhost: app.{{.DomainSuffix}}\nmin: {{.MinReplicas}}\n"
	if err := os.WriteFile(filepath.Join(dir, "pdb.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Scenario{Name: "demo", Dir: dir, Parameters: []Parameter{{Name: "MinReplicas", Default: "2"}}}

	tests := []struct {
		name    string
		file    string
		want    string
		wantErr bool
	}{
		{name: "binding and parameter defaults filled in", file: "pdb.yaml",
			want: "# kept: this is what kubectl receives\nname: echo-server-pdb\nhost: app.k3d.local\nmin: 2\n"},
		{name: "missing file", file: "nope.yaml", wantErr: true},
		{name: "path escaping the scenario", file: "../../etc/passwd", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := e.RenderFile(s, tt.file)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("RenderFile() = %q, want %q", got, tt.want)
			}
		})
	}
	if e.resolvedParams != nil {
		t.Errorf("RenderFile left activation parameters staged: %v", e.resolvedParams)
	}
}
