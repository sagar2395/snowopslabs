// SPDX-License-Identifier: Apache-2.0

//go:build unix

package toolchain

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// TestHelperProcess is not a real test: it is a controlled child process that
// the cancellation tests re-invoke via os.Args[0]. When asked, it installs an
// ignored SIGTERM disposition with a real sigaction, announces readiness, then
// blocks — a deterministic stand-in for a long-running tool that traps SIGTERM.
// Without the env marker it is an immediate no-op, so a normal suite run of it
// costs nothing.
func TestHelperProcess(t *testing.T) {
	switch os.Getenv("SNOWOPS_HELPER") {
	case "ignore-sigterm":
		signal.Ignore(syscall.SIGTERM)
		// The parent waits for this line before cancelling, so SIGTERM cannot
		// arrive before the ignore is established.
		fmt.Fprintln(os.Stdout, "ready")
		time.Sleep(60 * time.Second)
	default:
		// Not invoked as a helper; do nothing.
	}
}

// closeOnFirstWrite closes ch the first time it is written to. The cancellation
// test uses it to learn, from the helper's own stdout, that the helper has
// installed its SIGTERM ignore.
type closeOnFirstWrite struct {
	once sync.Once
	ch   chan struct{}
}

func (c *closeOnFirstWrite) Write(p []byte) (int, error) {
	c.once.Do(func() { close(c.ch) })
	return len(p), nil
}

// writeScript creates an executable script and returns its path.
func writeScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "script.sh")
	if err := os.WriteFile(path, []byte("#!/usr/bin/env bash\n"+body), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func TestExecRun(t *testing.T) {
	ctx := context.Background()
	e := NewExec()

	t.Run("captures stdout and stderr separately", func(t *testing.T) {
		script := writeScript(t, "echo to-stdout\necho to-stderr >&2\n")
		var stdout, stderr bytes.Buffer

		res, err := e.Run(ctx, Command{Path: script, Stdout: &stdout, Stderr: &stderr})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if res.ExitCode != 0 {
			t.Errorf("exit code = %d, want 0", res.ExitCode)
		}
		if got := strings.TrimSpace(stdout.String()); got != "to-stdout" {
			t.Errorf("stdout = %q", got)
		}
		if got := strings.TrimSpace(stderr.String()); got != "to-stderr" {
			t.Errorf("stderr = %q", got)
		}
	})

	t.Run("passes arguments as argv, not through a shell", func(t *testing.T) {
		// The injection guard. An argument full of shell metacharacters must
		// arrive as one literal argument. The marker file proves nothing was
		// evaluated: if the argument were interpolated into a shell string,
		// the command substitution would run and create it.
		dir := t.TempDir()
		marker := filepath.Join(dir, "pwned")
		script := writeScript(t, `printf '%s\n' "$@"`)

		var stdout bytes.Buffer
		nasty := `; touch ` + marker + `; $(touch ` + marker + `)`

		if _, err := e.Run(ctx, Command{
			Path:   script,
			Args:   []string{nasty, "second"},
			Stdout: &stdout,
		}); err != nil {
			t.Fatalf("Run: %v", err)
		}

		lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
		if len(lines) != 2 {
			t.Fatalf("got %d arguments, want 2: %q", len(lines), lines)
		}
		if lines[0] != nasty {
			t.Errorf("argument was reinterpreted: got %q, want %q", lines[0], nasty)
		}
		if _, err := os.Stat(marker); err == nil {
			t.Error("shell metacharacters were evaluated — the argument reached a shell")
		}
	})

	t.Run("passes environment to the script", func(t *testing.T) {
		script := writeScript(t, `echo "${DOMAIN_SUFFIX:-unset}"`)
		var stdout bytes.Buffer
		if _, err := e.Run(ctx, Command{
			Path:   script,
			Env:    map[string]string{"DOMAIN_SUFFIX": "test.local"},
			Stdout: &stdout,
		}); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if got := strings.TrimSpace(stdout.String()); got != "test.local" {
			t.Errorf("DOMAIN_SUFFIX = %q, want test.local", got)
		}
	})

	t.Run("inherits the parent environment as well", func(t *testing.T) {
		t.Setenv("SNOWOPS_TEST_INHERITED", "yes")
		script := writeScript(t, `echo "${SNOWOPS_TEST_INHERITED:-unset}"`)
		var stdout bytes.Buffer
		if _, err := e.Run(ctx, Command{Path: script, Stdout: &stdout}); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if got := strings.TrimSpace(stdout.String()); got != "yes" {
			t.Errorf("inherited variable = %q, want yes", got)
		}
	})

	t.Run("runs in the requested directory", func(t *testing.T) {
		dir := t.TempDir()
		script := writeScript(t, "pwd")
		var stdout bytes.Buffer
		if _, err := e.Run(ctx, Command{Path: script, Dir: dir, Stdout: &stdout}); err != nil {
			t.Fatalf("Run: %v", err)
		}
		got, _ := filepath.EvalSymlinks(strings.TrimSpace(stdout.String()))
		want, _ := filepath.EvalSymlinks(dir)
		if got != want {
			t.Errorf("pwd = %q, want %q", got, want)
		}
	})

	t.Run("reports a non-zero exit as an ExitError", func(t *testing.T) {
		for _, code := range []int{1, 2, 42, 127} {
			t.Run(strconv.Itoa(code), func(t *testing.T) {
				script := writeScript(t, fmt.Sprintf("exit %d\n", code))
				res, err := e.Run(ctx, Command{Path: script})

				var exitErr *ExitError
				if !errors.As(err, &exitErr) {
					t.Fatalf("error = %v, want *ExitError", err)
				}
				if exitErr.ExitCode != code || res.ExitCode != code {
					t.Errorf("exit code = %d/%d, want %d", exitErr.ExitCode, res.ExitCode, code)
				}
			})
		}
	})

	t.Run("nil writers discard output without erroring", func(t *testing.T) {
		script := writeScript(t, "echo noisy\necho also-noisy >&2\n")
		if _, err := e.Run(ctx, Command{Path: script}); err != nil {
			t.Fatalf("Run: %v", err)
		}
	})

	t.Run("rejects an empty path", func(t *testing.T) {
		if _, err := e.Run(ctx, Command{}); err == nil {
			t.Fatal("expected an error for an empty command path")
		}
	})

	t.Run("reports a missing binary", func(t *testing.T) {
		_, err := e.Run(ctx, Command{Path: filepath.Join(t.TempDir(), "does-not-exist")})
		if err == nil {
			t.Fatal("expected an error for a missing binary")
		}
		var exitErr *ExitError
		if errors.As(err, &exitErr) {
			t.Error("a missing binary is a start failure, not an exit code")
		}
	})

	t.Run("does not start when the context is already cancelled", func(t *testing.T) {
		marker := filepath.Join(t.TempDir(), "ran")
		script := writeScript(t, "touch "+marker+"\n")

		cancelled, cancel := context.WithCancel(ctx)
		cancel()

		if _, err := e.Run(cancelled, Command{Path: script}); !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
		if _, err := os.Stat(marker); err == nil {
			t.Error("the script ran despite an already-cancelled context")
		}
	})
}

// TestExecCancellation covers the behaviour ADR-0003 exists for: a cancelled
// run must die promptly, and must take its children with it.
func TestExecCancellation(t *testing.T) {
	t.Run("cancelling terminates the process promptly", func(t *testing.T) {
		e := NewExec()
		script := writeScript(t, "sleep 60\n")

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			_, err := e.Run(ctx, Command{Path: script})
			done <- err
		}()

		time.Sleep(150 * time.Millisecond) // let it get going
		start := time.Now()
		cancel()

		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("error = %v, want context.Canceled", err)
			}
			if elapsed := time.Since(start); elapsed > 5*time.Second {
				t.Errorf("took %v to die; cancellation should be prompt", elapsed)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("Run did not return after cancellation")
		}
	})

	t.Run("marks the result as signalled", func(t *testing.T) {
		// Callers must be able to tell a cancellation from a real failure.
		e := NewExec()
		script := writeScript(t, "sleep 60\n")

		ctx, cancel := context.WithCancel(context.Background())
		type outcome struct {
			res Result
			err error
		}
		done := make(chan outcome, 1)
		go func() {
			res, err := e.Run(ctx, Command{Path: script})
			done <- outcome{res, err}
		}()

		time.Sleep(150 * time.Millisecond)
		cancel()

		select {
		case got := <-done:
			if !got.res.Signalled {
				t.Error("Signalled = false; a cancelled run must be marked as signalled")
			}
			var exitErr *ExitError
			if errors.As(got.err, &exitErr) {
				t.Error("a cancellation must not be reported as an ordinary ExitError")
			}
		case <-time.After(10 * time.Second):
			t.Fatal("Run did not return")
		}
	})

	// This is the test behind R01: v1 left orphaned helm processes behind
	// because it killed only the direct child.
	t.Run("kills the whole process group, not just the direct child", func(t *testing.T) {
		e := NewExec()
		dir := t.TempDir()
		childPIDFile := filepath.Join(dir, "child.pid")

		// The script spawns a grandchild — as helm does — and records its PID.
		script := writeScript(t, fmt.Sprintf(`
sleep 60 &
echo $! > %s
wait
`, childPIDFile))

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			_, _ = e.Run(ctx, Command{Path: script})
			close(done)
		}()

		// Wait for the grandchild's PID to appear.
		var childPID int
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			data, err := os.ReadFile(childPIDFile)
			if err == nil {
				if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
					childPID = pid
					break
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
		if childPID == 0 {
			t.Skip("could not observe the grandchild PID in time")
		}

		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("Run did not return after cancellation")
		}

		// Give the signal a moment to land, then confirm the grandchild is gone.
		gone := false
		deadline = time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if err := syscall.Kill(childPID, 0); err != nil {
				gone = true
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if !gone {
			_ = syscall.Kill(childPID, syscall.SIGKILL) // don't leak it from the test
			t.Errorf("grandchild %d survived cancellation — the process group was not signalled", childPID)
		}
	})

	t.Run("SIGKILLs a process that ignores SIGTERM", func(t *testing.T) {
		// A process that ignores SIGTERM must still die: grace period, then kill.
		//
		// The fixture re-invokes the test binary as a helper (TestHelperProcess)
		// rather than running a shell script. macOS ships bash 3.2, whose
		// `trap '' TERM` does not reliably establish an ignored disposition —
		// not for the shell itself, and not for a foreground child across exec —
		// so a bash-based fixture is genuinely flaky there. A Go helper installs
		// SIG_IGN with a real sigaction and blocks, which is deterministic on
		// every platform. It prints a line once the ignore is in place so the
		// test can cancel only after SIGTERM is guaranteed to be swallowed.
		e := &Exec{GracePeriod: 300 * time.Millisecond}

		ready := &closeOnFirstWrite{ch: make(chan struct{})}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			_, err := e.Run(ctx, Command{
				Path:   os.Args[0],
				Args:   []string{"-test.run=^TestHelperProcess$"},
				Env:    map[string]string{"SNOWOPS_HELPER": "ignore-sigterm"},
				Stdout: ready,
			})
			done <- err
		}()

		select {
		case <-ready.ch:
		case <-time.After(5 * time.Second):
			t.Fatal("helper never reported that it had ignored SIGTERM")
		}
		start := time.Now()
		cancel()

		select {
		case <-done:
			if elapsed := time.Since(start); elapsed < 250*time.Millisecond {
				t.Errorf("died in %v — the grace period was not honoured", elapsed)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("a SIGTERM-ignoring process was never killed")
		}
	})

	t.Run("a deadline behaves like a cancellation", func(t *testing.T) {
		e := NewExec()
		script := writeScript(t, "sleep 60\n")

		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()

		res, err := e.Run(ctx, Command{Path: script})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error = %v, want context.DeadlineExceeded", err)
		}
		if !res.Signalled {
			t.Error("Signalled = false; a timed-out run must be marked as signalled")
		}
	})

	t.Run("a script finishing inside the grace period is not killed", func(t *testing.T) {
		// Cancellation must not corrupt the outcome of a run that was already
		// finishing on its own.
		e := &Exec{GracePeriod: 5 * time.Second}
		script := writeScript(t, "sleep 0.1\n")

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		start := time.Now()
		if _, err := e.Run(ctx, Command{Path: script}); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if elapsed := time.Since(start); elapsed > 3*time.Second {
			t.Errorf("took %v; a completed run must not wait out the grace period", elapsed)
		}
	})
}

func TestExecLookPath(t *testing.T) {
	e := NewExec()

	t.Run("finds a real binary", func(t *testing.T) {
		want, err := exec.LookPath("sh")
		if err != nil {
			t.Skip("sh not on PATH")
		}
		got, err := e.LookPath("sh")
		if err != nil {
			t.Fatalf("LookPath: %v", err)
		}
		if got != want {
			t.Errorf("LookPath = %q, want %q", got, want)
		}
	})

	t.Run("fails for a missing binary", func(t *testing.T) {
		if _, err := e.LookPath("snowops-definitely-not-a-real-binary"); err == nil {
			t.Fatal("expected an error for a missing binary")
		}
	})
}

func TestExecGracePeriodDefault(t *testing.T) {
	tests := []struct {
		name string
		exec *Exec
		want time.Duration
	}{
		{"zero value falls back to the default", &Exec{}, DefaultGracePeriod},
		{"negative falls back to the default", &Exec{GracePeriod: -time.Second}, DefaultGracePeriod},
		{"explicit value is honoured", &Exec{GracePeriod: 2 * time.Second}, 2 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.exec.grace(); got != tt.want {
				t.Errorf("grace() = %v, want %v", got, tt.want)
			}
		})
	}
}
