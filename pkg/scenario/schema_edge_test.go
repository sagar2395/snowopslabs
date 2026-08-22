// SPDX-License-Identifier: Apache-2.0
package scenario

import (
	"strings"
	"testing"

	"github.com/sagar2395/snowopslabs/pkg/checks"
)

// TestValidate_MissingName verifies that a scenario without a name is rejected.
func TestValidate_MissingName(t *testing.T) {
	s := &Scenario{
		Components: []Component{{Name: "x", Type: "manifest", Path: "x.yaml"}},
	}
	err := s.Validate()
	if err == nil {
		t.Fatal("expected error for missing name, got nil")
	}
	if !strings.Contains(err.Error(), "name is required") {
		t.Errorf("error should mention 'name is required': %v", err)
	}
}

// TestValidate_DuplicateStageName verifies that duplicate stage names are rejected.
func TestValidate_DuplicateStageName(t *testing.T) {
	s := &Scenario{
		Name: "demo",
		Stages: []Stage{
			{Name: "setup", Components: []Component{{Name: "a", Type: "script", Script: "a.sh"}}},
			{Name: "setup", Components: []Component{{Name: "b", Type: "script", Script: "b.sh"}}},
		},
	}
	err := s.Validate()
	if err == nil {
		t.Fatal("expected error for duplicate stage name, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate stage name") {
		t.Errorf("error should mention 'duplicate stage name': %v", err)
	}
}

// TestValidate_DuplicateComponentName verifies that duplicate component names
// are rejected.
func TestValidate_DuplicateComponentName(t *testing.T) {
	s := &Scenario{
		Name: "demo",
		Components: []Component{
			{Name: "x", Type: "manifest", Path: "x.yaml"},
			{Name: "x", Type: "manifest", Path: "y.yaml"},
		},
	}
	err := s.Validate()
	if err == nil {
		t.Fatal("expected error for duplicate component name, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate component name") {
		t.Errorf("error should mention 'duplicate component name': %v", err)
	}
}

// TestValidate_HelmRequiresChart verifies the helm type requires a chart field.
func TestValidate_HelmRequiresChart(t *testing.T) {
	s := &Scenario{
		Name:       "demo",
		Components: []Component{{Name: "x", Type: "helm", Chart: ""}},
	}
	err := s.Validate()
	if err == nil {
		t.Fatal("expected error for helm without chart, got nil")
	}
	if !strings.Contains(err.Error(), "helm component requires chart") {
		t.Errorf("error should mention chart requirement: %v", err)
	}
}

// TestValidate_ScriptRequiresScript verifies the script type requires a script field.
func TestValidate_ScriptRequiresScript(t *testing.T) {
	s := &Scenario{
		Name:       "demo",
		Components: []Component{{Name: "x", Type: "script", Script: ""}},
	}
	err := s.Validate()
	if err == nil {
		t.Fatal("expected error for script without script field, got nil")
	}
	if !strings.Contains(err.Error(), "script component requires script") {
		t.Errorf("error should mention script requirement: %v", err)
	}
}

// TestValidate_CheckScriptUnsafePath verifies that a check with a path-escaping
// script is rejected.
func TestValidate_CheckScriptUnsafePath(t *testing.T) {
	s := &Scenario{
		Name:       "demo",
		Components: []Component{},
		Checks:     []checks.Check{{Name: "c", Type: "script", Script: "../../evil.sh"}},
	}
	err := s.Validate()
	if err == nil {
		t.Fatal("expected error for unsafe check script path, got nil")
	}
	if !strings.Contains(err.Error(), "inside the scenario directory") {
		t.Errorf("error should mention path restriction: %v", err)
	}
}

// TestUnsafePath covers the unsafePath helper for both safe and unsafe cases.
func TestUnsafePath(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"", false},
		{"values/test.yaml", false},
		{"manifests/app.yaml", false},
		{"../outside.yaml", true},
		{"/absolute/path.yaml", true},
		{"a/../b.yaml", true},
	}
	for _, tt := range tests {
		got := unsafePath(tt.input)
		if got != tt.want {
			t.Errorf("unsafePath(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// TestStagesOrDefault_StagedScenario verifies that a staged scenario returns
// its stages unchanged.
func TestStagesOrDefault_StagedScenario(t *testing.T) {
	s := &Scenario{
		Stages: []Stage{
			{Name: "baseline", Components: []Component{{Name: "a", Type: "script", Script: "a.sh"}}},
			{Name: "fault", Components: []Component{{Name: "b", Type: "script", Script: "b.sh"}}},
		},
	}
	stages := s.StagesOrDefault()
	if len(stages) != 2 {
		t.Errorf("StagesOrDefault() = %d stages, want 2", len(stages))
	}
	if stages[0].Name != "baseline" {
		t.Errorf("first stage name = %q, want %q", stages[0].Name, "baseline")
	}
}

// TestValidate_UnknownComponentType verifies that an unknown component type is rejected.
func TestValidate_UnknownComponentType(t *testing.T) {
	s := &Scenario{
		Name:       "demo",
		Components: []Component{{Name: "x", Type: "docker-compose"}},
	}
	err := s.Validate()
	if err == nil {
		t.Fatal("expected error for unknown component type, got nil")
	}
	if !strings.Contains(err.Error(), "unknown type") {
		t.Errorf("error should mention 'unknown type': %v", err)
	}
}

// TestValidate_ManifestRequiresPath verifies the manifest type requires a path field.
func TestValidate_ManifestRequiresPath(t *testing.T) {
	s := &Scenario{
		Name:       "demo",
		Components: []Component{{Name: "x", Type: "manifest", Path: ""}},
	}
	err := s.Validate()
	if err == nil {
		t.Fatal("expected error for manifest without path, got nil")
	}
}

// TestValidate_EmptyComponentName verifies that a component with an empty name is rejected.
func TestValidate_EmptyComponentName(t *testing.T) {
	s := &Scenario{
		Name:       "demo",
		Components: []Component{{Name: "", Type: "manifest", Path: "x.yaml"}},
	}
	err := s.Validate()
	if err == nil {
		t.Fatal("expected error for empty component name, got nil")
	}
}
