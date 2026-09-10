// SPDX-License-Identifier: Apache-2.0
package incident

import (
	"testing"

	"github.com/sagar2395/snowopslabs/internal/workload"
)

// A fault whose target reads the binding follows it; one that pins a literal
// keeps breaking exactly the workload it names.
func TestTargetEnvFollowsBinding(t *testing.T) {
	tests := []struct {
		name    string
		target  Target
		bound   workload.Workload
		wantNS  string
		wantWkl string
	}{
		{
			name:    "templated target follows the default binding",
			target:  Target{Namespace: "{{.WorkloadNamespace}}", Workload: "{{.WorkloadName}}"},
			bound:   workload.Default("go-api"),
			wantNS:  "go-api",
			wantWkl: "go-api",
		},
		{
			name:    "templated target follows a rebinding",
			target:  Target{Namespace: "{{.WorkloadNamespace}}", Workload: "{{.WorkloadName}}"},
			bound:   workload.Workload{Name: "java-api", Namespace: "team-a"},
			wantNS:  "team-a",
			wantWkl: "java-api",
		},
		{
			name:    "literal target is unaffected by the binding",
			target:  Target{Namespace: "echo-server", Workload: "echo-server"},
			bound:   workload.Workload{Name: "java-api", Namespace: "team-a"},
			wantNS:  "echo-server",
			wantWkl: "echo-server",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := &Engine{ProjectRoot: "/repo", DomainSuffix: "k3d.local", Workload: tc.bound}
			env := e.targetEnv(&Fault{Target: tc.target})
			if env["TARGET_NAMESPACE"] != tc.wantNS {
				t.Errorf("TARGET_NAMESPACE = %q, want %q", env["TARGET_NAMESPACE"], tc.wantNS)
			}
			if env["TARGET_WORKLOAD"] != tc.wantWkl {
				t.Errorf("TARGET_WORKLOAD = %q, want %q", env["TARGET_WORKLOAD"], tc.wantWkl)
			}
		})
	}
}

// Before ADR-0014 the incident engine carried no monitoring namespace, so a
// fault naming it rendered "<no value>" into a kubectl argument.
func TestResolveTemplateMonitoringNamespace(t *testing.T) {
	e := &Engine{DomainSuffix: "k3d.local", MonitoringNamespace: "observability"}
	if got, want := e.resolveTemplate("{{.MonitoringNamespace}}"), "observability"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveTemplateLeavesForeignSyntaxAlone(t *testing.T) {
	e := &Engine{DomainSuffix: "k3d.local"}
	for _, in := range []string{"{{ $value }}", "{{ $labels.pod }}", "{{namespace}}"} {
		if got := e.resolveTemplate(in); got != in {
			t.Errorf("resolveTemplate(%q) = %q, want it unchanged", in, got)
		}
	}
}

func TestNewEngineBindsDefaultWorkload(t *testing.T) {
	e := NewEngine(t.TempDir(), "k3d.local")
	if e.Workload.Name != workload.DefaultApp {
		t.Errorf("NewEngine bound %q, want %q", e.Workload.Name, workload.DefaultApp)
	}
	if e.MonitoringNamespace == "" {
		t.Error("NewEngine left MonitoringNamespace empty")
	}
}

// Every path to a fault script must resolve the target. This guards the one
// that bypassed resolution: the CLI built the durable service's Target straight
// from the raw fault, so inject sent kubectl a literal "{{.WorkloadNamespace}}".
func TestResolvedTargetMatchesScriptEnv(t *testing.T) {
	e := &Engine{ProjectRoot: "/repo", Workload: workload.Workload{Name: "java-api", Namespace: "team-a"}}
	f := &Fault{Target: Target{Namespace: "{{.WorkloadNamespace}}", Workload: "{{.WorkloadName}}"}}

	rt := e.ResolvedTarget(f)
	if rt.Namespace != "team-a" || rt.Workload != "java-api" {
		t.Fatalf("ResolvedTarget = %+v, want team-a/java-api", rt)
	}
	env := e.targetEnv(f)
	if env["TARGET_NAMESPACE"] != rt.Namespace || env["TARGET_WORKLOAD"] != rt.Workload {
		t.Errorf("script env %v disagrees with ResolvedTarget %+v", env, rt)
	}
}

// The brief a learner reads on inject comes from Description, and the fault
// list's TARGET column from Target. Both were served raw, so every templated
// fault introduced itself as "{{.WorkloadName}}".
func TestGetResolvesReaderFacingFields(t *testing.T) {
	e := &Engine{
		Workload:   workload.Workload{Name: "shop", Namespace: "storefront"},
		faults:     make(map[string]*Fault),
		loadErrors: make(map[string]error),
	}
	e.faults["demo"] = &Fault{
		Name:        "demo",
		DisplayName: "{{.WorkloadName}} is sad",
		Description: "Latency on {{.WorkloadName}} crept up.",
		Target:      Target{Namespace: "{{.WorkloadNamespace}}", Workload: "{{.WorkloadName}}"},
	}

	got, err := e.Get("demo")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.DisplayName != "shop is sad" {
		t.Errorf("DisplayName = %q, want %q", got.DisplayName, "shop is sad")
	}
	if got.Description != "Latency on shop crept up." {
		t.Errorf("Description = %q", got.Description)
	}
	if got.Target.Namespace != "storefront" || got.Target.Workload != "shop" {
		t.Errorf("Target = %+v", got.Target)
	}
	if list := e.List(); len(list) != 1 || list[0].Target.Workload != "shop" {
		t.Errorf("List did not resolve the target column: %+v", list)
	}
	// The stored fault stays raw: the binding is chosen per run.
	if e.faults["demo"].Description != "Latency on {{.WorkloadName}} crept up." {
		t.Error("Get mutated the loaded fault")
	}
}

// A detection script runs on the checks.Runner, which does not inherit the
// executor's environment — so anything it needs has to come through targetEnv.
// Without the domain suffix a check cannot probe the workload's own ingress.
func TestTargetEnvCarriesTheContextAChecksScriptNeeds(t *testing.T) {
	e := &Engine{
		DomainSuffix:        "k3d.local",
		MonitoringNamespace: "observability",
		Workload:            workload.Workload{Name: "shop", Namespace: "storefront"},
	}
	env := e.targetEnv(&Fault{Target: Target{Namespace: "{{.WorkloadNamespace}}", Workload: "{{.WorkloadName}}"}})

	for k, want := range map[string]string{
		"TARGET_NAMESPACE":     "storefront",
		"TARGET_WORKLOAD":      "shop",
		"DOMAIN_SUFFIX":        "k3d.local",
		"MONITORING_NAMESPACE": "observability",
	} {
		if env[k] != want {
			t.Errorf("targetEnv[%q] = %q, want %q", k, env[k], want)
		}
	}
}
