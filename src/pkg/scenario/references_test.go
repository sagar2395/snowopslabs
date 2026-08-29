// SPDX-License-Identifier: Apache-2.0
package scenario

import (
	"strings"
	"testing"
)

func TestValidateReferences(t *testing.T) {
	cases := []struct {
		name string
		refs []Reference
		want string // substring expected in the joined errors; "" means valid
	}{
		{"valid https", []Reference{{Label: "KEDA", URL: "https://keda.sh"}}, ""},
		{"valid http", []Reference{{Label: "local", URL: "http://grafana.k3d.local"}}, ""},
		{"missing label", []Reference{{URL: "https://x"}}, "label is required"},
		{"missing url", []Reference{{Label: "x"}}, "url is required"},
		{"non-http scheme", []Reference{{Label: "x", URL: "ftp://x"}}, "must start with http"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := strings.Join(ValidateReferences(tc.refs), "\n")
			if tc.want == "" && errs != "" {
				t.Fatalf("expected valid, got: %s", errs)
			}
			if tc.want != "" && !strings.Contains(errs, tc.want) {
				t.Fatalf("errors = %q, want substring %q", errs, tc.want)
			}
		})
	}
}

func TestValidateSnippets(t *testing.T) {
	cases := []struct {
		name  string
		snips []Snippet
		want  string
	}{
		{"valid inline", []Snippet{{Label: "x", YAML: "kind: ConfigMap"}}, ""},
		{"valid path", []Snippet{{Label: "x", Path: "manifests/a.yaml"}}, ""},
		{"missing label", []Snippet{{YAML: "kind: X"}}, "label is required"},
		{"neither yaml nor path", []Snippet{{Label: "x"}}, "one of yaml or path is required"},
		{"both yaml and path", []Snippet{{Label: "x", YAML: "a", Path: "b"}}, "not both"},
		{"unsafe absolute path", []Snippet{{Label: "x", Path: "/etc/passwd"}}, "must be a relative path"},
		{"unsafe dotdot path", []Snippet{{Label: "x", Path: "../escape.yaml"}}, "must be a relative path"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := strings.Join(ValidateSnippets(tc.snips), "\n")
			if tc.want == "" && errs != "" {
				t.Fatalf("expected valid, got: %s", errs)
			}
			if tc.want != "" && !strings.Contains(errs, tc.want) {
				t.Fatalf("errors = %q, want substring %q", errs, tc.want)
			}
		})
	}
}

// TestValidate_IncludesReferenceAndSnippetProblems proves the fields are wired
// into the scenario's own Validate, not only the standalone helpers.
func TestValidate_IncludesReferenceAndSnippetProblems(t *testing.T) {
	s := &Scenario{
		Name:       "s1",
		Components: []Component{{Name: "c", Type: "script", Script: "run.sh"}},
		References: []Reference{{Label: "bad"}},
		Snippets:   []Snippet{{Label: "bad"}},
	}
	err := s.Validate()
	if err == nil {
		t.Fatal("expected validation errors")
	}
	for _, want := range []string{"url is required", "one of yaml or path is required"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
}
