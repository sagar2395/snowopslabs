// SPDX-License-Identifier: Apache-2.0
package compare

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sagar2395/snowopslabs/internal/workload"
	"github.com/sagar2395/snowopslabs/pkg/checks"
)

// fakeQuerier answers by substring match on the query, so a test says what it
// is stubbing rather than reproducing PromQL.
type fakeQuerier struct {
	answers map[string]string
	errs    map[string]error
	asked   []string
	// dynamic lets a test vary an answer across calls, for the polling paths.
	dynamic func(query string) (string, bool)
}

func (f *fakeQuerier) QueryScalar(_ context.Context, q string) (string, error) {
	f.asked = append(f.asked, q)
	if f.dynamic != nil {
		if v, ok := f.dynamic(q); ok {
			return v, nil
		}
	}
	for frag, err := range f.errs {
		if strings.Contains(q, frag) {
			return "", err
		}
	}
	for frag, v := range f.answers {
		if strings.Contains(q, frag) {
			return v, nil
		}
	}
	return "", checks.ErrNoSamples
}

func testWorkload(name string) workload.Workload {
	return workload.Workload{
		Name: name, Namespace: name, Port: "8080",
		Metric: "http_server_request_duration_seconds",
	}
}

func TestOptionsValidate(t *testing.T) {
	ok := Options{Scenario: "s", Apps: []string{"a", "b"}, RPS: 20, Window: 2 * time.Minute}
	tests := []struct {
		name string
		opts Options
		want string // substring the error must contain; "" means valid
	}{
		{"valid", ok, ""},
		{"no scenario", Options{Apps: []string{"a", "b"}, RPS: 1, Window: 2 * time.Minute}, "scenario is required"},
		{"one app is not a comparison", Options{Scenario: "s", Apps: []string{"a"}, RPS: 1, Window: 2 * time.Minute}, "at least two apps"},
		{"same app twice", Options{Scenario: "s", Apps: []string{"a", "a"}, RPS: 1, Window: 2 * time.Minute}, `"a" listed twice`},
		{"rps zero", Options{Scenario: "s", Apps: []string{"a", "b"}, RPS: 0, Window: 2 * time.Minute}, "rps must be between"},
		{"window under two scrapes", Options{Scenario: "s", Apps: []string{"a", "b"}, RPS: 1, Window: time.Minute}, "window must be at least 2m"},
		{"negative warmup", Options{Scenario: "s", Apps: []string{"a", "b"}, RPS: 1, Window: 2 * time.Minute, Warmup: -1}, "warmup must not be negative"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.opts.Validate()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("want valid, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// Load has to outlast the measured window, or traffic stops while the window is
// still being read and the tail of every comparison is measured at zero rps.
func TestLoadOutlastsTheMeasuredWindow(t *testing.T) {
	o := Options{Warmup: time.Minute, Window: 3 * time.Minute}
	if got, min := o.Load(), o.Warmup+o.Window; got <= min {
		t.Errorf("Load() = %s, want more than warmup+window (%s)", got, min)
	}
}

func TestPromDuration(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"2m30s", "150s"}, {"1m", "60s"}, {"3m", "180s"},
	} {
		d, _ := time.ParseDuration(tc.in)
		if got := PromDuration(d); got != tc.want {
			t.Errorf("PromDuration(%s) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestMeasureReadsTheSetAndDerivesTail(t *testing.T) {
	q := &fakeQuerier{answers: map[string]string{
		"_count{app=\"go-api\"}":             "42",
		"histogram_quantile(0.50":            "0.010",
		"histogram_quantile(0.99":            "0.250",
		"kube_deployment_status":             "3",
		"container_cpu_usage":                "0.25",
		"container_memory_working_set":       "1048576",
		"http_response_status_code=~\"5..\"": "0.5",
	}}
	m, err := Measure(context.Background(), q, testWorkload("go-api"), 3*time.Minute)
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}
	if got := m.Values[KeyP99]; got != 0.250 {
		t.Errorf("p99 = %v, want 0.25", got)
	}
	// Tail amplification is derived, never queried.
	if got := m.Values[KeyTail]; got != 25 {
		t.Errorf("tail amplification = %v, want 25 (p99/p50)", got)
	}
	if m.App != "go-api" {
		t.Errorf("App = %q", m.App)
	}
}

// A metric the cluster does not export must be absent, not zero: "used none"
// and "not measurable here" are different answers and the report says so.
func TestMeasureLeavesUnexportedMetricsAbsent(t *testing.T) {
	q := &fakeQuerier{answers: map[string]string{"_count{app=": "10"}}
	m, err := Measure(context.Background(), q, testWorkload("go-api"), 2*time.Minute)
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}
	if _, ok := m.Values[KeyCPU]; ok {
		t.Error("cpu recorded despite no samples")
	}
	if _, ok := m.Values[KeyRPS]; !ok {
		t.Error("throughput missing despite samples")
	}
}

// A partly-failed read would make the comparison silently unfair, so it fails.
func TestMeasureFailsOnAQueryError(t *testing.T) {
	q := &fakeQuerier{
		answers: map[string]string{"_count{app=": "10"},
		errs:    map[string]error{"histogram_quantile(0.99": fmt.Errorf("prometheus returned status 503")},
	}
	if _, err := Measure(context.Background(), q, testWorkload("go-api"), 2*time.Minute); err == nil {
		t.Fatal("want an error when a metric read fails")
	}
}

// NaN is what a quantile over an empty histogram returns. It means "nothing was
// measured", not "the run is broken".
func TestMeasureTreatsNaNAsAbsent(t *testing.T) {
	q := &fakeQuerier{answers: map[string]string{"histogram_quantile(0.50": "NaN"}}
	m, err := Measure(context.Background(), q, testWorkload("go-api"), 2*time.Minute)
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}
	if v, ok := m.Values[KeyP50]; ok {
		t.Errorf("p50 recorded as %v, want absent", v)
	}
}

func TestDeltaNamesWhichWayIsBetter(t *testing.T) {
	tests := []struct {
		name             string
		base, v          float64
		better           direction
		wantPct, wantSay string
	}{
		{"slower latency is worse", 0.010, 0.020, lower, "+100.0%", "worse"},
		{"faster latency is better", 0.020, 0.010, lower, "-50.0%", "better"},
		{"more throughput is better", 100, 150, higher, "+50.0%", "better"},
		{"replica count is neither", 2, 4, neutral, "+100.0%", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := delta(tc.base, tc.v, tc.better)
			if !strings.Contains(got, tc.wantPct) {
				t.Errorf("delta = %q, want it to contain %q", got, tc.wantPct)
			}
			if tc.wantSay == "" {
				if strings.Contains(got, "better") || strings.Contains(got, "worse") {
					t.Errorf("delta = %q, want no verdict for a neutral metric", got)
				}
				return
			}
			if !strings.Contains(got, tc.wantSay) {
				t.Errorf("delta = %q, want it to say %q", got, tc.wantSay)
			}
		})
	}
}

func TestDeltaHandlesAZeroBaseline(t *testing.T) {
	if got := delta(0, 5, higher); !strings.Contains(got, "n/a") {
		t.Errorf("delta from a zero baseline = %q, want n/a rather than a division", got)
	}
}

func TestRenderMarksTheBaselineAndExplainsDashes(t *testing.T) {
	var sb strings.Builder
	o := Options{Scenario: "autoscaling-under-load", Apps: []string{"go-api", "java-api"},
		Profile: "steady", RPS: 40, Warmup: time.Minute, Window: 3 * time.Minute}
	ms := []Measurement{
		{App: "go-api", Values: map[string]float64{KeyRPS: 40, KeyP50: 0.01}},
		{App: "java-api", Values: map[string]float64{KeyRPS: 40, KeyP50: 0.02}},
	}
	Render(&sb, o, ms)
	out := sb.String()

	for _, want := range []string{
		"GO-API (BASELINE)", "JAVA-API",
		"1m0s warmup (excluded)", // fairness is stated in the report, not just the code
		"+100.0% worse",          // java-api's p50 against the baseline
		"these are measurements", // never mistaken for a grade
	} {
		if !strings.Contains(strings.ToLower(out), strings.ToLower(want)) {
			t.Errorf("report missing %q\n%s", want, out)
		}
	}
	// Every metric no workload could report is named once, so a dash is
	// explained rather than looking like a failure.
	if !strings.Contains(out, "Not measurable in this cluster") {
		t.Errorf("report does not explain its dashes:\n%s", out)
	}
}
