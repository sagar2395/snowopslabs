// SPDX-License-Identifier: Apache-2.0
package scenario

import (
	"testing"
)

func TestDeactivateAll_ClearsMarkers(t *testing.T) {
	e := NewEngine(t.TempDir(), "k3d.local", "k3d")

	for _, n := range []string{"autoscaling-under-load", "observability-sre"} {
		if err := e.markActive(n); err != nil {
			t.Fatalf("markActive(%s): %v", n, err)
		}
	}
	if !e.isActive("autoscaling-under-load") || !e.isActive("observability-sre") {
		t.Fatal("precondition: both scenarios should be active")
	}

	cleared := e.DeactivateAll()
	if len(cleared) != 2 {
		t.Errorf("DeactivateAll cleared %d markers, want 2 (%v)", len(cleared), cleared)
	}
	if e.isActive("autoscaling-under-load") || e.isActive("observability-sre") {
		t.Error("scenarios should read as inactive after DeactivateAll")
	}
}

func TestDeactivateAll_NoMarkers_NoError(t *testing.T) {
	e := NewEngine(t.TempDir(), "k3d.local", "k3d")
	if cleared := e.DeactivateAll(); len(cleared) != 0 {
		t.Errorf("expected nothing cleared on a fresh engine, got %v", cleared)
	}
}
