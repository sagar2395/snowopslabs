// SPDX-License-Identifier: Apache-2.0

package metrics

import (
	"strings"
	"testing"
)

func TestCounterExposition(t *testing.T) {
	r := NewRegistry()
	c := r.NewCounterVec("reqs_total", "Total requests.", "method")
	c.Inc("get")
	c.Inc("get")
	c.Add(3, "post")

	got := r.Render()
	want := "# HELP reqs_total Total requests.\n" +
		"# TYPE reqs_total counter\n" +
		`reqs_total{method="get"} 2` + "\n" +
		`reqs_total{method="post"} 3` + "\n"
	if got != want {
		t.Errorf("counter exposition mismatch:\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestCounterUnlabeled(t *testing.T) {
	r := NewRegistry()
	c := r.NewCounterVec("events_total", "Events.")
	c.Inc()
	if got := r.Render(); !strings.Contains(got, "events_total 1\n") {
		t.Errorf("unlabeled counter should render without braces:\n%s", got)
	}
}

func TestGauge(t *testing.T) {
	r := NewRegistry()
	g := r.NewGaugeVec("in_flight", "In flight.")
	g.Inc()
	g.Inc()
	g.Dec()
	g.Add(5)
	g.Set(2)
	if got := r.Render(); !strings.Contains(got, "in_flight 2\n") || !strings.Contains(got, "# TYPE in_flight gauge\n") {
		t.Errorf("gauge render wrong:\n%s", got)
	}
}

func TestHistogramExposition(t *testing.T) {
	r := NewRegistry()
	h := r.NewHistogramVec("dur_seconds", "Durations.", []float64{0.1, 0.5, 1}, "route")
	// 0.05 -> bucket 0.1 ; 0.2 -> bucket 0.5 ; 3 -> +Inf overflow.
	h.Observe(0.05, "/a")
	h.Observe(0.2, "/a")
	h.Observe(3, "/a")

	got := r.Render()
	for _, want := range []string{
		"# TYPE dur_seconds histogram\n",
		`dur_seconds_bucket{route="/a",le="0.1"} 1` + "\n",  // 0.05
		`dur_seconds_bucket{route="/a",le="0.5"} 2` + "\n",  // 0.05, 0.2 (cumulative)
		`dur_seconds_bucket{route="/a",le="1"} 2` + "\n",    // still 2
		`dur_seconds_bucket{route="/a",le="+Inf"} 3` + "\n", // + the 3.0
		`dur_seconds_count{route="/a"} 3` + "\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("histogram output missing %q:\n%s", want, got)
		}
	}
	// _sum is 0.05 + 0.2 + 3 = 3.25
	if !strings.Contains(got, `dur_seconds_sum{route="/a"} 3.25`+"\n") {
		t.Errorf("histogram _sum wrong:\n%s", got)
	}
}

func TestHistogramBucketMonotonic(t *testing.T) {
	r := NewRegistry()
	h := r.NewHistogramVec("h", "h", []float64{1, 2, 3})
	for _, v := range []float64{0.5, 1.5, 2.5, 10} {
		h.Observe(v)
	}
	// Cumulative counts must be non-decreasing and end at the total.
	var prev int
	for _, le := range []string{"1", "2", "3", "+Inf"} {
		line := findLine(t, r.Render(), `h_bucket{le="`+le+`"} `)
		n := atoiTail(t, line)
		if n < prev {
			t.Errorf("bucket le=%s count %d < previous %d (not cumulative)", le, n, prev)
		}
		prev = n
	}
	if prev != 4 {
		t.Errorf("+Inf bucket = %d, want total 4", prev)
	}
}

func TestLabelValueEscaping(t *testing.T) {
	r := NewRegistry()
	c := r.NewCounterVec("c", "c", "path")
	c.Inc("a\"b\\c\nd")
	got := r.Render()
	if !strings.Contains(got, `c{path="a\"b\\c\nd"} 1`) {
		t.Errorf("label value not escaped correctly:\n%s", got)
	}
}

func TestBucketBoundFormatting(t *testing.T) {
	r := NewRegistry()
	h := r.NewHistogramVec("h", "h", []float64{0.005, 0.025, 2.5})
	h.Observe(0.001)
	got := r.Render()
	for _, le := range []string{"0.005", "0.025", "2.5"} {
		if !strings.Contains(got, `le="`+le+`"`) {
			t.Errorf("bucket bound %q not rendered verbatim:\n%s", le, got)
		}
	}
}

func TestSeriesSortedDeterministically(t *testing.T) {
	r := NewRegistry()
	c := r.NewCounterVec("c", "c", "k")
	for _, v := range []string{"zeta", "alpha", "mu"} {
		c.Inc(v)
	}
	got := r.Render()
	ia := strings.Index(got, `k="alpha"`)
	im := strings.Index(got, `k="mu"`)
	iz := strings.Index(got, `k="zeta"`)
	if ia >= im || im >= iz {
		t.Errorf("series not sorted by label value:\n%s", got)
	}
}

func TestInvalidDefinitionsPanic(t *testing.T) {
	cases := []struct {
		name string
		fn   func()
	}{
		{"bad metric name", func() { NewRegistry().NewCounterVec("1bad", "h") }},
		{"bad label name", func() { NewRegistry().NewCounterVec("ok", "h", "bad-label") }},
		{"duplicate label", func() { NewRegistry().NewCounterVec("ok", "h", "a", "a") }},
		{"non-ascending buckets", func() { NewRegistry().NewHistogramVec("ok", "h", []float64{1, 1}) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("expected a panic for %s", tc.name)
				}
			}()
			tc.fn()
		})
	}
}

func TestWrongArityPanics(t *testing.T) {
	r := NewRegistry()
	c := r.NewCounterVec("c", "c", "a", "b")
	defer func() {
		if recover() == nil {
			t.Error("expected a panic on wrong label arity")
		}
	}()
	c.Inc("only-one")
}

// helpers

func findLine(t *testing.T, out, prefix string) string {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	t.Fatalf("no line with prefix %q in:\n%s", prefix, out)
	return ""
}

func atoiTail(t *testing.T, line string) int {
	t.Helper()
	fields := strings.Fields(line)
	n := 0
	for _, r := range fields[len(fields)-1] {
		n = n*10 + int(r-'0')
	}
	return n
}
