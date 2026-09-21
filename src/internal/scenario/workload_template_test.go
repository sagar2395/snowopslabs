// SPDX-License-Identifier: Apache-2.0
package scenario

import (
	"testing"

	"github.com/sagar2395/snowopslabs/internal/workload"
)

func TestResolveTemplateWorkloadVars(t *testing.T) {
	e := &Engine{
		ProjectRoot:         "/repo",
		DomainSuffix:        "k3d.local",
		MonitoringNamespace: "monitoring",
		Workload:            workload.Default("go-api"),
	}
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "name", input: "deployment/{{.WorkloadName}}", want: "deployment/go-api"},
		{name: "namespace", input: "{{.WorkloadNamespace}}", want: "go-api"},
		{name: "service", input: "{{.WorkloadService}}", want: "go-api.go-api.svc.cluster.local"},
		{name: "port", input: "{{.WorkloadPort}}", want: "8080"},
		{name: "metric", input: "rate({{.WorkloadMetric}}_count[5m])", want: "rate(http_server_request_duration_seconds_count[5m])"},
		{name: "existing built-in still resolves", input: "{{.MonitoringNamespace}}", want: "monitoring"},
		{name: "prometheus syntax untouched", input: "{{ $value }}", want: "{{ $value }}"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := e.ResolveTemplate(tc.input); got != tc.want {
				t.Errorf("ResolveTemplate(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// Rebinding the engine to another app must move every workload reference with
// it — that is the whole point of the binding.
func TestResolveTemplateFollowsRebinding(t *testing.T) {
	e := &Engine{DomainSuffix: "k3d.local", Workload: workload.Default("go-api")}
	const in = "{{.WorkloadName}} in {{.WorkloadNamespace}} at {{.WorkloadService}}"
	if got, want := e.ResolveTemplate(in), "go-api in go-api at go-api.go-api.svc.cluster.local"; got != want {
		t.Fatalf("before rebind: got %q, want %q", got, want)
	}
	e.Workload = workload.Workload{Name: "java-api", Namespace: "team-a", Port: "9090"}
	if got, want := e.ResolveTemplate(in), "java-api in team-a at java-api.team-a.svc.cluster.local"; got != want {
		t.Fatalf("after rebind: got %q, want %q", got, want)
	}
	if got, want := e.ResolveTemplate("{{.WorkloadPort}}"), "9090"; got != want {
		t.Errorf("port: got %q, want %q", got, want)
	}
}

// A parameter must never shadow a workload variable, or a scenario could
// redirect a fault at a workload it was not bound to.
func TestParamCannotShadowWorkloadVar(t *testing.T) {
	e := &Engine{Workload: workload.Default("go-api")}
	got := e.ResolveTemplateWithParams("{{.WorkloadName}}", map[string]string{"WorkloadName": "hijacked"})
	if got != "go-api" {
		t.Errorf("got %q, want %q", got, "go-api")
	}
}

func TestNewEngineBindsDefaultWorkload(t *testing.T) {
	e := NewEngine(t.TempDir(), "k3d.local", "k3d")
	if e.Workload.Name != workload.DefaultApp {
		t.Errorf("NewEngine bound %q, want %q", e.Workload.Name, workload.DefaultApp)
	}
	if got := e.ResolveTemplate("{{.WorkloadService}}"); got == "" || got == "{{.WorkloadService}}" {
		t.Errorf("default binding did not resolve workload vars: %q", got)
	}
}
