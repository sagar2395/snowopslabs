// SPDX-License-Identifier: Apache-2.0
package executor

import (
	"bytes"
	"strings"
	"testing"
)

// TestRunScript_NonExistentScript verifies that RunScript returns an error
// containing "script not found" when the script does not exist.
func TestRunScript_NonExistentScript(t *testing.T) {
	e := New(t.TempDir())
	err := e.RunScript("definitely-does-not-exist.sh")
	if err == nil {
		t.Fatal("expected error for non-existent script, got nil")
	}
	if !strings.Contains(err.Error(), "script not found") {
		t.Errorf("error should mention 'script not found': %v", err)
	}
}

// TestRunScriptStreamed_NonExistentScript verifies that RunScriptStreamed
// returns an error and a non-empty action ID when the script does not exist.
func TestRunScriptStreamed_NonExistentScript(t *testing.T) {
	e := New(t.TempDir())
	e.Stdout = &bytes.Buffer{}
	e.Stderr = &bytes.Buffer{}

	id, err := e.RunScriptStreamed("test-label", "no-such-script.sh")
	if err == nil {
		t.Fatal("expected error for non-existent script, got nil")
	}
	if !strings.Contains(err.Error(), "script not found") {
		t.Errorf("error should mention 'script not found': %v", err)
	}
	if id == "" {
		t.Error("expected non-empty action ID even on failure")
	}
}

// TestRunScriptStreamedWith_NonExistentScript verifies that
// RunScriptStreamedWith broadcasts error events and returns an error.
func TestRunScriptStreamedWith_NonExistentScript(t *testing.T) {
	e := New(t.TempDir())
	e.Stdout = &bytes.Buffer{}
	e.Stderr = &bytes.Buffer{}

	var received []ActionEvent
	ch := e.Broadcast.Subscribe()
	done := make(chan struct{})
	go func() {
		for ev := range ch {
			received = append(received, ev)
		}
		close(done)
	}()

	customID := "pre-alloc-id-42"
	err := e.RunScriptStreamedWith(customID, "test-label", "no-such-script.sh")
	if err == nil {
		t.Fatal("expected error for non-existent script, got nil")
	}
	if !strings.Contains(err.Error(), "script not found") {
		t.Errorf("error should mention 'script not found': %v", err)
	}

	e.Broadcast.Unsubscribe(ch)
	<-done

	// Should have received at least start and end events with the custom ID.
	if len(received) == 0 {
		t.Fatal("expected broadcast events, got none")
	}
	for _, ev := range received {
		if ev.ID != customID {
			t.Errorf("event ID: got %q, want %q", ev.ID, customID)
		}
	}
	types := map[string]bool{}
	for _, ev := range received {
		types[ev.Type] = true
	}
	if !types["action_start"] {
		t.Error("expected action_start event")
	}
	if !types["action_end"] {
		t.Error("expected action_end event")
	}
}

// TestNextActionID_Monotonic verifies that NextActionID generates strictly
// increasing numeric IDs.
func TestNextActionID_Monotonic(t *testing.T) {
	e := New(t.TempDir())

	ids := make([]string, 5)
	for i := range ids {
		ids[i] = e.NextActionID()
	}

	// All IDs must be unique.
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Errorf("duplicate action ID: %q", id)
		}
		seen[id] = true
	}

	// Each ID must start with "action-".
	for _, id := range ids {
		if !strings.HasPrefix(id, "action-") {
			t.Errorf("action ID %q does not start with 'action-'", id)
		}
	}
}
