// SPDX-License-Identifier: Apache-2.0
package executor

import (
	"strings"
	"testing"
)

func TestCaptureOutputPaths(t *testing.T) {
	e := New(t.TempDir())

	// Success: stdout is captured and trimmed.
	out, err := e.CaptureOutput("printf", "hello")
	if err != nil {
		t.Fatalf("CaptureOutput: %v", err)
	}
	if out != "hello" {
		t.Errorf("out = %q, want %q", out, "hello")
	}

	// Failure with stderr: the ExitError branch wraps the underlying error and
	// appends stderr, so errors.Is still sees the original exec error.
	_, err = e.CaptureOutput("sh", "-c", "echo boom >&2; exit 3")
	if err == nil {
		t.Fatal("expected an error for a non-zero exit")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error %q should include stderr", err.Error())
	}

	// Failure with no such binary: the non-ExitError branch returns the error as-is.
	if _, err := e.CaptureOutput("labctl-no-such-binary-xyz"); err == nil {
		t.Fatal("expected an error for a missing binary")
	}
}

func TestBroadcastStartEnd(t *testing.T) {
	e := New(t.TempDir())
	ch := e.Broadcast.Subscribe()
	defer e.Broadcast.Unsubscribe(ch)

	e.BroadcastStart("action-1", "Install ingress")
	if ev := <-ch; ev.Type != "action_start" || ev.ID != "action-1" {
		t.Errorf("start event = %+v", ev)
	}

	e.BroadcastEnd("action-1", "Install ingress", nil)
	ev := <-ch
	if ev.Type != "action_end" || ev.ExitCode == nil || *ev.ExitCode != 0 {
		t.Errorf("success end event = %+v", ev)
	}

	e.BroadcastEnd("action-2", "Install mesh", errBoom{})
	ev = <-ch
	if ev.ExitCode == nil || *ev.ExitCode != 1 || ev.Error == "" {
		t.Errorf("failure end event = %+v", ev)
	}
}

type errBoom struct{}

func (errBoom) Error() string { return "boom" }
