// SPDX-License-Identifier: Apache-2.0

// Package metrics is a tiny, dependency-free metrics registry that renders the
// Prometheus text exposition format (version 0.0.4).
//
// It exists so labctl can expose an optional /metrics endpoint without pulling
// in prometheus/client_golang and its transitive dependencies — the project is
// deliberately cgo-free and thin on dependencies (ADR-0002). The metric set is
// small and its label cardinality is bounded and known, so a hand-rolled
// registry is enough; if the surface grows past what this comfortably handles,
// swapping in client_golang behind the same call sites is a contained change.
package metrics

import (
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Registry holds a set of metric families and renders them. It is safe for
// concurrent use.
type Registry struct {
	mu         sync.Mutex
	collectors []collector
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry { return &Registry{} }

type collector interface {
	render(sb *strings.Builder)
}

func (r *Registry) add(c collector) {
	r.mu.Lock()
	r.collectors = append(r.collectors, c)
	r.mu.Unlock()
}

// Render returns the whole registry in Prometheus text exposition format.
// Families are rendered in registration order; series within a family are
// sorted by their label values so the output is deterministic.
func (r *Registry) Render() string {
	r.mu.Lock()
	cs := make([]collector, len(r.collectors))
	copy(cs, r.collectors)
	r.mu.Unlock()

	var sb strings.Builder
	for _, c := range cs {
		c.render(&sb)
	}
	return sb.String()
}

// ── Counter ──────────────────────────────────────────────────────────────────

// CounterVec is a set of monotonically increasing counters sharing a name and
// differing only in their label values.
type CounterVec struct {
	name, help string
	labels     []string
	mu         sync.Mutex
	series     map[string]*counterChild
}

type counterChild struct {
	lv  []string
	val float64
}

// NewCounterVec registers a counter family. Panics on a malformed name or
// duplicate label — a programming error, caught the first time it runs.
func (r *Registry) NewCounterVec(name, help string, labels ...string) *CounterVec {
	mustValidName(name)
	mustValidLabels(labels)
	c := &CounterVec{name: name, help: help, labels: labels, series: map[string]*counterChild{}}
	r.add(c)
	return c
}

// Inc adds one to the counter identified by labelValues (one per label, in
// order). A mismatched arity is a programming error and panics.
func (c *CounterVec) Inc(labelValues ...string) { c.Add(1, labelValues...) }

// Add adds delta (which must be >= 0) to the identified counter.
func (c *CounterVec) Add(delta float64, labelValues ...string) {
	if len(labelValues) != len(c.labels) {
		panic("metrics: counter " + c.name + ": wrong number of label values")
	}
	if delta < 0 {
		panic("metrics: counter " + c.name + ": negative delta")
	}
	key := labelKey(labelValues)
	c.mu.Lock()
	child := c.series[key]
	if child == nil {
		child = &counterChild{lv: append([]string(nil), labelValues...)}
		c.series[key] = child
	}
	child.val += delta
	c.mu.Unlock()
}

func (c *CounterVec) render(sb *strings.Builder) {
	c.mu.Lock()
	children := make([]*counterChild, 0, len(c.series))
	for _, ch := range c.series {
		children = append(children, ch)
	}
	c.mu.Unlock()
	sort.Slice(children, func(i, j int) bool { return lessLabels(children[i].lv, children[j].lv) })

	writeHeader(sb, c.name, c.help, "counter")
	for _, ch := range children {
		writeSample(sb, c.name, c.labels, ch.lv, ch.val)
	}
}

// ── Gauge ────────────────────────────────────────────────────────────────────

// GaugeVec is a set of gauges (values that can go up or down) sharing a name.
type GaugeVec struct {
	name, help string
	labels     []string
	mu         sync.Mutex
	series     map[string]*gaugeChild
}

type gaugeChild struct {
	lv  []string
	val float64
}

// NewGaugeVec registers a gauge family.
func (r *Registry) NewGaugeVec(name, help string, labels ...string) *GaugeVec {
	mustValidName(name)
	mustValidLabels(labels)
	g := &GaugeVec{name: name, help: help, labels: labels, series: map[string]*gaugeChild{}}
	r.add(g)
	return g
}

func (g *GaugeVec) child(labelValues []string) *gaugeChild {
	if len(labelValues) != len(g.labels) {
		panic("metrics: gauge " + g.name + ": wrong number of label values")
	}
	key := labelKey(labelValues)
	child := g.series[key]
	if child == nil {
		child = &gaugeChild{lv: append([]string(nil), labelValues...)}
		g.series[key] = child
	}
	return child
}

// Set replaces the value of the identified gauge.
func (g *GaugeVec) Set(v float64, labelValues ...string) {
	g.mu.Lock()
	g.child(labelValues).val = v
	g.mu.Unlock()
}

// Add adds delta (possibly negative) to the identified gauge.
func (g *GaugeVec) Add(delta float64, labelValues ...string) {
	g.mu.Lock()
	g.child(labelValues).val += delta
	g.mu.Unlock()
}

// Inc and Dec are Add(±1) on an unlabeled gauge (no label values).
func (g *GaugeVec) Inc(labelValues ...string) { g.Add(1, labelValues...) }
func (g *GaugeVec) Dec(labelValues ...string) { g.Add(-1, labelValues...) }

func (g *GaugeVec) render(sb *strings.Builder) {
	g.mu.Lock()
	children := make([]*gaugeChild, 0, len(g.series))
	for _, ch := range g.series {
		children = append(children, ch)
	}
	g.mu.Unlock()
	sort.Slice(children, func(i, j int) bool { return lessLabels(children[i].lv, children[j].lv) })

	writeHeader(sb, g.name, g.help, "gauge")
	for _, ch := range children {
		writeSample(sb, g.name, g.labels, ch.lv, ch.val)
	}
}

// ── Histogram ────────────────────────────────────────────────────────────────

// HistogramVec is a set of histograms sharing a name and bucket layout.
type HistogramVec struct {
	name, help string
	labels     []string
	bounds     []float64 // upper bounds, ascending, no +Inf (implicit)
	mu         sync.Mutex
	series     map[string]*histogramChild
}

type histogramChild struct {
	lv     []string
	counts []uint64 // per-bucket (not cumulative); len == len(bounds)+1 (last is +Inf overflow)
	sum    float64
	count  uint64
}

// DefaultHTTPBuckets are latency buckets tuned for sub-second HTTP handlers.
var DefaultHTTPBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// DefaultRunBuckets span the much longer durations of cluster/scenario runs.
var DefaultRunBuckets = []float64{0.5, 1, 5, 10, 30, 60, 120, 300, 600}

// NewHistogramVec registers a histogram family. Buckets are the inclusive upper
// bounds; they must be ascending. A +Inf bucket is always appended implicitly.
func (r *Registry) NewHistogramVec(name, help string, buckets []float64, labels ...string) *HistogramVec {
	mustValidName(name)
	mustValidLabels(labels)
	for i := 1; i < len(buckets); i++ {
		if buckets[i] <= buckets[i-1] {
			panic("metrics: histogram " + name + ": buckets must be strictly ascending")
		}
	}
	h := &HistogramVec{
		name: name, help: help, labels: labels,
		bounds: append([]float64(nil), buckets...),
		series: map[string]*histogramChild{},
	}
	r.add(h)
	return h
}

// Observe records one value (seconds) in the identified histogram.
func (h *HistogramVec) Observe(v float64, labelValues ...string) {
	if len(labelValues) != len(h.labels) {
		panic("metrics: histogram " + h.name + ": wrong number of label values")
	}
	key := labelKey(labelValues)
	h.mu.Lock()
	child := h.series[key]
	if child == nil {
		child = &histogramChild{lv: append([]string(nil), labelValues...), counts: make([]uint64, len(h.bounds)+1)}
		h.series[key] = child
	}
	// First bucket whose upper bound is >= v; overflow (+Inf) is the last slot.
	idx := sort.SearchFloat64s(h.bounds, v)
	child.counts[idx]++
	child.sum += v
	child.count++
	h.mu.Unlock()
}

func (h *HistogramVec) render(sb *strings.Builder) {
	h.mu.Lock()
	children := make([]*histogramChild, 0, len(h.series))
	for _, ch := range h.series {
		children = append(children, ch)
	}
	h.mu.Unlock()
	sort.Slice(children, func(i, j int) bool { return lessLabels(children[i].lv, children[j].lv) })

	writeHeader(sb, h.name, h.help, "histogram")
	for _, ch := range children {
		var cum uint64
		for i, ub := range h.bounds {
			cum += ch.counts[i]
			writeBucket(sb, h.name, h.labels, ch.lv, formatFloat(ub), cum)
		}
		cum += ch.counts[len(h.bounds)] // +Inf overflow
		writeBucket(sb, h.name, h.labels, ch.lv, "+Inf", cum)
		writeSample(sb, h.name+"_sum", h.labels, ch.lv, ch.sum)
		writeSampleUint(sb, h.name+"_count", h.labels, ch.lv, ch.count)
	}
}

// ── Rendering helpers ────────────────────────────────────────────────────────

func writeHeader(sb *strings.Builder, name, help, typ string) {
	if help != "" {
		sb.WriteString("# HELP ")
		sb.WriteString(name)
		sb.WriteByte(' ')
		sb.WriteString(escapeHelp(help))
		sb.WriteByte('\n')
	}
	sb.WriteString("# TYPE ")
	sb.WriteString(name)
	sb.WriteByte(' ')
	sb.WriteString(typ)
	sb.WriteByte('\n')
}

func writeSample(sb *strings.Builder, name string, labelNames, labelValues []string, v float64) {
	sb.WriteString(name)
	writeLabels(sb, labelNames, labelValues, "", "")
	sb.WriteByte(' ')
	sb.WriteString(formatFloat(v))
	sb.WriteByte('\n')
}

func writeSampleUint(sb *strings.Builder, name string, labelNames, labelValues []string, v uint64) {
	sb.WriteString(name)
	writeLabels(sb, labelNames, labelValues, "", "")
	sb.WriteByte(' ')
	sb.WriteString(strconv.FormatUint(v, 10))
	sb.WriteByte('\n')
}

func writeBucket(sb *strings.Builder, name string, labelNames, labelValues []string, le string, cum uint64) {
	sb.WriteString(name)
	sb.WriteString("_bucket")
	writeLabels(sb, labelNames, labelValues, "le", le)
	sb.WriteByte(' ')
	sb.WriteString(strconv.FormatUint(cum, 10))
	sb.WriteByte('\n')
}

// writeLabels renders {a="1",b="2"}; an extra label (extraName/extraVal) is
// appended last, used for the histogram le="…" bucket bound.
func writeLabels(sb *strings.Builder, names, values []string, extraName, extraVal string) {
	if len(names) == 0 && extraName == "" {
		return
	}
	sb.WriteByte('{')
	for i, n := range names {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(n)
		sb.WriteString(`="`)
		sb.WriteString(escapeLabelValue(values[i]))
		sb.WriteByte('"')
	}
	if extraName != "" {
		if len(names) > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(extraName)
		sb.WriteString(`="`)
		sb.WriteString(escapeLabelValue(extraVal))
		sb.WriteByte('"')
	}
	sb.WriteByte('}')
}

func formatFloat(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }

var labelValueEscaper = strings.NewReplacer(`\`, `\\`, "\n", `\n`, `"`, `\"`)
var helpEscaper = strings.NewReplacer(`\`, `\\`, "\n", `\n`)

func escapeLabelValue(s string) string { return labelValueEscaper.Replace(s) }
func escapeHelp(s string) string       { return helpEscaper.Replace(s) }

// labelKey joins label values into a map key that cannot collide across
// different value tuples (a real separator byte that cannot appear mid-value
// is unnecessary — values are joined with a NUL, which argv never contains).
func labelKey(values []string) string { return strings.Join(values, "\x00") }

func lessLabels(a, b []string) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

// ── Name validation (cheap guard against malformed metric definitions) ────────

func mustValidName(name string) {
	if !validIdent(name) {
		panic("metrics: invalid metric name " + strconv.Quote(name))
	}
}

func mustValidLabels(labels []string) {
	seen := map[string]bool{}
	for _, l := range labels {
		if !validIdent(l) {
			panic("metrics: invalid label name " + strconv.Quote(l))
		}
		if seen[l] {
			panic("metrics: duplicate label name " + strconv.Quote(l))
		}
		seen[l] = true
	}
}

// validIdent matches Prometheus's [a-zA-Z_][a-zA-Z0-9_]* (colons are legal in
// metric names but reserved for recording rules, so we disallow them here).
func validIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_':
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}
