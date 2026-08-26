// SPDX-License-Identifier: Apache-2.0
package incident

import (
	"errors"
	"strings"
	"testing"

	"github.com/sagar2395/snowopslabs/internal/executor"
)

// TestActive_ReturnsNilWhenNoActive verifies that Active() returns (nil, nil)
// when no active incident file exists.
func TestActive_ReturnsNilWhenNoActive(t *testing.T) {
	e, _ := testEngine(t) // no faults injected, empty incidents/
	active, err := e.Active()
	if err != nil {
		t.Fatalf("Active() error: %v", err)
	}
	if active != nil {
		t.Fatalf("expected nil active, got %+v", active)
	}
}

// TestGet_UnknownFaultReturnsError verifies that Get() for a missing fault
// returns a descriptive error.
func TestGet_UnknownFaultReturnsError(t *testing.T) {
	e, _ := testEngine(t, "fault-a")
	_, err := e.Get("no-such-fault")
	if err == nil {
		t.Fatal("expected error for unknown fault, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention 'not found': %v", err)
	}
}

// TestInject_FaultNotFound verifies that injecting a non-existent fault
// returns an error.
func TestInject_FaultNotFound(t *testing.T) {
	e, root := testEngine(t)
	ex := executor.New(root)

	_, err := e.Inject("ghost-fault", ex, false, false)
	if err == nil {
		t.Fatal("expected error injecting unknown fault, got nil")
	}
}

// TestInject_ErrIncidentActive verifies that a second inject without --force
// returns ErrIncidentActive.
func TestInject_ErrIncidentActive(t *testing.T) {
	e, root := testEngine(t, "fault-a", "fault-b")
	ex := executor.New(root)

	if _, err := e.Inject("fault-a", ex, false, false); err != nil {
		t.Fatalf("first inject: %v", err)
	}

	_, err := e.Inject("fault-b", ex, false, false)
	if err == nil {
		t.Fatal("expected ErrIncidentActive on second inject, got nil")
	}
	if !errors.Is(err, ErrIncidentActive) {
		t.Errorf("expected errors.Is(err, ErrIncidentActive), got: %v", err)
	}
}

// TestPickRandom_NoEligibleFaultsEdge verifies that PickRandom returns an error
// when there are no eligible faults (prerequisite apps not present).
func TestPickRandom_NoEligibleFaultsEdge(t *testing.T) {
	// Use a root with a fault but without the required apps/ directory,
	// so the preflight check fails and the fault is not eligible.
	root := t.TempDir()
	writeFault(t, root, "fault-a")
	e := NewEngine(root, "k3d.local")

	_, err := e.PickRandom(0, "")
	if err == nil {
		t.Fatal("expected error when no faults are eligible, got nil")
	}
	if !strings.Contains(err.Error(), "eligible") {
		t.Errorf("error should mention 'eligible': %v", err)
	}
}

// TestPickRandom_InvalidCategory verifies that PickRandom with a category that
// no fault belongs to returns an error.
func TestPickRandom_InvalidCategory(t *testing.T) {
	e, _ := testEngine(t, "fault-a", "fault-b")
	// "network" is a valid category string but none of the test faults use it.
	_, err := e.PickRandom(42, "network")
	if err == nil {
		t.Fatal("expected error for category with no eligible faults, got nil")
	}
	if !strings.Contains(err.Error(), "network") {
		t.Errorf("error should mention category name: %v", err)
	}
}
