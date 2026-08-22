// SPDX-License-Identifier: Apache-2.0
package checks

import (
	"strings"
	"testing"
)

func TestCheckValidate(t *testing.T) {
	tests := []struct {
		name    string
		check   Check
		wantErr string // substring of the expected error; "" means valid
	}{
		{
			name:  "valid http minimal",
			check: Check{Name: "grafana", Type: "http", URL: "http://grafana.k3d.local"},
		},
		{
			name:  "valid http full",
			check: Check{Name: "api", Type: "http", URL: "http://x", ExpectStatus: 204, BodyContains: "ok", TimeoutSeconds: 5},
		},
		{
			name:  "valid kubectl existence",
			check: Check{Name: "rules", Type: "kubectl", Resource: "prometheusrules", Namespace: "monitoring"},
		},
		{
			name: "valid kubectl jsonpath",
			check: Check{Name: "loki", Type: "kubectl", Resource: "statefulset/loki",
				JSONPath: "{.status.readyReplicas}", Operator: ">=", Value: "1"},
		},
		{
			name:  "valid promql",
			check: Check{Name: "p99", Type: "promql", Query: "up", Operator: "==", Value: "1"},
		},
		{
			name:  "valid script",
			check: Check{Name: "custom", Type: "script", Script: "checks/verify.sh"},
		},
		{
			name:    "missing name",
			check:   Check{Type: "http", URL: "http://x"},
			wantErr: "name is required",
		},
		{
			name:    "missing type",
			check:   Check{Name: "x"},
			wantErr: "type is required",
		},
		{
			name:    "unknown type",
			check:   Check{Name: "x", Type: "grpc"},
			wantErr: `unknown check type "grpc"`,
		},
		{
			name:    "http without url",
			check:   Check{Name: "x", Type: "http"},
			wantErr: "requires url",
		},
		{
			name:    "http bad status",
			check:   Check{Name: "x", Type: "http", URL: "http://x", ExpectStatus: 999},
			wantErr: "not a valid HTTP status",
		},
		{
			name:    "kubectl without resource",
			check:   Check{Name: "x", Type: "kubectl"},
			wantErr: "requires resource",
		},
		{
			name:    "kubectl jsonpath without operator",
			check:   Check{Name: "x", Type: "kubectl", Resource: "deploy/x", JSONPath: "{.x}"},
			wantErr: "requires operator and value",
		},
		{
			name:    "kubectl operator without jsonpath",
			check:   Check{Name: "x", Type: "kubectl", Resource: "deploy/x", Operator: "==", Value: "1"},
			wantErr: "no jsonpath",
		},
		{
			name:    "promql without query",
			check:   Check{Name: "x", Type: "promql", Operator: "<", Value: "1"},
			wantErr: "requires query",
		},
		{
			name:    "promql without operator",
			check:   Check{Name: "x", Type: "promql", Query: "up"},
			wantErr: "requires operator and value",
		},
		{
			name:    "bad operator",
			check:   Check{Name: "x", Type: "promql", Query: "up", Operator: "~=", Value: "1"},
			wantErr: `unknown operator "~="`,
		},
		{
			name:    "script without path",
			check:   Check{Name: "x", Type: "script"},
			wantErr: "requires script",
		},
		{
			name:    "negative timeout",
			check:   Check{Name: "x", Type: "http", URL: "http://x", TimeoutSeconds: -1},
			wantErr: "timeoutSeconds",
		},
		{
			name:    "http check with promql field",
			check:   Check{Name: "x", Type: "http", URL: "http://x", Query: "up"},
			wantErr: `fields not valid for type "http": query`,
		},
		{
			name:    "script check with kubectl fields",
			check:   Check{Name: "x", Type: "script", Script: "s.sh", Resource: "deploy/x", Namespace: "ns"},
			wantErr: `fields not valid for type "script": namespace, resource`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.check.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("expected valid, got: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestCompare(t *testing.T) {
	tests := []struct {
		got, op, want string
		pass          bool
		wantErr       bool
	}{
		{"1", ">=", "1", true, false},
		{"2", ">=", "1", true, false},
		{"0", ">=", "1", false, false},
		{"0.25", "<", "0.3", true, false},
		{"3", "==", "3.0", true, false},
		{"3", "!=", "4", true, false},
		{"abc", "==", "abc", true, false},
		{"abc", "!=", "abd", true, false},
		{"hello world", "contains", "world", true, false},
		{"hello", "contains", "world", false, false},
		{"abc", "<", "1", false, true}, // ordering needs numbers
		{"1", "~", "1", false, true},   // unknown operator
	}

	for _, tt := range tests {
		pass, err := compare(tt.got, tt.op, tt.want)
		if tt.wantErr {
			if err == nil {
				t.Errorf("compare(%q,%q,%q): expected error", tt.got, tt.op, tt.want)
			}
			continue
		}
		if err != nil {
			t.Errorf("compare(%q,%q,%q): unexpected error %v", tt.got, tt.op, tt.want, err)
			continue
		}
		if pass != tt.pass {
			t.Errorf("compare(%q,%q,%q) = %v, want %v", tt.got, tt.op, tt.want, pass, tt.pass)
		}
	}
}

func TestAllPass(t *testing.T) {
	if AllPass(nil) {
		t.Error("AllPass(nil) must be false — nothing verified is not success")
	}
	if AllPass([]Result{{Pass: true}, {Pass: false}}) {
		t.Error("AllPass with a failure must be false")
	}
	if !AllPass([]Result{{Pass: true}, {Pass: true}}) {
		t.Error("AllPass with all passes must be true")
	}
}
