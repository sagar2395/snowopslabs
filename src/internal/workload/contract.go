// SPDX-License-Identifier: Apache-2.0
package workload

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Capability is something a scenario may require of the app it runs against,
// beyond the base interface every app must provide. The vocabulary is closed:
// a scenario's `requires:` and an app's APP_CAPABILITIES are matched by string,
// so an open vocabulary would let a typo on either side mean "never satisfied"
// with no error anywhere.
type Capability string

const (
	// CapPrometheusMetrics: serves MetricsPath in Prometheus exposition format,
	// including the RequestMetric histogram. Required by most scenarios.
	CapPrometheusMetrics Capability = "prometheus-metrics"
	// CapOTLPTracing: honours OTEL_EXPORTER_OTLP_ENDPOINT and exports spans.
	CapOTLPTracing Capability = "otlp-tracing"
	// CapReadinessToggle: readiness can be flipped to failing on demand, so a
	// scenario can watch probes, endpoints and alerts react without killing it.
	CapReadinessToggle Capability = "readiness-toggle"
)

// capabilityDoc is the closed vocabulary, with the reason each one exists. It is
// deliberately short: a capability earns its place by being something a scenario
// in this repository actually needs, never by being something an app might have.
var capabilityDoc = map[Capability]string{
	CapPrometheusMetrics: "serves the metrics path in Prometheus format, including the request-duration histogram",
	CapOTLPTracing:       "honours OTEL_EXPORTER_OTLP_ENDPOINT and exports spans",
	CapReadinessToggle:   "readiness can be flipped to failing on demand",
}

// AllCapabilities lists the vocabulary in a stable order, for error messages
// and documentation.
func AllCapabilities() []Capability {
	out := make([]Capability, 0, len(capabilityDoc))
	for c := range capabilityDoc {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Describe returns the one-line meaning of a capability, or "" if unknown.
func (c Capability) Describe() string { return capabilityDoc[c] }

// Valid reports whether c is in the vocabulary.
func (c Capability) Valid() bool { _, ok := capabilityDoc[c]; return ok }

// ParseCapability validates one capability name.
func ParseCapability(s string) (Capability, error) {
	c := Capability(strings.TrimSpace(s))
	if !c.Valid() {
		return "", fmt.Errorf("unknown capability %q (known: %s)", s, joinCaps(AllCapabilities()))
	}
	return c, nil
}

func joinCaps(caps []Capability) string {
	parts := make([]string, len(caps))
	for i, c := range caps {
		parts[i] = string(c)
	}
	return strings.Join(parts, ", ")
}

// Contract is the runtime interface an app promises, declared in its app.env.
// It is what makes one scenario runnable against many applications: the scenario
// names the contract, not the app.
type Contract struct {
	// Port is the port serving HTTP.
	Port string
	// HealthPath and ReadyPath back the liveness and readiness probes.
	HealthPath string
	ReadyPath  string
	// MetricsPath serves Prometheus exposition. Meaningful only with
	// CapPrometheusMetrics.
	MetricsPath string
	// RequestMetric is the request-duration histogram's name. Declared rather
	// than assumed: each language's instrumentation library picks its own, and a
	// scenario that hardcodes one grades the language instead of the engineer.
	RequestMetric string
	// Capabilities are the optional behaviours this app claims. A claim is only
	// a claim — `labctl app verify` is what checks it against a running pod.
	Capabilities []Capability
}

// Contract env keys, read from an app's app.env.
const (
	KeyPort          = "APP_PORT"
	KeyHealthPath    = "APP_HEALTH_PATH"
	KeyReadyPath     = "APP_READY_PATH"
	KeyMetricsPath   = "APP_METRICS_PATH"
	KeyRequestMetric = "APP_REQUEST_METRIC"
	KeyCapabilities  = "APP_CAPABILITIES"
)

// Base-interface defaults. Every app serves probes on a port, so defaulting
// these keeps a conforming app's declaration short. Capabilities have no
// default: an app claims one explicitly or does not have it.
const (
	DefaultHealthPath  = "/health"
	DefaultReadyPath   = "/ready"
	DefaultMetricsPath = "/metrics"
)

// ParseContract reads a contract from an app.env's key/value pairs. It is pure:
// the caller does the file IO, so the rules stay testable without a filesystem.
//
// Unset base fields take their defaults. An unknown or malformed value is an
// error, so a typo fails when the app is loaded rather than as a puzzling check
// failure once a scenario is halfway through activation.
func ParseContract(vals map[string]string) (Contract, error) {
	get := func(k, def string) string {
		if v := strings.TrimSpace(vals[k]); v != "" {
			return v
		}
		return def
	}
	c := Contract{
		Port:          get(KeyPort, DefaultPort),
		HealthPath:    get(KeyHealthPath, DefaultHealthPath),
		ReadyPath:     get(KeyReadyPath, DefaultReadyPath),
		MetricsPath:   get(KeyMetricsPath, DefaultMetricsPath),
		RequestMetric: get(KeyRequestMetric, DefaultMetric),
	}
	raw := strings.TrimSpace(vals[KeyCapabilities])
	if raw != "" {
		seen := make(map[Capability]bool)
		for _, part := range strings.Split(raw, ",") {
			if strings.TrimSpace(part) == "" {
				continue
			}
			parsed, err := ParseCapability(part)
			if err != nil {
				return Contract{}, fmt.Errorf("%s: %w", KeyCapabilities, err)
			}
			if seen[parsed] {
				continue
			}
			seen[parsed] = true
			c.Capabilities = append(c.Capabilities, parsed)
		}
		sort.Slice(c.Capabilities, func(i, j int) bool { return c.Capabilities[i] < c.Capabilities[j] })
	}
	if err := c.Validate(); err != nil {
		return Contract{}, err
	}
	return c, nil
}

// Validate reports why a contract is unusable.
func (c Contract) Validate() error {
	port, err := strconv.Atoi(c.Port)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("%s: %q is not a port between 1 and 65535", KeyPort, c.Port)
	}
	for _, f := range []struct{ key, val string }{
		{KeyHealthPath, c.HealthPath},
		{KeyReadyPath, c.ReadyPath},
	} {
		if !strings.HasPrefix(f.val, "/") {
			return fmt.Errorf("%s: %q must start with /", f.key, f.val)
		}
	}
	if c.Has(CapPrometheusMetrics) {
		if !strings.HasPrefix(c.MetricsPath, "/") {
			return fmt.Errorf("%s: %q must start with /", KeyMetricsPath, c.MetricsPath)
		}
		if strings.TrimSpace(c.RequestMetric) == "" {
			return fmt.Errorf("%s is required when %s declares %s",
				KeyRequestMetric, KeyCapabilities, CapPrometheusMetrics)
		}
	}
	for _, declared := range c.Capabilities {
		if !declared.Valid() {
			return fmt.Errorf("%s: unknown capability %q", KeyCapabilities, declared)
		}
	}
	return nil
}

// Has reports whether the app claims a capability.
func (c Contract) Has(want Capability) bool {
	for _, got := range c.Capabilities {
		if got == want {
			return true
		}
	}
	return false
}

// Missing returns the required capabilities this contract does not claim, in the
// order they were required, so a preflight message names every gap at once
// rather than one per re-run.
func (c Contract) Missing(required []Capability) []Capability {
	var out []Capability
	for _, r := range required {
		if !c.Has(r) {
			out = append(out, r)
		}
	}
	return out
}

// Workload binds this contract to a deployed name and namespace, so the port and
// metric a scenario templates come from the app's own declaration rather than a
// compiled-in guess.
func (c Contract) Workload(name, namespace string) Workload {
	if namespace == "" {
		namespace = name
	}
	return Workload{
		Name:      name,
		Namespace: namespace,
		Port:      c.Port,
		Metric:    c.RequestMetric,
	}.WithDefaults()
}
