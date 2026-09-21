// SPDX-License-Identifier: Apache-2.0

package compare

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sagar2395/snowopslabs/internal/workload"
)

// fakeLab records the order of everything it is asked to do, because the order
// is what makes a comparison fair.
type fakeLab struct {
	log      []string
	failOn   string // step to fail, e.g. "up:java-api"
	inflight int    // workloads under load right now
	maxSeen  int
}

func (l *fakeLab) step(s string) error {
	l.log = append(l.log, s)
	if l.failOn == s {
		return errors.New("boom")
	}
	return nil
}

func (l *fakeLab) Bind(app string) (workload.Workload, error) {
	if err := l.step("bind:" + app); err != nil {
		return workload.Workload{}, err
	}
	return testWorkload(app), nil
}
func (l *fakeLab) EnsureDeployed(_ context.Context, w workload.Workload) error {
	return l.step("deploy:" + w.Name)
}
func (l *fakeLab) ScenarioUp(_ context.Context, s string, w workload.Workload) error {
	return l.step("up:" + w.Name)
}
func (l *fakeLab) ScenarioDown(_ context.Context, s string, w workload.Workload) error {
	return l.step("down:" + w.Name)
}
func (l *fakeLab) TrafficStart(_ context.Context, w workload.Workload, _ string, _ int, _ time.Duration) error {
	l.inflight++
	if l.inflight > l.maxSeen {
		l.maxSeen = l.inflight
	}
	return l.step("traffic-start:" + w.Name)
}
func (l *fakeLab) TrafficStop(_ context.Context) error {
	if l.inflight > 0 {
		l.inflight--
	}
	return l.step("traffic-stop")
}

func okQuerier() *fakeQuerier {
	return &fakeQuerier{answers: map[string]string{
		"_count{app=": "40", // matches the offered 40 rps

		"histogram_quantile(0.50":      "0.01",
		"histogram_quantile(0.99":      "0.02",
		"kube_deployment_status":       "2",
		"container_cpu_usage":          "0.1",
		"container_memory_working_set": "1048576",
	}}
}

func fairOptions() Options {
	return Options{
		Scenario: "autoscaling-under-load", Apps: []string{"go-api", "java-api"},
		Profile: "steady", RPS: 40, Warmup: time.Minute, Window: 3 * time.Minute,
	}
}

// Each workload must be set up, loaded, measured and torn down before the
// next one begins.
func TestRunMeasuresOneWorkloadAtATime(t *testing.T) {
	lab := &fakeLab{}
	rep, err := Run(context.Background(), fairOptions(), lab, okQuerier(), noSleep)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if lab.maxSeen != 1 {
		t.Errorf("%d workloads were under load at once; a comparison must measure one at a time", lab.maxSeen)
	}
	want := []string{
		"bind:go-api", "deploy:go-api", "up:go-api", "traffic-start:go-api", "traffic-stop", "down:go-api",
		"bind:java-api", "deploy:java-api", "up:java-api", "traffic-start:java-api", "traffic-stop", "down:java-api",
	}
	if strings.Join(lab.log, ",") != strings.Join(want, ",") {
		t.Errorf("order was\n  %v\nwant\n  %v", lab.log, want)
	}
	if len(rep.Measurements) != 2 {
		t.Fatalf("got %d measurements, want 2", len(rep.Measurements))
	}
	if rep.Measurements[0].App != "go-api" {
		t.Errorf("baseline is %q, want the first app listed", rep.Measurements[0].App)
	}
}

// The warmup must be waited out AND excluded from the range that is measured.
// Measuring a 4m range after a 1m warmup would put the warmup back in.
func TestRunExcludesTheWarmupFromTheMeasuredRange(t *testing.T) {
	o := fairOptions()
	var waits []time.Duration
	sleep := func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}
	q := okQuerier()
	if _, err := Run(context.Background(), o, &fakeLab{}, q, sleep); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Two waits per app: the warmup, then the window.
	if len(waits) != 4 || waits[0] != o.Warmup || waits[1] != o.Window {
		t.Fatalf("waits = %v, want warmup then window for each app", waits)
	}
	// Every range asked of Prometheus is the window, never warmup+window.
	wantRange := "[" + PromDuration(o.Window) + "]"
	badRange := "[" + PromDuration(o.Warmup+o.Window) + "]"
	for _, q := range q.asked {
		if strings.Contains(q, badRange) {
			t.Fatalf("a query measured warmup+window:\n  %s", q)
		}
	}
	found := false
	for _, q := range q.asked {
		if strings.Contains(q, wantRange) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no query used the measured window %s", wantRange)
	}
}

// A comparison missing a column is not a comparison.
func TestRunFailsTheWholeComparisonWhenOneAppFails(t *testing.T) {
	lab := &fakeLab{failOn: "up:java-api"}
	_, err := Run(context.Background(), fairOptions(), lab, okQuerier(), noSleep)
	if err == nil {
		t.Fatal("want an error when one workload cannot be measured")
	}
	if !strings.Contains(err.Error(), "java-api") {
		t.Errorf("error = %v, want it to name the app that failed", err)
	}
}

// A failure midway must not leave the cluster loaded or the scenario active,
// or the next comparison measures whatever was left behind.
func TestRunTearsDownEvenWhenAStepFails(t *testing.T) {
	lab := &fakeLab{failOn: "traffic-start:go-api"}
	if _, err := Run(context.Background(), fairOptions(), lab, okQuerier(), noSleep); err == nil {
		t.Fatal("want an error")
	}
	joined := strings.Join(lab.log, ",")
	for _, want := range []string{"traffic-stop", "down:go-api"} {
		if !strings.Contains(joined, want) {
			t.Errorf("teardown step %q did not run after a failure: %v", want, lab.log)
		}
	}
}

// Cancelling during the warmup must still stop the load it started.
func TestRunStopsTrafficWhenCancelled(t *testing.T) {
	lab := &fakeLab{}
	ctx, cancel := context.WithCancel(context.Background())
	sleep := func(context.Context, time.Duration) error {
		cancel()
		return context.Canceled
	}
	if _, err := Run(ctx, fairOptions(), lab, okQuerier(), sleep); err == nil {
		t.Fatal("want an error when cancelled")
	}
	if !strings.Contains(strings.Join(lab.log, ","), "traffic-stop") {
		t.Errorf("cancelled run left traffic running: %v", lab.log)
	}
}

func TestRunRejectsAnUnfairComparisonBeforeTouchingTheCluster(t *testing.T) {
	lab := &fakeLab{}
	o := fairOptions()
	o.Apps = []string{"go-api"} // not a comparison
	if _, err := Run(context.Background(), o, lab, okQuerier(), noSleep); err == nil {
		t.Fatal("want a validation error")
	}
	if len(lab.log) != 0 {
		t.Errorf("cluster was touched before validation: %v", lab.log)
	}
}

// The conditions travel with the numbers: a stored report has to render as it
// ran, or it cannot be compared with a later one.
func TestReportRoundTripsItsOptions(t *testing.T) {
	o := fairOptions()
	rep, err := Run(context.Background(), o, &fakeLab{}, okQuerier(), noSleep)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := rep.Options()
	if got.Warmup != o.Warmup || got.Window != o.Window || got.RPS != o.RPS || got.Profile != o.Profile {
		t.Errorf("Options() = %+v, want %+v", got, o)
	}
}

func noSleep(context.Context, time.Duration) error { return nil }

func TestRealSleeperReturnsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := RealSleeper(ctx, time.Hour); err == nil {
		t.Fatal("want the context error rather than an hour")
	}
	if err := RealSleeper(context.Background(), 0); err != nil {
		t.Errorf("a zero wait should not error: %v", err)
	}
}

// Starting the generator is not being under load. The window must not open
// until the offered rate is actually arriving, or it measures the ramp.
func TestRunWaitsForLoadBeforeTheWarmupClockStarts(t *testing.T) {
	// Serves nothing for the first two polls, then reaches rate.
	polls := 0
	q := &fakeQuerier{answers: map[string]string{
		"histogram_quantile(0.50":      "0.01",
		"histogram_quantile(0.99":      "0.02",
		"container_cpu_usage":          "0.1",
		"container_memory_working_set": "1048576",
	}}
	q.dynamic = func(query string) (string, bool) {
		if !strings.Contains(query, "_count{app=") {
			return "", false
		}
		polls++
		if polls <= 2 {
			return "0", true
		}
		return "40", true
	}

	var waits []time.Duration
	sleep := func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}
	o := fairOptions()
	o.Apps = []string{"go-api"}
	o.Apps = append(o.Apps, "java-api")

	if _, err := Run(context.Background(), o, &fakeLab{}, q, sleep); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The first app polled while load was absent, so there are settle waits
	// before its warmup rather than the warmup starting immediately.
	if len(waits) < 4 {
		t.Fatalf("waits = %v, want settle polls before warmup", waits)
	}
	if waits[0] == o.Warmup {
		t.Errorf("warmup started before load arrived: waits = %v", waits)
	}
}

// A window that saw none of the offered load is a void run, and reporting it as
// a comparison is worse than failing: nothing in the table says it is empty.
func TestRunRefusesAWindowThatSawNoLoad(t *testing.T) {
	q := &fakeQuerier{answers: map[string]string{
		"_count{app=":                  "0.3", // health probes only, against 40 offered
		"histogram_quantile(0.50":      "0.01",
		"container_memory_working_set": "1048576",
	}}
	_, err := Run(context.Background(), fairOptions(), &fakeLab{}, q, noSleep)
	if err == nil {
		t.Fatal("want an error when the offered load never arrived")
	}
	if !strings.Contains(err.Error(), "reached the workload") &&
		!strings.Contains(err.Error(), "never reached") {
		t.Errorf("error = %v, want it to say the load did not arrive", err)
	}
}
