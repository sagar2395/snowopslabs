// SPDX-License-Identifier: Apache-2.0

// Package compare measures a bound workload under load and diffs the result
// against another workload's measurement of the same scenario.
//
// Comparing stacks is a product feature (ADR-0014 §6), which makes fairness a
// requirement rather than a detail: every workload is measured under the same
// traffic profile, at the same request rate, over a window of the same length
// that starts only after an explicit warmup. Without the warmup exclusion the
// headline feature reports "the JVM is slower", which measures class loading
// and JIT warm-up rather than the application.
//
// What is measured is derived from the app contract, so any conforming app —
// including one the user brings — can be compared without declaring anything
// extra. Nothing here grades: a comparison reports numbers, and a scenario's
// checks remain the only thing that passes or fails.
package compare

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/sagar2395/snowopslabs/internal/workload"
	"github.com/sagar2395/snowopslabs/pkg/checks"
)

// Options are the fair-run controls. One set governs every workload in a
// comparison — that is what makes the runs comparable — so they are validated
// once, before the first app is deployed.
type Options struct {
	Scenario string        // scenario every workload is measured under
	Apps     []string      // workloads to measure, in order; the first is the baseline
	Profile  string        // traffic profile (steady keeps the rate flat, which is what a comparison wants)
	RPS      int           // offered request rate
	Warmup   time.Duration // load runs this long before measurement starts
	Window   time.Duration // length of the measured window
}

// Validate rejects a comparison that could not produce a fair result, before
// anything is deployed.
func (o Options) Validate() error {
	var errs []string
	if strings.TrimSpace(o.Scenario) == "" {
		errs = append(errs, "scenario is required")
	}
	if len(o.Apps) < 2 {
		errs = append(errs, "at least two apps are required (--apps go-api,java-api)")
	}
	seen := map[string]bool{}
	for _, a := range o.Apps {
		if strings.TrimSpace(a) == "" {
			errs = append(errs, "empty app name in --apps")
			continue
		}
		if seen[a] {
			errs = append(errs, fmt.Sprintf("app %q listed twice", a))
		}
		seen[a] = true
	}
	if o.RPS < 1 || o.RPS > 10000 {
		errs = append(errs, fmt.Sprintf("rps must be between 1 and 10000, got %d", o.RPS))
	}
	if o.Warmup < 0 {
		errs = append(errs, "warmup must not be negative")
	}
	// A rate over a range holding fewer than two scrapes returns nothing, so a
	// short window does not measure a quiet workload — it measures nothing and
	// says zero. Two minutes is four samples at the common 30s scrape interval.
	if o.Window < 2*time.Minute {
		errs = append(errs, fmt.Sprintf("window must be at least 2m so it spans several scrapes, got %s", o.Window))
	}
	if len(errs) > 0 {
		return fmt.Errorf("invalid comparison: %s", strings.Join(errs, "; "))
	}
	return nil
}

// Load is how long the traffic generator must run for one workload: the warmup
// plus the measured window, plus a margin so load never stops while the window
// is still being measured.
func (o Options) Load() time.Duration { return o.Warmup + o.Window + 30*time.Second }

// direction says which way is better for a metric, so the report can mark the
// winner without the reader having to remember that low latency is good and
// high throughput is good.
type direction int

const (
	neutral direction = iota
	lower
	higher
)

// Metric is one measured quantity: how to ask Prometheus for it and how to
// render the answer.
type Metric struct {
	Key    string // stable identifier, recorded in results history
	Label  string // column label
	Unit   string
	scale  float64 // multiplier from the Prometheus value to the displayed one
	digits int
	better direction
	// query builds the PromQL for one workload over the measured window; nil
	// for a metric derived from others.
	query func(w workload.Workload, window string) string
}

// Keys of the metrics a comparison reports. Referenced by name where a specific
// metric is derived or explained.
const (
	KeyRPS      = "requests_per_second"
	KeyP50      = "latency_p50"
	KeyP99      = "latency_p99"
	KeyTail     = "tail_amplification"
	KeyErrors   = "error_ratio"
	KeyReplicas = "peak_ready_replicas"
	KeyCPU      = "cpu_cores"
	KeyMemory   = "memory_working_set"
)

// Metrics is the fixed measurement set, in report order.
//
// Every one is derived from the app contract's declared request metric or from
// the cluster's own instrumentation, so a conforming app needs to declare
// nothing extra to be comparable. The set is deliberately closed: an author who
// could add metrics per scenario could add one that only their app can satisfy,
// and the comparison would stop being a comparison.
func Metrics() []Metric {
	// Selecting on the `app` label rather than the pod name matches how every
	// scenario check queries the workload, so a comparison and a check cannot
	// disagree about which series belong to the application.
	sel := func(w workload.Workload) string { return fmt.Sprintf(`{app=%q}`, w.Name) }
	pods := func(w workload.Workload) string {
		return fmt.Sprintf(`{namespace=%q,pod=~%q,container!=""}`, w.Namespace, w.Name+"-.*")
	}
	return []Metric{{
		Key: KeyRPS, Label: "throughput", Unit: "req/s", scale: 1, digits: 1, better: higher,
		query: func(w workload.Workload, win string) string {
			return fmt.Sprintf(`sum(rate(%s_count%s[%s]))`, w.Metric, sel(w), win)
		},
	}, {
		Key: KeyP50, Label: "latency p50", Unit: "ms", scale: 1000, digits: 1, better: lower,
		query: func(w workload.Workload, win string) string {
			return fmt.Sprintf(`histogram_quantile(0.50, sum(rate(%s_bucket%s[%s])) by (le))`, w.Metric, sel(w), win)
		},
	}, {
		Key: KeyP99, Label: "latency p99", Unit: "ms", scale: 1000, digits: 1, better: lower,
		query: func(w workload.Workload, win string) string {
			return fmt.Sprintf(`histogram_quantile(0.99, sum(rate(%s_bucket%s[%s])) by (le))`, w.Metric, sel(w), win)
		},
	}, {
		// Derived, not queried: the same ratio the scenarios grade on, and the
		// one number in this set that is comparable across runtimes on its own.
		Key: KeyTail, Label: "tail amplification", Unit: "x", scale: 1, digits: 2, better: lower,
	}, {
		Key: KeyErrors, Label: "5xx rate", Unit: "%", scale: 100, digits: 2, better: lower,
		query: func(w workload.Workload, win string) string {
			// `or vector(0)` because an app that has never returned a 5xx has no
			// 5xx series at all, and no samples would otherwise read as "not
			// measurable" rather than "none".
			return fmt.Sprintf(
				`(sum(rate(%s_count{app=%q,http_response_status_code=~"5.."}[%s])) or vector(0)) / clamp_min(sum(rate(%s_count%s[%s])), 0.001)`,
				w.Metric, w.Name, win, w.Metric, sel(w), win)
		},
	}, {
		Key: KeyReplicas, Label: "peak ready replicas", Unit: "", scale: 1, digits: 0, better: neutral,
		query: func(w workload.Workload, win string) string {
			return fmt.Sprintf(`max_over_time(kube_deployment_status_replicas_ready{namespace=%q,deployment=%q}[%s])`,
				w.Namespace, w.Name, win)
		},
	}, {
		// Total across replicas, not per pod: an app that answers the same load
		// on four replicas is not cheaper than one that answers it on two.
		Key: KeyCPU, Label: "cpu (total, mean)", Unit: "cores", scale: 1, digits: 3, better: lower,
		query: func(w workload.Workload, win string) string {
			return fmt.Sprintf(`avg_over_time(sum(rate(container_cpu_usage_seconds_total%s[1m]))[%s:1m])`, pods(w), win)
		},
	}, {
		Key: KeyMemory, Label: "memory (total, peak)", Unit: "MiB", scale: 1.0 / (1024 * 1024), digits: 1, better: lower,
		query: func(w workload.Workload, win string) string {
			return fmt.Sprintf(`max_over_time(sum(container_memory_working_set_bytes%s)[%s:1m])`, pods(w), win)
		},
	}}
}

// Querier is the slice of Prometheus a measurement needs. checks.Runner
// implements it, so a comparison reads the same Prometheus, through the same
// client, as the checks that grade the scenario.
type Querier interface {
	QueryScalar(ctx context.Context, query string) (string, error)
}

// Measurement is one workload's numbers over the measured window. A metric with
// no series is absent rather than zero — "the cluster does not export this" and
// "this workload used none" are different answers.
type Measurement struct {
	App    string             `json:"app"`
	Values map[string]float64 `json:"values"`
}

// PromDuration renders a window as a PromQL range. Whole seconds, so a window
// of 2m30s is a legal range rather than "2m30s" spelled Go's way.
func PromDuration(d time.Duration) string { return strconv.Itoa(int(d.Seconds())) + "s" }

// Measure runs the measurement set against one workload over the window that
// has just closed. A query that matches no series leaves its metric absent; any
// other failure is the whole measurement's failure, because a comparison built
// from partly-failed reads would be silently unfair.
func Measure(ctx context.Context, q Querier, w workload.Workload, window time.Duration) (Measurement, error) {
	m := Measurement{App: w.Name, Values: map[string]float64{}}
	win := PromDuration(window)
	for _, metric := range Metrics() {
		if metric.query == nil {
			continue
		}
		raw, err := q.QueryScalar(ctx, metric.query(w, win))
		switch {
		case errors.Is(err, checks.ErrNoSamples):
			continue
		case err != nil:
			return m, fmt.Errorf("measuring %s: %w", metric.Key, err)
		}
		v, err := strconv.ParseFloat(raw, 64)
		// A quantile over an empty histogram is NaN, and Go parses that happily.
		// It is a missing measurement, not a number: recorded as one it would
		// poison every ratio derived from it.
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		m.Values[metric.Key] = v
	}
	if p50, ok := m.Values[KeyP50]; ok && p50 > 0 {
		if p99, ok := m.Values[KeyP99]; ok {
			m.Values[KeyTail] = p99 / p50
		}
	}
	return m, nil
}

// Format renders one value in the metric's display unit.
func (m Metric) Format(v float64) string {
	s := strconv.FormatFloat(v*m.scale, 'f', m.digits, 64)
	if m.Unit == "" {
		return s
	}
	return s + " " + m.Unit
}

// Render writes the comparison table: one row per metric, one column per
// workload, and each non-baseline column's difference from the baseline.
func Render(out io.Writer, o Options, ms []Measurement) {
	fmt.Fprintf(out, "Comparison — %s\n", o.Scenario)
	fmt.Fprintf(out, "  profile %s at %d rps · %s warmup (excluded) · %s measured window\n\n",
		o.Profile, o.RPS, o.Warmup, o.Window)

	w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	header := []string{"METRIC"}
	for i, m := range ms {
		label := m.App
		if i == 0 {
			label += " (baseline)"
		}
		header = append(header, strings.ToUpper(label))
	}
	fmt.Fprintln(w, strings.Join(header, "\t"))

	for _, metric := range Metrics() {
		row := []string{metric.Label}
		for i, m := range ms {
			v, ok := m.Values[metric.Key]
			if !ok {
				row = append(row, "—")
				continue
			}
			cell := metric.Format(v)
			if i > 0 {
				if base, ok := ms[0].Values[metric.Key]; ok {
					cell += "  " + delta(base, v, metric.better)
				}
			}
			row = append(row, cell)
		}
		fmt.Fprintln(w, strings.Join(row, "\t"))
	}
	_ = w.Flush()

	if missing := missingMetrics(ms); len(missing) > 0 {
		fmt.Fprintf(out, "\nNot measurable in this cluster (no series): %s\n", strings.Join(missing, ", "))
	}
	fmt.Fprintf(out, "\nThese are measurements, not a grade: the scenario's checks decide pass/fail.\n")
}

// delta renders one column's difference from the baseline as a percentage,
// annotated with which way is better so "+1400%" on latency cannot be misread
// as an improvement.
func delta(base, v float64, better direction) string {
	if base == 0 {
		return "(n/a)"
	}
	pct := (v - base) / base * 100
	mark := ""
	switch {
	case better == neutral || pct == 0:
	case (better == lower) == (pct < 0):
		mark = " better"
	default:
		mark = " worse"
	}
	return fmt.Sprintf("(%+.1f%%%s)", pct, mark)
}

// missingMetrics names the metrics no workload could report, so a dash in the
// table is explained once rather than looking like a failed run.
func missingMetrics(ms []Measurement) []string {
	var out []string
	for _, metric := range Metrics() {
		any := false
		for _, m := range ms {
			if _, ok := m.Values[metric.Key]; ok {
				any = true
				break
			}
		}
		if !any {
			out = append(out, metric.Label)
		}
	}
	sort.Strings(out)
	return out
}

// Direction exposes which way is better for a metric, so a client can mark a
// winner without hardcoding the answer per metric key. Returned as two booleans
// rather than the unexported enum: the API layer serialises it, and a new
// direction should not silently become a number in a JSON contract.
func (m Metric) Direction() (lowerBetter, noPreference bool) {
	return m.better == lower, m.better == neutral
}

// Display exposes how a raw Prometheus value becomes the number a reader sees:
// the multiplier into the metric's unit, and the decimal places.
//
// Exported so every surface formats a metric the same way. The API sends these
// with the data rather than the client keeping its own table, because a client
// that decided "seconds" on its own rendered a 5ms latency as "0.005 ms".
func (m Metric) Display() (scale float64, digits int) { return m.scale, m.digits }
