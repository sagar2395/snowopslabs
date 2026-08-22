// SPDX-License-Identifier: Apache-2.0
package catalog

import (
	"strings"
	"testing"
)

func TestResolve(t *testing.T) {
	ctx := DefaultTemplateContext("/proj")
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr string
	}{
		{"no template", "plain string", "plain string", ""},
		{"domain suffix", "http://x.{{.DomainSuffix}}/y", "http://x.k3d.local/y", ""},
		{"monitoring ns", "{{.MonitoringNamespace}}", "monitoring", ""},
		{"project root", "{{.ProjectRoot}}/bin", "/proj/bin", ""},
		{"unknown key", "{{.Bogus}}", "", "Bogus"},
		{"malformed", "{{.DomainSuffix", "", "malformed template"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(tt.in, ctx)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("Resolve(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestProblemString(t *testing.T) {
	tests := []struct {
		name string
		p    Problem
		want string
	}{
		{
			"with line",
			Problem{Kind: KindPath, Name: "p1", File: "learn/p1/path.yaml", Line: 7, Message: "boom"},
			"learn/p1/path.yaml:7: [path/p1] boom",
		},
		{
			"without line",
			Problem{Kind: KindScenario, Name: "s1", File: "s.yaml", Message: "bad"},
			"s.yaml: [scenario/s1] bad",
		},
		{
			"without name",
			Problem{Kind: KindIncident, File: "f.yaml", Message: "x"},
			"f.yaml: [incident] x",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.p.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProblemsSorted(t *testing.T) {
	c := newCatalog(nil)
	c.problems = []Problem{
		{File: "b.yaml", Line: 2, Message: "z"},
		{File: "a.yaml", Line: 5, Message: "a"},
		{File: "a.yaml", Line: 1, Message: "a"},
	}
	got := c.Problems()
	if got[0].File != "a.yaml" || got[0].Line != 1 {
		t.Errorf("first = %v, want a.yaml:1", got[0])
	}
	if got[2].File != "b.yaml" {
		t.Errorf("last = %v, want b.yaml", got[2])
	}
}
