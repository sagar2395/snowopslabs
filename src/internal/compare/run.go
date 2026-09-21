// SPDX-License-Identifier: Apache-2.0
package compare

import (
	"context"
	"fmt"
	"time"

	"github.com/sagar2395/snowopslabs/internal/workload"
)

// Lab is the side-effecting half of a comparison: everything that changes the
// cluster. It is an interface so the ordering rules below — which are what make
// a comparison fair — can be tested without a cluster.
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

// Sleeper waits, and gives up when the context does. Injected so tests do not
// spend the warmup.
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

// Report is one comparison: the conditions every workload was held to, and what
// each one did under them. The conditions are recorded with the numbers because
// a measurement without them cannot be compared with anything.
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

// Options returns the fair-run controls this report was produced under, so a
// stored report renders exactly as it did when it ran.
func (r *Report) Options() Options {
	return Options{
		Scenario: r.Scenario, Apps: r.Apps, Profile: r.Profile, RPS: r.RPS,
		Warmup: time.Duration(r.WarmupSeconds) * time.Second,
		Window: time.Duration(r.WindowSeconds) * time.Second,
	}
}

// Run measures every app in turn under identical conditions.
//
// Four rules make the result a comparison rather than two unrelated runs, and
// each is enforced here rather than left to the caller:
//
//  1. **One workload at a time.** Two apps measured concurrently share the
//     node's CPU, so each would be measured under the other's load — and the
//     one scheduled second would look slower on every run.
//  2. **The same conditions throughout.** Profile, rate and window come from a
//     single Options, validated once before anything is deployed.
//  3. **Warmup is excluded.** Load runs for Warmup before the window opens.
//     Measuring from the first request compares class loading and JIT warm-up,
//     which is how a comparison ends up reporting the runtime rather than the
//     application.
//  4. **Each app starts from the same state.** The scenario is torn down after
//     every app, so the second is not measured on a cluster the first left
//     scaled up.
//
// A failure on any app fails the whole run: a comparison missing a column is
// not a comparison, and reporting one silently would be worse than stopping.
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

// runOne measures a single workload and always tears down what it set up, so a
// failure midway does not leave the next app measuring a dirty cluster.
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

	// Teardown runs on every path out, including a cancelled context, and its
	// own failure never masks the failure that caused it.
	defer func() {
		_ = lab.TrafficStop(context.WithoutCancel(ctx))
		_ = lab.ScenarioDown(context.WithoutCancel(ctx), o.Scenario, w)
	}()

	if err := lab.TrafficStart(ctx, w, o.Profile, o.RPS, o.Load()); err != nil {
		return zero, fmt.Errorf("starting traffic: %w", err)
	}

	// Wait for load to actually arrive before the warmup clock starts.
	//
	// Starting the generator is not the same as being under load: the job has
	// to be scheduled, pull an image and ramp, which took longer than a 30s
	// warmup on the first real run of this harness. The window then opened on
	// an idle app and the comparison reported 0.3 req/s against 30 requested —
	// a plausible-looking table built on no load at all, which is worse than an
	// error because nothing about it says the run was void.
	if err := awaitLoad(ctx, q, w, o, sleep); err != nil {
		return zero, err
	}
	if err := sleep(ctx, o.Warmup); err != nil {
		return zero, fmt.Errorf("during warmup: %w", err)
	}
	if err := sleep(ctx, o.Window); err != nil {
		return zero, fmt.Errorf("during the measured window: %w", err)
	}

	// Measured after the window has closed, looking back over exactly its
	// length — so the warmup is outside the range, not merely before it.
	m, err := Measure(ctx, q, w, o.Window)
	if err != nil {
		return zero, err
	}
	// A window that saw almost none of the offered load did not measure this
	// workload; it measured the gap where the load should have been. Refusing
	// is the only honest answer, because every other number in the row was
	// taken under conditions the comparison does not describe.
	if got := m.Values[KeyRPS]; got < minServedFraction*float64(o.RPS) {
		return zero, fmt.Errorf(
			"only %.1f req/s of the %d offered reached the workload during the measured window; "+
				"the comparison would not be measuring the same conditions", got, o.RPS)
	}
	return m, nil
}

// minServedFraction is how much of the offered rate must actually be served for
// a window to count. Well below 1 because a saturated workload legitimately
// serves less than it is offered — which is a finding, not a void run — but far
// enough above 0 to catch a generator that never started.
const minServedFraction = 0.5

// loadSettleTimeout bounds the wait for load to arrive. Generous, because it
// covers scheduling and an image pull on a cold node.
const loadSettleTimeout = 3 * time.Minute

// loadPollWindow is the range awaitLoad reads over.
//
// It must span at least two scrapes, because `rate()` over a range holding one
// sample returns nothing at all. Measured the hard way: a 30s range on this
// lab's 30s scrape interval reported "no samples" for a workload that was
// serving exactly the 30 req/s it had been asked for, and the run aborted
// saying the load never arrived.
const loadPollWindow = 2 * time.Minute

// loadArrivedFraction is how much of the offered rate counts as "load is
// flowing". Well below the measurement threshold because this range starts
// before the generator did and averages the idle time in with the rest — the
// strict check is on the measured window, where the whole range is under load.
const loadArrivedFraction = 0.2

// awaitLoad blocks until load is actually reaching the workload.
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
