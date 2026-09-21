// SPDX-License-Identifier: Apache-2.0

package compare

import (
	"context"
	"fmt"
	"time"

	"github.com/sagar2395/snowopslabs/internal/workload"
)

// Lab is everything a comparison does that changes the cluster. It is an
// interface so the ordering rules in Run can be tested without a cluster.
type Lab interface {
	// Bind resolves an app name to its workload contract.
	Bind(app string) (workload.Workload, error)
	// EnsureDeployed makes the workload serve, or reports why it cannot.
	EnsureDeployed(ctx context.Context, w workload.Workload) error
	ScenarioUp(ctx context.Context, scenario string, w workload.Workload) error
	ScenarioDown(ctx context.Context, scenario string, w workload.Workload) error
	TrafficStart(ctx context.Context, w workload.Workload, profile string, rps int, d time.Duration) error
	TrafficStop(ctx context.Context) error
}

// Sleeper waits for a duration or until the context ends. Tests replace it so
// they do not wait out the warmup.
type Sleeper func(ctx context.Context, d time.Duration) error

// RealSleeper waits on a timer or the context, whichever comes first.
func RealSleeper(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Report is one comparison: the conditions every workload was measured under,
// and each workload's results.
type Report struct {
	Scenario      string        `json:"scenario"`
	Apps          []string      `json:"apps"`
	Profile       string        `json:"profile"`
	RPS           int           `json:"rps"`
	WarmupSeconds int           `json:"warmupSeconds"`
	WindowSeconds int           `json:"windowSeconds"`
	StartedAt     time.Time     `json:"startedAt"`
	EndedAt       time.Time     `json:"endedAt"`
	Measurements  []Measurement `json:"measurements"`
}

// Options returns the conditions this report was produced under, so a stored
// report renders as it did when it ran.
func (r *Report) Options() Options {
	return Options{
		Scenario: r.Scenario, Apps: r.Apps, Profile: r.Profile, RPS: r.RPS,
		Warmup: time.Duration(r.WarmupSeconds) * time.Second,
		Window: time.Duration(r.WindowSeconds) * time.Second,
	}
}

// Run measures every app in turn under identical conditions.
//
// Run enforces four rules so the results are comparable:
//
//  1. One workload at a time. Apps measured together would compete for the
//     same CPU.
//  2. The same conditions for every app, from one validated Options.
//  3. Warmup is excluded: load runs for Warmup before the measured window
//     starts.
//  4. Every app starts from the same state: the scenario is torn down after
//     each app.
//
// If any app fails, the whole run fails, since a comparison with a missing
// workload is not useful.
func Run(ctx context.Context, o Options, lab Lab, q Querier, sleep Sleeper) (*Report, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}
	if sleep == nil {
		sleep = RealSleeper
	}

	rep := &Report{
		Scenario: o.Scenario, Apps: o.Apps, Profile: o.Profile, RPS: o.RPS,
		WarmupSeconds: int(o.Warmup.Seconds()), WindowSeconds: int(o.Window.Seconds()),
		StartedAt: time.Now().UTC(),
	}

	for _, app := range o.Apps {
		m, err := runOne(ctx, o, lab, q, sleep, app)
		if err != nil {
			return rep, fmt.Errorf("comparing %s: %w", app, err)
		}
		rep.Measurements = append(rep.Measurements, m)
	}

	rep.EndedAt = time.Now().UTC()
	return rep, nil
}

// runOne measures a single workload and always tears down what it set up,
// even when it fails part-way.
func runOne(ctx context.Context, o Options, lab Lab, q Querier, sleep Sleeper, app string) (Measurement, error) {
	var zero Measurement

	w, err := lab.Bind(app)
	if err != nil {
		return zero, fmt.Errorf("binding: %w", err)
	}
	if err := lab.EnsureDeployed(ctx, w); err != nil {
		return zero, fmt.Errorf("deploying: %w", err)
	}
	if err := lab.ScenarioUp(ctx, o.Scenario, w); err != nil {
		return zero, fmt.Errorf("activating %s: %w", o.Scenario, err)
	}

	// Teardown runs on every return path, including cancellation. A teardown
	// error never replaces the error that caused the return.
	defer func() {
		_ = lab.TrafficStop(context.WithoutCancel(ctx))
		_ = lab.ScenarioDown(context.WithoutCancel(ctx), o.Scenario, w)
	}()

	if err := lab.TrafficStart(ctx, w, o.Profile, o.RPS, o.Load()); err != nil {
		return zero, fmt.Errorf("starting traffic: %w", err)
	}

	// Wait until load is actually reaching the app before starting the warmup.
	// The traffic job must be scheduled, pull its image and ramp up, which can
	// take longer than the warmup itself.
	if err := awaitLoad(ctx, q, w, o, sleep); err != nil {
		return zero, err
	}
	if err := sleep(ctx, o.Warmup); err != nil {
		return zero, fmt.Errorf("during warmup: %w", err)
	}
	if err := sleep(ctx, o.Window); err != nil {
		return zero, fmt.Errorf("during the measured window: %w", err)
	}

	// Measure after the window ends, over exactly its length, so the warmup
	// falls outside the queried range.
	m, err := Measure(ctx, q, w, o.Window)
	if err != nil {
		return zero, err
	}
	// If the app served almost none of the offered load during the window,
	// the numbers do not describe it under load, so fail instead.
	if got := m.Values[KeyRPS]; got < minServedFraction*float64(o.RPS) {
		return zero, fmt.Errorf(
			"only %.1f req/s of the %d offered reached the workload during the measured window; "+
				"the comparison would not be measuring the same conditions", got, o.RPS)
	}
	return m, nil
}

// minServedFraction is the share of the offered rate an app must serve for its
// window to count. It is well below 1, since an overloaded app legitimately
// serves less, but high enough to catch load that never arrived.
const minServedFraction = 0.5

// loadSettleTimeout is how long to wait for load to arrive, including pod
// scheduling and an image pull.
const loadSettleTimeout = 3 * time.Minute

// loadPollWindow is the range awaitLoad queries. It must hold at least two
// scrapes, because rate() over a single sample returns nothing; with a 30s
// scrape interval, 30s is too short.
const loadPollWindow = 2 * time.Minute

// loadArrivedFraction is the share of the offered rate that counts as "load
// has arrived". It is lower than minServedFraction because the polled range
// can include time before the load started.
const loadArrivedFraction = 0.2

// awaitLoad blocks until load is reaching the workload, or loadSettleTimeout
// passes.
func awaitLoad(ctx context.Context, q Querier, w workload.Workload, o Options, sleep Sleeper) error {
	const poll = 10 * time.Second
	want := loadArrivedFraction * float64(o.RPS)

	for waited := time.Duration(0); waited < loadSettleTimeout; waited += poll {
		m, err := Measure(ctx, q, w, loadPollWindow)
		if err == nil && m.Values[KeyRPS] >= want {
			return nil
		}
		if err := sleep(ctx, poll); err != nil {
			return fmt.Errorf("waiting for load: %w", err)
		}
	}
	return fmt.Errorf("load never reached %d req/s within %s — is the traffic generator running?",
		o.RPS, loadSettleTimeout)
}
