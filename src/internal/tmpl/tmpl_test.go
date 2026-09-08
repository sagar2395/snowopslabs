// SPDX-License-Identifier: Apache-2.0
package tmpl

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

func testContext() Context {
	return Context{
		DomainSuffix:        "k3d.local",
		MonitoringNamespace: "monitoring",
		ProjectRoot:         "/repo",
		LokiRetentionPeriod: "72h",
		IngressClass:        "traefik",
		WorkloadName:        "go-api",
		WorkloadNamespace:   "go-api",
		WorkloadService:     "go-api.go-api.svc.cluster.local",
		WorkloadPort:        "8080",
		WorkloadMetric:      "http_server_request_duration_seconds",
	}
}

// The drift this package exists to prevent: a field added to Context but never
// wired into Vars resolves at validation and vanishes at run time.
func TestVarsCoversEveryField(t *testing.T) {
	vars := Context{}.Vars()
	for _, name := range FieldNames() {
		if _, ok := vars[name]; !ok {
			t.Errorf("Context field %q is missing from Vars()", name)
		}
	}
	if len(vars) != len(FieldNames()) {
		t.Errorf("Vars() has %d keys, Context has %d fields", len(vars), len(FieldNames()))
	}
}

func TestVarsValuesMatchFields(t *testing.T) {
	ctx := testContext()
	vars := ctx.Vars()
	v := reflect.ValueOf(ctx)
	for i, name := range FieldNames() {
		if got, want := vars[name], v.Field(i).String(); got != want {
			t.Errorf("Vars()[%q] = %q, want %q", name, got, want)
		}
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		{name: "no template", input: "plain string"},
		{name: "empty", input: ""},
		{name: "known built-in", input: "http://grafana.{{.DomainSuffix}}"},
		{name: "known workload var", input: "deployment/{{.WorkloadName}}"},
		{name: "several known vars", input: "{{.WorkloadName}}.{{.WorkloadNamespace}}:{{.WorkloadPort}}"},
		{name: "spaces inside braces", input: "{{ .IngressClass }}"},

		// Other systems' templating shares these files and must pass untouched.
		{name: "loki line_format", input: `line_format "{{.method}} {{.path}} {{.status}}"`},
		{name: "prometheus value", input: "current value {{ $value }}"},
		{name: "prometheus labels", input: "pod {{ $labels.pod }} is down"},
		{name: "grafana legend", input: "{{namespace}}"},
		{name: "helm pipeline", input: `{{ index .data "x" | base64decode }}`},
		{name: "helm nested field", input: "{{ .Values.image.tag }}"},

		// A PascalCase name is claiming to be one of ours, so it must resolve.
		{name: "typo in a built-in", input: "{{.DomainSufix}}", wantErr: "DomainSufix"},
		{name: "typo in a workload var", input: "{{.WorkloadNam}}", wantErr: "WorkloadNam"},
		{name: "unknown var lists the known ones", input: "{{.Nope}}", wantErr: "DomainSuffix"},
		{name: "typo among valid vars is still caught", input: "{{.WorkloadName}} in {{.Wrongo}}", wantErr: "Wrongo"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.input)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate(%q) = %v, want nil", tc.input, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate(%q) = nil, want an error containing %q", tc.input, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err, tc.wantErr)
			}
		})
	}
}

// Validate must accept exactly what Expand resolves: a variable Expand
// substitutes must never be reported unknown, and vice versa.
func TestValidateAgreesWithExpand(t *testing.T) {
	for _, name := range FieldNames() {
		ref := "{{." + name + "}}"
		if err := Validate(ref); err != nil {
			t.Errorf("Validate(%q) = %v, want nil", ref, err)
		}
		if got := Expand(ref, testContext(), nil); got == ref {
			t.Errorf("Expand(%q) left it unresolved", ref)
		}
	}
}

// Expand must leave every other templating language that shares a content file
// completely alone. Each of these used to break a real apply.
func TestExpandLeavesForeignTemplatingAlone(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "prometheus value", input: "current value {{ $value }}"},
		{name: "prometheus labels", input: "pod {{ $labels.pod }} is down"},
		{name: "grafana legend", input: "{{namespace}}"},
		{name: "helm sprig pipeline", input: `{{ index .data "x" | base64decode }}`},
		{name: "helm nested field", input: "{{ .Values.image.tag }}"},
		{name: "unknown labctl var", input: "{{.NotAThing}}"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Expand(tc.input, testContext(), nil); got != tc.input {
				t.Errorf("Expand(%q) = %q, want it unchanged", tc.input, got)
			}
		})
	}
}

func TestExpand(t *testing.T) {
	tests := []struct {
		name  string
		input string
		extra map[string]string
		want  string
	}{
		{name: "empty input", input: "", want: ""},
		{name: "no braces", input: "plain", want: "plain"},
		{name: "built-in", input: "ns={{.MonitoringNamespace}}", want: "ns=monitoring"},
		{name: "workload service", input: "http://{{.WorkloadService}}:{{.WorkloadPort}}/", want: "http://go-api.go-api.svc.cluster.local:8080/"},
		{name: "extra value", input: "max={{.MaxReplicas}}", extra: map[string]string{"MaxReplicas": "6"}, want: "max=6"},
		{
			name:  "extra cannot shadow a built-in",
			input: "{{.DomainSuffix}}",
			extra: map[string]string{"DomainSuffix": "hijacked"},
			want:  "k3d.local",
		},
		{
			name:  "mixed labctl and prometheus syntax",
			input: "{{.WorkloadName}} in {{.WorkloadNamespace}} at {{ $value }}",
			want:  "go-api in go-api at {{ $value }}",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Expand(tc.input, testContext(), tc.extra); got != tc.want {
				t.Errorf("Expand(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// An empty binding must not silently produce "deployment/" — callers are
// expected to populate the workload before expanding content.
func TestExpandZeroContextEmptiesKnownVars(t *testing.T) {
	if got := Expand("deployment/{{.WorkloadName}}", Context{}, nil); got != "deployment/" {
		t.Errorf("got %q, want %q", got, "deployment/")
	}
}

func TestFieldNamesIsStable(t *testing.T) {
	got := FieldNames()
	if len(got) == 0 {
		t.Fatal("FieldNames() is empty")
	}
	sorted := append([]string(nil), got...)
	sort.Strings(sorted)
	for _, n := range sorted {
		if n == "" || strings.ToUpper(n[:1]) != n[:1] {
			t.Errorf("field %q is not exported", n)
		}
	}
}
