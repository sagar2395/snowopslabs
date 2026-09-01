// SPDX-License-Identifier: Apache-2.0
package scenario

import (
	"strings"
	"testing"
)

func intp(n int) *int { return &n }

func TestValidateParameters_AcceptsWellFormed(t *testing.T) {
	params := []Parameter{
		{Name: "MinReplicas", Default: "1", Type: "int", Min: intp(1), Max: intp(10), NotGreaterThan: "MaxReplicas"},
		{Name: "MaxReplicas", Default: "6", Type: "int", Min: intp(1), Max: intp(10)},
		{Name: "Threshold", Default: "25", Type: "int", Min: intp(1)},
		{Name: "Note", Default: "hello"}, // string param, no bounds
	}
	if errs := ValidateParameters(params); len(errs) != 0 {
		t.Fatalf("expected no errors, got: %v", errs)
	}
}

func TestValidateParameters_RejectsBadDeclarations(t *testing.T) {
	cases := []struct {
		name   string
		params []Parameter
		want   string
	}{
		{"missing name", []Parameter{{Default: "1"}}, "name is required"},
		{"bad template name", []Parameter{{Name: "has-dash", Default: "1"}}, "template var"},
		{"duplicate name", []Parameter{{Name: "X", Default: "1"}, {Name: "X", Default: "2"}}, "duplicate"},
		{"unknown type", []Parameter{{Name: "X", Default: "1", Type: "float"}}, "unknown type"},
		{"missing default", []Parameter{{Name: "X", Type: "int"}}, "default is required"},
		{"default out of bounds", []Parameter{{Name: "X", Default: "99", Type: "int", Max: intp(10)}}, "above the maximum"},
		{"default not int", []Parameter{{Name: "X", Default: "abc", Type: "int"}}, "not an integer"},
		{"notGreaterThan unknown", []Parameter{{Name: "X", Default: "1", Type: "int", NotGreaterThan: "Ghost"}}, "references unknown parameter"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := strings.Join(ValidateParameters(tc.params), "\n")
			if !strings.Contains(errs, tc.want) {
				t.Errorf("expected an error containing %q, got: %q", tc.want, errs)
			}
		})
	}
}

func TestParameter_ValidateValue(t *testing.T) {
	p := Parameter{Name: "Threshold", Type: "int", Min: intp(1), Max: intp(500)}
	if err := p.ValidateValue("25", "override"); err != nil {
		t.Errorf("25 should be valid: %v", err)
	}
	if err := p.ValidateValue("0", "override"); err == nil {
		t.Error("0 is below min, should fail")
	}
	if err := p.ValidateValue("9999", "override"); err == nil {
		t.Error("9999 is above max, should fail")
	}
	if err := p.ValidateValue("abc", "override"); err == nil {
		t.Error("non-integer should fail")
	}
	// A string parameter accepts anything.
	s := Parameter{Name: "Note", Type: "string"}
	if err := s.ValidateValue("anything at all", "override"); err != nil {
		t.Errorf("string param should accept any value: %v", err)
	}
}
