// SPDX-License-Identifier: Apache-2.0
package scenario

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/sagar2395/snowopslabs/pkg/checks"
)

const v2ScenarioYAML = `name: v2-scenario
displayName: V2 Scenario
description: Exercises stages, objectives, and checks
category: testing
objectives:
  - "Bring the stack up"
  - "Keep p99 latency under 300ms"
stages:
  - name: baseline
    description: Install the baseline stack
    components:
      - name: base-helm
        type: helm
        chart: test/chart
        namespace: test-ns
  - name: inject
    components:
      - name: fault-manifest
        type: manifest
        path: manifests/test.yaml
checks:
  - name: api-healthy
    type: http
    url: "http://api.{{.DomainSuffix}}/health"
    expectStatus: 200
  - name: replicas-ready
    type: kubectl
    resource: deployment/base
    namespace: "{{.MonitoringNamespace}}"
    jsonpath: "{.status.readyReplicas}"
    operator: ">="
    value: "1"
  - name: latency-ok
    type: promql
    query: 'histogram_quantile(0.99, sum(rate(http_request_duration_seconds_bucket[5m])) by (le))'
    operator: "<"
    value: "0.3"
`

func parse(t *testing.T, yamlStr string) *Scenario {
	t.Helper()
	var s Scenario
	if err := yaml.Unmarshal([]byte(yamlStr), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return &s
}

func TestParseV2Scenario(t *testing.T) {
	s := parse(t, v2ScenarioYAML)
	if err := s.Validate(); err != nil {
		t.Fatalf("v2 scenario should be valid: %v", err)
	}
	if len(s.Objectives) != 2 {
		t.Errorf("objectives: got %d, want 2", len(s.Objectives))
	}
	if len(s.Stages) != 2 {
		t.Fatalf("stages: got %d, want 2", len(s.Stages))
	}
	if s.Stages[0].Name != "baseline" || s.Stages[1].Name != "inject" {
		t.Errorf("stage names wrong: %q, %q", s.Stages[0].Name, s.Stages[1].Name)
	}
	if len(s.Checks) != 3 {
		t.Fatalf("checks: got %d, want 3", len(s.Checks))
	}
	if s.Checks[1].Operator != ">=" || s.Checks[1].Value != "1" {
		t.Errorf("kubectl check fields not parsed: %+v", s.Checks[1])
	}
}

func TestAllComponents(t *testing.T) {
	v2 := parse(t, v2ScenarioYAML)
	all := v2.AllComponents()
	if len(all) != 2 {
		t.Fatalf("v2 AllComponents: got %d, want 2", len(all))
	}
	if all[0].Name != "base-helm" || all[1].Name != "fault-manifest" {
		t.Errorf("stage flattening order wrong: %q, %q", all[0].Name, all[1].Name)
	}

	v1 := parse(t, testScenarioYAML)
	if len(v1.AllComponents()) != 2 {
		t.Fatalf("v1 AllComponents: got %d, want 2", len(v1.AllComponents()))
	}
}

func TestValidate_V1BackwardCompatible(t *testing.T) {
	for name, yamlStr := range map[string]string{
		"flat components":    testScenarioYAML,
		"empty components":   minimalScenarioYAML,
		"runtime restricted": scenarioWithRuntimesYAML,
	} {
		s := parse(t, yamlStr)
		if err := s.Validate(); err != nil {
			t.Errorf("%s: v1 scenario must stay valid: %v", name, err)
		}
	}
}

func TestValidate_Errors(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Scenario)
		wantErr string
	}{
		{
			name:    "missing name",
			mutate:  func(s *Scenario) { s.Name = "" },
			wantErr: "name is required",
		},
		{
			name: "components and stages together",
			mutate: func(s *Scenario) {
				s.Components = []Component{{Name: "x", Type: "helm", Chart: "c/x"}}
			},
			wantErr: "not both",
		},
		{
			name:    "stage without name",
			mutate:  func(s *Scenario) { s.Stages[0].Name = "" },
			wantErr: "stage 1: name is required",
		},
		{
			name:    "duplicate stage name",
			mutate:  func(s *Scenario) { s.Stages[1].Name = "baseline" },
			wantErr: "duplicate stage name",
		},
		{
			name: "duplicate component name",
			mutate: func(s *Scenario) {
				s.Stages[1].Components[0].Name = "base-helm"
				s.Stages[1].Components[0].Type = "helm"
				s.Stages[1].Components[0].Chart = "c/x"
			},
			wantErr: "duplicate component name",
		},
		{
			name:    "unknown component type",
			mutate:  func(s *Scenario) { s.Stages[0].Components[0].Type = "kustomize" },
			wantErr: `unknown type "kustomize"`,
		},
		{
			name: "helm without chart",
			mutate: func(s *Scenario) {
				s.Stages[0].Components[0].Chart = ""
			},
			wantErr: "requires chart",
		},
		{
			name:    "manifest without path",
			mutate:  func(s *Scenario) { s.Stages[1].Components[0].Path = "" },
			wantErr: "requires path",
		},
		{
			name:    "invalid check bubbles up",
			mutate:  func(s *Scenario) { s.Checks[0].URL = "" },
			wantErr: "requires url",
		},
		{
			name:    "duplicate check name",
			mutate:  func(s *Scenario) { s.Checks[1].Name = s.Checks[0].Name },
			wantErr: "duplicate check name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := parse(t, v2ScenarioYAML)
			tt.mutate(s)
			err := s.Validate()
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestScan_RejectsInvalidV2AndRecordsError(t *testing.T) {
	root := t.TempDir()
	bad := strings.Replace(v2ScenarioYAML, "type: http", "type: gopher", 1)
	createTestScenario(t, root, "bad-v2", bad)

	engine := NewEngine(root, "k3d.local", "k3d")
	if len(engine.List()) != 0 {
		t.Error("invalid v2 scenario must not load")
	}
	errs := engine.LoadErrors()
	if len(errs) != 1 {
		t.Fatalf("expected 1 load error, got %d", len(errs))
	}
	if err := errs["bad-v2"]; err == nil || !strings.Contains(err.Error(), "gopher") {
		t.Errorf("load error should name the bad type: %v", err)
	}
}

func TestPreflight_StagedComponentsAndCheckScripts(t *testing.T) {
	root := t.TempDir()
	yamlStr := v2ScenarioYAML + `  - name: custom
    type: script
    script: checks/custom.sh
`
	createTestScenario(t, root, "v2-scenario", yamlStr)

	engine := NewEngine(root, "k3d.local", "k3d")
	s, err := engine.Get("v2-scenario")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// Check script missing → preflight must fail and name it.
	err = engine.Preflight(s)
	if err == nil || !strings.Contains(err.Error(), "custom.sh") {
		t.Fatalf("expected preflight failure naming the missing check script, got: %v", err)
	}

	// Create it → preflight passes (manifests/test.yaml exists via helper).
	dir := filepath.Join(root, "scenarios", "v2-scenario", "checks")
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "custom.sh"), []byte("#!/usr/bin/env bash\nexit 0\n"), 0755)
	if err := engine.Preflight(s); err != nil {
		t.Fatalf("expected preflight pass, got: %v", err)
	}
}

func TestVerify(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	root := t.TempDir()
	yamlStr := `name: verify-scenario
displayName: Verify
description: test
category: testing
components: []
checks:
  - name: api-up
    type: http
    url: "` + srv.URL + `/{{.DomainSuffix}}"
`
	createTestScenario(t, root, "verify-scenario", yamlStr)

	engine := NewEngine(root, "test.local", "k3d")
	runner := checks.NewRunner()
	runner.DefaultTimeout = 5 * time.Second

	results, err := engine.Verify(context.Background(), "verify-scenario", runner)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(results) != 1 || !results[0].Pass {
		t.Fatalf("expected 1 passing result, got %+v", results)
	}
	if runner.ScriptDir == "" {
		t.Error("Verify must point the runner at the scenario dir for script checks")
	}
}

func TestVerify_NoChecks(t *testing.T) {
	root := t.TempDir()
	createTestScenario(t, root, "minimal-scenario", minimalScenarioYAML)

	engine := NewEngine(root, "k3d.local", "k3d")
	_, err := engine.Verify(context.Background(), "minimal-scenario", checks.NewRunner())
	if err == nil || !strings.Contains(err.Error(), "no checks") {
		t.Fatalf("expected ErrNoChecks, got: %v", err)
	}
}

func TestVerify_UnknownScenario(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "scenarios"), 0755)

	engine := NewEngine(root, "k3d.local", "k3d")
	_, err := engine.Verify(context.Background(), "ghost", checks.NewRunner())
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got: %v", err)
	}
}

func TestResolveCheck_Templates(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "scenarios"), 0755)
	engine := NewEngine(root, "lab.example", "k3d")
	engine.MonitoringNamespace = "obs"

	c := engine.resolveCheck(checks.Check{
		Name: "x", Type: "kubectl",
		Resource:  "statefulset/loki",
		Namespace: "{{.MonitoringNamespace}}",
		JSONPath:  "{.status.readyReplicas}", // single braces must survive untouched
		Operator:  ">=", Value: "1",
	})
	if c.Namespace != "obs" {
		t.Errorf("namespace not resolved: %q", c.Namespace)
	}
	if c.JSONPath != "{.status.readyReplicas}" {
		t.Errorf("jsonpath mangled by template resolution: %q", c.JSONPath)
	}

	h := engine.resolveCheck(checks.Check{Name: "h", Type: "http", URL: "http://grafana.{{.DomainSuffix}}"})
	if h.URL != "http://grafana.lab.example" {
		t.Errorf("url not resolved: %q", h.URL)
	}
}
