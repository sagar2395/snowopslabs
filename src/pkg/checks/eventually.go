// SPDX-License-Identifier: Apache-2.0
package checks

import (
	"context"
	"time"
)

// Default polling parameters for Eventually. They are deliberately gentle so a
// slow-to-converge cluster (a rollout, a scaler warming up) is given time
// without hammering the API server.
const (
	defaultEventuallyDeadline = 60 * time.Second
	defaultInitialInterval    = 1 * time.Second
	defaultMaxInterval        = 10 * time.Second
	defaultMultiplier         = 2.0
)

// EventuallyOpts controls the retry/backoff behaviour of Eventually. The zero
// value is valid: every field falls back to a sensible default, so callers can
// pass EventuallyOpts{} for the standard policy or override only what they need.
type EventuallyOpts struct {
	// Deadline is the total wall-clock budget for the whole poll, across all
	// attempts. When it elapses Eventually returns the most recent Result.
	Deadline time.Duration
	// InitialInterval is the pause before the second attempt.
	InitialInterval time.Duration
	// MaxInterval caps the exponentially growing pause between attempts.
	MaxInterval time.Duration
	// Multiplier grows the interval after each failed attempt. Values <= 1
	// disable growth (constant interval).
	Multiplier float64
}

func (o EventuallyOpts) withDefaults() EventuallyOpts {
	if o.Deadline <= 0 {
		o.Deadline = defaultEventuallyDeadline
	}
	if o.InitialInterval <= 0 {
		o.InitialInterval = defaultInitialInterval
	}
	if o.MaxInterval <= 0 {
		o.MaxInterval = defaultMaxInterval
	}
	if o.Multiplier < 1 {
		o.Multiplier = defaultMultiplier
	}
	return o
}

// Eventually polls the check until it passes, the deadline elapses, or ctx is
// cancelled — whichever comes first. It returns the most recent Result, with
// Attempts set to the number of evaluations and DurationMS spanning the whole
// poll (not just the last attempt).
//
// A check that errors (misconfiguration, unreachable dependency) is retried the
// same as one that merely fails: transient errors during a rollout are
// expected. The final Result therefore reflects the last observation, so the
// caller can tell "never became true" from "still erroring".
func (r *Runner) Eventually(ctx context.Context, c Check, opts EventuallyOpts) Result {
	opts = opts.withDefaults()
	start := time.Now()

	deadlineCtx, cancel := context.WithTimeout(ctx, opts.Deadline)
	defer cancel()

	interval := opts.InitialInterval
	attempts := 0
	var last Result
	for {
		attempts++
		last = r.Run(deadlineCtx, c)
		last.Attempts = attempts
		if last.Pass {
			break
		}

		// Compute the next backoff, capped, before deciding whether to sleep.
		nextInterval := time.Duration(float64(interval) * opts.Multiplier)
		if nextInterval > opts.MaxInterval {
			nextInterval = opts.MaxInterval
		}

		select {
		case <-deadlineCtx.Done():
			// Budget exhausted or caller cancelled: return the last observation.
			last.Explanation = last.explain()
			last.DurationMS = time.Since(start).Milliseconds()
			return last
		case <-time.After(interval):
		}
		interval = nextInterval
	}

	last.Explanation = last.explain()
	last.DurationMS = time.Since(start).Milliseconds()
	return last
}
