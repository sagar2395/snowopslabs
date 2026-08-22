// SPDX-License-Identifier: Apache-2.0

package run

import (
	"context"
	"errors"
	"time"

	"github.com/sagar2395/snowopslabs/internal/store"
	"github.com/sagar2395/snowopslabs/internal/toolchain"
)

// worker pulls run IDs off the queue and executes them until shutdown.
func (e *Engine) worker() {
	defer e.wg.Done()
	for {
		select {
		case <-e.shutdown:
			return
		case id, ok := <-e.queue:
			if !ok {
				return
			}
			e.execute(id)
		}
	}
}

// execute runs one submitted run to a terminal state. It always writes exactly
// one terminal record: the store's FinishRun guard means a cancellation racing
// a natural exit cannot produce two.
func (e *Engine) execute(id string) {
	// Background, not a request context: a run outlives the HTTP request that
	// submitted it. Cancellation comes from Cancel or Shutdown.
	ctx := context.Background()

	specVal, ok := e.specs.LoadAndDelete(id)
	if !ok {
		// Cancelled out of the queue between submit and pickup.
		return
	}
	spec := specVal.(Spec)

	e.mu.Lock()
	_, stillPending := e.pending[id]
	if stillPending {
		delete(e.pending, id)
	}
	e.mu.Unlock()
	if !stillPending {
		// Cancel already finished this run while it sat in the queue.
		return
	}

	rec, err := e.store.GetRun(ctx, id)
	if err != nil {
		return
	}

	timeout := rec.Timeout
	if timeout <= 0 {
		timeout = e.timeoutFor(rec.Kind)
	}

	runCtx, cancel := context.WithCancel(ctx)
	timeoutCtx, cancelTimeout := context.WithTimeout(runCtx, timeout)
	defer cancelTimeout()

	e.mu.Lock()
	e.cancels[id] = cancel
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		delete(e.cancels, id)
		e.mu.Unlock()
		cancel()
	}()

	startedAt := e.now()
	if err := e.store.StartRun(ctx, id, startedAt); err != nil {
		// Another path already moved it out of queued (a cancel landing at the
		// same instant). Nothing to do.
		return
	}
	e.subs.publish(Event{Type: EventStatus, RunID: id, Status: store.StatusRunning})

	sink := newLogSink(ctx, e.store, e.subs, id, e.now)

	scriptPath, err := e.resolver.Resolve(rec.Script)
	if err != nil {
		sink.system("cannot run " + rec.Script + ": " + err.Error())
		sink.flush()
		e.finish(ctx, id, store.StatusFailed, nil, err.Error(), 0)
		return
	}

	dir := spec.Dir
	if dir == "" {
		dir = e.dir
	}

	stdout := sink.writer(store.StreamStdout)
	stderr := sink.writer(store.StreamStderr)

	cmd := toolchain.Command{
		Path:   scriptPath,
		Args:   rec.Argv,
		Dir:    dir,
		Env:    spec.Env,
		Stdout: stdout,
		Stderr: stderr,
	}

	// Monotonic measurement. Subtracting two wall-clock timestamps would be
	// wrong across a DST change or an NTP step.
	begin := time.Now()
	res, runErr := e.runner.Run(timeoutCtx, cmd)
	elapsed := time.Since(begin)

	// A script that dies mid-line, or ends without a trailing newline, still
	// has its last words recorded — that line is often the one explaining why.
	stdout.Flush()
	stderr.Flush()
	sink.flush()

	status, exitCode, message := classify(runCtx, timeoutCtx, timeout, res, runErr)
	if message != "" {
		sink.system(message)
		sink.flush()
	}

	stepStatus := store.StepSucceeded
	if status != store.StatusSucceeded {
		stepStatus = store.StepFailed
	}
	_ = e.store.FinishSteps(ctx, id, stepStatus, e.now())

	sink.close()
	e.finish(ctx, id, status, exitCode, message, elapsed)
}

// classify turns an execution outcome into a terminal status, an exit code and
// a human sentence for the transcript.
//
// The ordering matters: a process killed by our own SIGTERM reports a non-zero
// exit, but the *reason* is the timeout or the cancellation, and the user must
// be able to tell those apart from a script that genuinely failed.
func classify(runCtx, timeoutCtx context.Context, timeout time.Duration, res toolchain.Result, runErr error) (store.Status, *int, string) {
	switch {
	case errors.Is(timeoutCtx.Err(), context.DeadlineExceeded) && runCtx.Err() == nil:
		return store.StatusTimedOut, nil,
			"timed out after " + timeout.String() + " — raise the timeout for this run kind if the work is legitimately this slow"

	case runCtx.Err() != nil:
		return store.StatusCancelled, nil, "cancelled by user"

	case runErr == nil:
		return store.StatusSucceeded, intPtr(res.ExitCode), ""
	}

	var exitErr *toolchain.ExitError
	if errors.As(runErr, &exitErr) {
		return store.StatusFailed, intPtr(exitErr.ExitCode), exitErr.Error()
	}
	// Failed to start at all: missing binary, permission denied.
	return store.StatusFailed, nil, runErr.Error()
}

func (e *Engine) finish(ctx context.Context, id string, status store.Status, exitCode *int, message string, elapsed time.Duration) {
	// Ignore the error: a nil return means another path already recorded a
	// terminal state, which is the correct outcome for a race.
	_ = e.store.FinishRun(ctx, id, status, exitCode, message, e.now(), elapsed)
	e.subs.publish(Event{Type: EventStatus, RunID: id, Status: status})
}

func intPtr(i int) *int { return &i }
