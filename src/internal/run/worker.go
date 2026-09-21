// SPDX-License-Identifier: Apache-2.0

package run

import (
	"context"
	"errors"
	"fmt"
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
	// A run outlives the request that submitted it, so it starts from
	// Background. Cancel, Shutdown and the timeout are what stop it.
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
		// A concurrent Cancel already moved it out of the queued state.
		return
	}
	e.subs.publish(Event{Type: EventStatus, RunID: id, Status: store.StatusRunning})
	if e.metrics != nil {
		e.metrics.RunStarted()
	}

	sink := newLogSink(ctx, e.store, e.subs, id, e.now)

	var (
		res    toolchain.Result
		runErr error
		begin  = time.Now()
	)
	if spec.Func != nil {
		res, runErr = e.executeFunc(timeoutCtx, spec.Func, sink)
	} else {
		scriptPath, err := e.resolver.Resolve(rec.Script)
		if err != nil {
			sink.system("cannot run " + rec.Script + ": " + err.Error())
			sink.flush()
			e.finish(ctx, id, rec.Kind, store.StatusFailed, nil, err.Error(), 0)
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

		res, runErr = e.runner.Run(timeoutCtx, cmd)

		// Keep a final line that had no trailing newline; it is often the error.
		stdout.Flush()
		stderr.Flush()
	}
	elapsed := time.Since(begin)
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
	e.finish(ctx, id, rec.Kind, status, exitCode, message, elapsed)
}

// executeFunc runs an in-process operation with its output going to the sink,
// and returns the same Result/error shape a script run does so classify can
// treat both alike. A panic in fn becomes a failed run, not a crashed worker.
func (e *Engine) executeFunc(ctx context.Context, fn RunFunc, sink *logSink) (res toolchain.Result, err error) {
	out := sink.writer(store.StreamStdout)
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("operation panicked: %v", r)
		}
		out.Flush()
	}()
	err = fn(ctx, out)
	return toolchain.Result{}, err
}

// classify turns an execution outcome into a terminal status, an exit code and
// a human sentence for the transcript.
//
// The context checks come first: a process we killed on timeout or cancel
// also exits non-zero, and the user needs to see the real reason rather than
// "failed".
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

func (e *Engine) finish(ctx context.Context, id, kind string, status store.Status, exitCode *int, message string, elapsed time.Duration) {
	// An error here means another path already recorded a terminal state for
	// this run, and that record stands.
	_ = e.store.FinishRun(ctx, id, status, exitCode, message, e.now(), elapsed)
	e.subs.publish(Event{Type: EventStatus, RunID: id, Status: status})
	if e.metrics != nil {
		e.metrics.RunFinished(kind, string(status), elapsed)
	}
	// Re-read the record so hooks see every field, including target and lock
	// key. Hooks run on this worker, so Shutdown waits for them.
	if len(e.finishHooks) > 0 {
		if rec, err := e.store.GetRun(ctx, id); err == nil {
			for _, h := range e.finishHooks {
				h(ctx, rec)
			}
		}
	}
}

func intPtr(i int) *int { return &i }
