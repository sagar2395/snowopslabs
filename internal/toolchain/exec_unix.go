// SPDX-License-Identifier: Apache-2.0

//go:build unix

package toolchain

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// DefaultGracePeriod is how long a cancelled process gets to exit on SIGTERM
// before SIGKILL. Helm needs a moment to release its release lock; beyond a few
// seconds it is not going to.
const DefaultGracePeriod = 15 * time.Second

// Exec is the production Runner. It executes each command in its own process
// group so that cancellation reaches the whole tree.
//
// This is the fix for v1's central defect: exec.Command(...).Run() with no
// context meant a twenty-minute helm install could not be aborted, and killing
// labctl orphaned every child it had spawned.
type Exec struct {
	// GracePeriod overrides DefaultGracePeriod.
	GracePeriod time.Duration
	// lookPath is injectable so tests can exercise resolution failures.
	lookPath func(string) (string, error)
}

// NewExec returns a production Runner.
func NewExec() *Exec {
	return &Exec{GracePeriod: DefaultGracePeriod, lookPath: exec.LookPath}
}

// LookPath resolves a binary on PATH.
func (e *Exec) LookPath(name string) (string, error) {
	lp := e.lookPath
	if lp == nil {
		lp = exec.LookPath
	}
	return lp(name)
}

func (e *Exec) grace() time.Duration {
	if e.GracePeriod > 0 {
		return e.GracePeriod
	}
	return DefaultGracePeriod
}

// Run executes cmd, returning when the process exits.
//
// Cancellation sequence (ADR-0003):
//  1. SIGTERM to the process group, so helm's children are included.
//  2. Wait up to the grace period.
//  3. SIGKILL to the group.
//
// A context already cancelled on entry means nothing is started at all.
func (e *Exec) Run(ctx context.Context, cmd Command) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if cmd.Path == "" {
		return Result{}, errors.New("toolchain: command path is required")
	}

	// Deliberately not exec.CommandContext: its cancellation kills only the
	// direct child, leaving helm's descendants running. We manage the signal
	// sequence ourselves against the process group.
	//nolint:gosec // G204: running scripts is this tool's purpose; the path is
	// containment-checked by Resolver and args are an argv array, never a shell string.
	c := exec.Command(cmd.Path, cmd.Args...)
	c.Dir = cmd.Dir
	c.Stdout = cmd.Stdout
	c.Stderr = cmd.Stderr
	c.Env = append(os.Environ(), cmd.EnvSlice()...)

	// Setpgid puts the child in a new process group whose ID is its PID, so
	// signalling -PID reaches every descendant.
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := c.Start(); err != nil {
		return Result{}, fmt.Errorf("starting %s: %w", cmd.String(), err)
	}

	pgid := c.Process.Pid

	// waitDone carries the result of Wait so the killer goroutine can stop.
	waitDone := make(chan error, 1)
	go func() { waitDone <- c.Wait() }()

	var (
		killOnce  sync.Once
		signalled bool
		mu        sync.Mutex
	)
	stopKiller := make(chan struct{})

	go func() {
		select {
		case <-ctx.Done():
			killOnce.Do(func() {
				mu.Lock()
				signalled = true
				mu.Unlock()

				terminateGroup(pgid, syscall.SIGTERM)
				select {
				case <-time.After(e.grace()):
					terminateGroup(pgid, syscall.SIGKILL)
				case <-stopKiller:
					// Exited within the grace period; no SIGKILL needed.
				}
			})
		case <-stopKiller:
		}
	}()

	waitErr := <-waitDone
	close(stopKiller)

	mu.Lock()
	wasSignalled := signalled
	mu.Unlock()

	res := Result{Signalled: wasSignalled}

	// A cancelled context wins over the exit status: a process killed by our
	// own SIGTERM reports a non-zero exit, but the *reason* is the cancellation
	// and callers must be able to tell that apart from a real failure.
	if ctxErr := ctx.Err(); ctxErr != nil && wasSignalled {
		if c.ProcessState != nil {
			res.ExitCode = c.ProcessState.ExitCode()
		}
		return res, ctxErr
	}

	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
			return res, &ExitError{Command: cmd.String(), ExitCode: res.ExitCode}
		}
		return res, fmt.Errorf("running %s: %w", cmd.String(), waitErr)
	}

	return res, nil
}

// terminateGroup signals a whole process group, falling back to the single
// process if the group has already gone. Errors are ignored on purpose: the
// only failure mode that matters here is "already dead", which is the outcome
// we wanted anyway.
func terminateGroup(pgid int, sig syscall.Signal) {
	if err := syscall.Kill(-pgid, sig); err != nil {
		_ = syscall.Kill(pgid, sig)
	}
}
