// SPDX-License-Identifier: Apache-2.0
package executor

import (
	"bytes"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestNew(t *testing.T) {
	exec := New("/tmp/test")
	if exec.ProjectRoot != "/tmp/test" {
		t.Errorf("ProjectRoot: got %q, want %q", exec.ProjectRoot, "/tmp/test")
	}
	if exec.Env == nil {
		t.Error("Env map should be initialized")
	}
	if exec.Stdout != os.Stdout {
		t.Error("Stdout should default to os.Stdout")
	}
	if exec.Stderr != os.Stderr {
		t.Error("Stderr should default to os.Stderr")
	}
}

func TestSetEnv(t *testing.T) {
	exec := New("/tmp/test")
	exec.SetEnv("FOO", "bar")
	exec.SetEnv("BAZ", "qux")

	if exec.Env["FOO"] != "bar" {
		t.Errorf("Env[FOO]: got %q, want %q", exec.Env["FOO"], "bar")
	}
	if exec.Env["BAZ"] != "qux" {
		t.Errorf("Env[BAZ]: got %q, want %q", exec.Env["BAZ"], "qux")
	}
}

func TestSetEnv_Overwrite(t *testing.T) {
	exec := New("/tmp/test")
	exec.SetEnv("KEY", "first")
	exec.SetEnv("KEY", "second")

	if exec.Env["KEY"] != "second" {
		t.Errorf("Env[KEY]: got %q, want %q", exec.Env["KEY"], "second")
	}
}

// TestSetEnv_ConcurrentWithBuildEnv models the HTTP server, where one handler
// calls SetEnv (e.g. traffic tunables) while other handlers run commands and
// snapshot the environment via buildEnv. Before Env was guarded by a mutex this
// tripped "concurrent map read and map write"; run with -race to guard against
// regressions.
func TestSetEnv_ConcurrentWithBuildEnv(t *testing.T) {
	t.Parallel()
	e := New(t.TempDir())
	e.Stdout = io.Discard
	e.Stderr = io.Discard

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			e.SetEnv("TRAFFIC_RPS", "10")
		}()
		go func() {
			defer wg.Done()
			_ = e.RunCommand("true") // RunCommand -> buildEnv reads e.Env
		}()
	}
	wg.Wait()
}

func TestRunCommand_Echo(t *testing.T) {
	var buf bytes.Buffer
	exec := New(t.TempDir())
	exec.Stdout = &buf
	exec.Stderr = &buf

	err := exec.RunCommand("echo", "hello")
	if err != nil {
		t.Fatalf("RunCommand echo: %v", err)
	}

	output := buf.String()
	if output != "hello\n" {
		t.Errorf("output: got %q, want %q", output, "hello\n")
	}
}

func TestCaptureOutput(t *testing.T) {
	exec := New(t.TempDir())

	out, err := exec.CaptureOutput("echo", "captured")
	if err != nil {
		t.Fatalf("CaptureOutput echo: %v", err)
	}

	if out != "captured" {
		t.Errorf("output: got %q, want %q", out, "captured")
	}
}

func TestRunScript(t *testing.T) {
	root := t.TempDir()
	script := "test-script.sh"
	os.WriteFile(root+"/"+script, []byte("#!/bin/bash\necho script-ran"), 0755)

	var buf bytes.Buffer
	exec := New(root)
	exec.Stdout = &buf

	err := exec.RunScript(script)
	if err != nil {
		t.Fatalf("RunScript: %v", err)
	}

	if buf.String() != "script-ran\n" {
		t.Errorf("output: got %q, want %q", buf.String(), "script-ran\n")
	}
}

func TestRunScript_NotFound(t *testing.T) {
	exec := New(t.TempDir())
	err := exec.RunScript("nonexistent.sh")
	if err == nil {
		t.Error("expected error for missing script")
	}
}

func TestRunCommand_Failure(t *testing.T) {
	exec := New(t.TempDir())
	exec.Stdout = &bytes.Buffer{}
	exec.Stderr = &bytes.Buffer{}

	err := exec.RunCommand("false")
	if err == nil {
		t.Error("expected error for failed command")
	}
}

func TestBuildEnv(t *testing.T) {
	exec := New(t.TempDir())
	exec.SetEnv("CUSTOM_VAR", "custom_value")

	env := exec.buildEnv()

	found := false
	for _, e := range env {
		if e == "CUSTOM_VAR=custom_value" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected CUSTOM_VAR=custom_value in build environment")
	}
}

// ---------------------------------------------------------------------------
// Streaming / job-ID tests
// ---------------------------------------------------------------------------

func TestRunScriptStreamed_ReturnsActionID(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(root+"/ok.sh", []byte("#!/bin/bash\necho hello"), 0755)

	e := New(root)
	e.Stdout = &bytes.Buffer{}
	e.Stderr = &bytes.Buffer{}

	id, err := e.RunScriptStreamed("Test", "ok.sh")
	if err != nil {
		t.Fatalf("RunScriptStreamed: %v", err)
	}
	if id == "" {
		t.Error("expected non-empty action ID")
	}
}

func TestRunScriptStreamed_NotFound_EmitsEvents(t *testing.T) {
	e := New(t.TempDir())

	var received []ActionEvent
	ch := e.Broadcast.Subscribe()
	done := make(chan struct{})
	go func() {
		for ev := range ch {
			received = append(received, ev)
		}
		close(done)
	}()

	id, err := e.RunScriptStreamed("Missing script", "no-such.sh")
	if err == nil {
		t.Fatal("expected error for missing script")
	}
	if id == "" {
		t.Error("expected non-empty action ID even on failure")
	}

	e.Broadcast.Unsubscribe(ch)
	<-done

	types := make(map[string]bool)
	for _, ev := range received {
		types[ev.Type] = true
		if ev.ID != id {
			t.Errorf("event ID mismatch: got %q, want %q", ev.ID, id)
		}
	}
	if !types["action_start"] {
		t.Error("expected action_start event")
	}
	if !types["action_end"] {
		t.Error("expected action_end event")
	}
}

func TestRunScriptStreamed_SuccessPath_EmitsEvents(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(root+"/greet.sh", []byte("#!/bin/bash\necho hi"), 0755)

	e := New(root)
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

	id, err := e.RunScriptStreamed("Greet", "greet.sh")
	if err != nil {
		t.Fatalf("RunScriptStreamed: %v", err)
	}

	e.Broadcast.Unsubscribe(ch)
	<-done

	types := make(map[string]bool)
	for _, ev := range received {
		if ev.ID != id {
			t.Errorf("event ID mismatch: got %q, want %q", ev.ID, id)
		}
		types[ev.Type] = true
	}
	if !types["action_start"] {
		t.Error("expected action_start event")
	}
	if !types["action_output"] {
		t.Error("expected action_output event")
	}
	if !types["action_end"] {
		t.Error("expected action_end event")
	}
}

// TestStreamOutput_LongLineNotTruncated guards against the bufio.Scanner
// regression: a single line larger than the old 1 MiB cap used to silently drop
// that line and everything after it from both the writer and the broadcast
// stream. A sentinel line printed *after* the huge line must survive.
func TestStreamOutput_LongLineNotTruncated(t *testing.T) {
	const longLen = 2 * 1024 * 1024 // 2 MiB, well past the old 1 MiB cap

	var buf bytes.Buffer
	e := New(t.TempDir())
	e.Stdout = &buf
	e.Stderr = &buf

	var outputs []string
	ch := e.Broadcast.Subscribe()
	done := make(chan struct{})
	go func() {
		for ev := range ch {
			if ev.Type == "action_output" {
				outputs = append(outputs, ev.Output)
			}
		}
		close(done)
	}()

	// The shell generates the 2 MiB line itself (passing it as an argv entry
	// would blow ARG_MAX): fill it from /dev/zero, then print a sentinel line
	// *after* it that must survive the long line.
	script := "head -c 2097152 /dev/zero | tr '\\0' 'x'; printf '\\nSENTINEL_END\\n'"
	_, err := e.RunCommandStreamed("t", "bash", "-c", script)
	if err != nil {
		t.Fatalf("RunCommandStreamed: %v", err)
	}
	e.Broadcast.Unsubscribe(ch)
	<-done

	if !strings.Contains(buf.String(), "SENTINEL_END") {
		t.Errorf("writer lost output after a >1MiB line (got %d bytes, no sentinel)", buf.Len())
	}
	var sawLong, sawSentinel bool
	for _, o := range outputs {
		if len(o) == longLen {
			sawLong = true
		}
		if o == "SENTINEL_END" {
			sawSentinel = true
		}
	}
	if !sawLong {
		t.Error("broadcast stream did not carry the full 2MiB line")
	}
	if !sawSentinel {
		t.Error("broadcast stream lost the sentinel line after the long line")
	}
}

func TestRunScriptStreamedWith_UsesProvidedID(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(root+"/noop.sh", []byte("#!/bin/bash\nexit 0"), 0755)

	e := New(root)
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

	wantID := "custom-job-42"
	err := e.RunScriptStreamedWith(wantID, "Noop", "noop.sh")
	if err != nil {
		t.Fatalf("RunScriptStreamedWith: %v", err)
	}

	e.Broadcast.Unsubscribe(ch)
	<-done

	for _, ev := range received {
		if ev.ID != wantID {
			t.Errorf("event ID: got %q, want %q", ev.ID, wantID)
		}
	}
}

func TestNextActionID_Unique(t *testing.T) {
	e := New(t.TempDir())
	ids := make(map[string]bool)
	for i := 0; i < 10; i++ {
		id := e.NextActionID()
		if ids[id] {
			t.Errorf("duplicate action ID: %q", id)
		}
		ids[id] = true
	}
}
