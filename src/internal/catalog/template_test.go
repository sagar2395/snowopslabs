// SPDX-License-Identifier: Apache-2.0
package catalog

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr string
	}{
		{name: "no template", in: "plain string"},
		{name: "domain suffix", in: "http://x.{{.DomainSuffix}}/y"},
		{name: "monitoring ns", in: "{{.MonitoringNamespace}}"},
		{name: "project root", in: "{{.ProjectRoot}}/bin"},
		{name: "ingress class", in: "{{.IngressClass}}"},
		{name: "workload name", in: "deployment/{{.WorkloadName}}"},
		// Foreign templating shares these files; validation must not claim it.
		{name: "loki line_format", in: `line_format "{{.method}} {{.status}}"`},
		{name: "prometheus annotation", in: "value {{ $value }}"},
		{name: "unknown key", in: "{{.Bogus}}", wantErr: "Bogus"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.in)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Errorf("Validate(%q) = %v, want nil", tt.in, err)
			}
		})
	}
}

// The defaults exist so validation has a populated context to describe; every
// field must be set or a caller reading them gets a misleading blank.
func TestDefaultTemplateContextIsPopulated(t *testing.T) {
	ctx := DefaultTemplateContext("/proj")
	for name, val := range ctx.Vars() {
		if val == "" {
			t.Errorf("DefaultTemplateContext leaves %q empty", name)
		}
	}
}
