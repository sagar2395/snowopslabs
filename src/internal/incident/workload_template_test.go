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
