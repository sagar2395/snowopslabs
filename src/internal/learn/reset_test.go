// SPDX-License-Identifier: Apache-2.0
package learn

import (
	"os"
	"testing"
)

func TestResetProgress(t *testing.T) {
	e, learnDir := makeEngine(t)
	writePathYAML(t, learnDir, "test-path", validPathYAML)

	// Start and complete a module, then reset.
	prog, err := e.StartPath("test-path")
	if err != nil {
		t.Fatalf("StartPath: %v", err)
	}
	if err := e.MarkComplete(prog, 0); err != nil {
		t.Fatalf("MarkComplete: %v", err)
	}
	if _, err := os.Stat(e.progressFile("test-path")); err != nil {
		t.Fatalf("progress file should exist after start: %v", err)
	}

	if err := e.ResetProgress("test-path"); err != nil {
		t.Fatalf("ResetProgress: %v", err)
	}
	// Progress must read as not-started again.
	got, err := e.Progress("test-path")
	if err != nil {
		t.Fatalf("Progress after reset: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil (not-started) progress after reset, got %+v", got)
	}
}

func TestResetProgress_NeverStarted_NoError(t *testing.T) {
	e, learnDir := makeEngine(t)
	writePathYAML(t, learnDir, "test-path", validPathYAML)
	if err := e.ResetProgress("test-path"); err != nil {
		t.Errorf("resetting a never-started path should be a no-op, got %v", err)
	}
}

func TestResetProgress_UnknownPath_Errors(t *testing.T) {
	e, _ := makeEngine(t)
	if err := e.ResetProgress("does-not-exist"); err == nil {
		t.Error("expected an error resetting an unknown path")
	}
}
