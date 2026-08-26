// SPDX-License-Identifier: Apache-2.0
package scenario

import (
	"strings"
	"testing"
)

func TestAPIVersionSupported(t *testing.T) {
	cases := map[string]bool{
		"":                         true, // empty => default
		"scenario.snowops.net/v2":  true,
		"scenario.snowops.net/v99": false,
		"scenario.example.com/v2":  false,
	}
	for v, want := range cases {
		if got := APIVersionSupported(v); got != want {
			t.Errorf("APIVersionSupported(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestValidate_AcceptsMinimalAndRejectsBad(t *testing.T) {
	// Minimal valid v1 scenario (flat components).
	ok := &Scenario{
		Name: "demo",
		Components: []Component{
			{Name: "x", Type: "manifest", Path: "manifests/x.yaml"},
		},
	}
	if err := ok.Validate(); err != nil {
		t.Fatalf("expected valid, got: %v", err)
	}

	// Unsupported apiVersion + both components and stages => errors.
	bad := &Scenario{
		Name:       "demo",
		APIVersion: "scenario.snowops.net/v99",
		Components: []Component{{Name: "x", Type: "manifest", Path: "x.yaml"}},
		Stages:     []Stage{{Name: "s", Components: []Component{{Name: "y", Type: "script", Script: "s.sh"}}}},
	}
	err := bad.Validate()
	if err == nil {
		t.Fatal("expected validation errors, got nil")
	}
	for _, want := range []string{"unsupported apiVersion", "either components"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}

func TestValidate_RejectsPathEscape(t *testing.T) {
	s := &Scenario{
		Name:       "demo",
		Components: []Component{{Name: "x", Type: "manifest", Path: "../escape.yaml"}},
	}
	if err := s.Validate(); err == nil || !strings.Contains(err.Error(), "inside the scenario directory") {
		t.Fatalf("expected path-escape rejection, got: %v", err)
	}
}

func TestStagesOrDefaultAndAllComponents(t *testing.T) {
	flat := &Scenario{Components: []Component{{Name: "a"}, {Name: "b"}}}
	if got := flat.StagesOrDefault(); len(got) != 1 || len(got[0].Components) != 2 {
		t.Errorf("flat StagesOrDefault wrong: %+v", got)
	}
	if got := flat.AllComponents(); len(got) != 2 {
		t.Errorf("flat AllComponents = %d, want 2", len(got))
	}
	staged := &Scenario{Stages: []Stage{
		{Name: "s1", Components: []Component{{Name: "a"}}},
		{Name: "s2", Components: []Component{{Name: "b"}, {Name: "c"}}},
	}}
	if got := staged.AllComponents(); len(got) != 3 {
		t.Errorf("staged AllComponents = %d, want 3", len(got))
	}
}
