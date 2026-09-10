// SPDX-License-Identifier: Apache-2.0
package scenario

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/sagar2395/snowopslabs/internal/workload"
	"github.com/sagar2395/snowopslabs/pkg/checks"
)

// Anything an author can write and a reader can read must come back resolved.
// The list of resolved fields used to live in the API handler and grew one field
// at a time, which is how stage descriptions, explore labels and check
// remediations each shipped a literal "{{.WorkloadName}}" into the UI.
//
// This walks the whole response rather than naming fields, so a field added to
// the schema and forgotten here fails instead of shipping.
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
	// The original must be untouched — it is the cached scenario every other
	// request is served from.
	if s.Description != "drives {{.WorkloadName}} in {{.WorkloadNamespace}}" {
		t.Errorf("the cached scenario was mutated: %q", s.Description)
	}
}
