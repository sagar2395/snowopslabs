// SPDX-License-Identifier: Apache-2.0

package checks

import (
	"context"
	"time"
)

// Default polling settings for Eventually: enough time for a slow rollout
// without querying the cluster too often.
const (
	defaultEventuallyDeadline = 60 * time.Second
	defaultInitialInterval    = 1 * time.Second
	defaultMaxInterval        = 10 * time.Second
	defaultMultiplier         = 2.0
)

// EventuallyOpts controls how Eventually retries. Zero fields take the
// defaults, so EventuallyOpts{} is valid.
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
// A check that errors is retried like one that fails, since errors are common
// while things roll out. The returned Result is the last attempt, so a caller
// can tell a failing check from an erroring one.
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

		nextInterval := min(time.Duration(float64(interval)*opts.Multiplier), opts.MaxInterval)

		select {
		case <-deadlineCtx.Done():
			// Out of time or cancelled: return the last result.
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
