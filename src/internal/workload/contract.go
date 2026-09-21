// SPDX-License-Identifier: Apache-2.0

package workload

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Capability is something a scenario may require of its app beyond the base
// interface every app provides. Only the values declared here are valid, so a
// misspelt capability in a scenario or an app.env is an error rather than a
// requirement that can never be met.
type Capability string

const (
	// CapPrometheusMetrics means the app serves MetricsPath in Prometheus
	// format, including the RequestMetric histogram. Most scenarios need it.
	CapPrometheusMetrics Capability = "prometheus-metrics"
	// CapOTLPTracing means the app exports spans to
	// OTEL_EXPORTER_OTLP_ENDPOINT.
	CapOTLPTracing Capability = "otlp-tracing"
	// CapReadinessToggle means the app's readiness can be made to fail on
	// request, so a scenario can watch probes and alerts react.
	CapReadinessToggle Capability = "readiness-toggle"
)

// capabilityDoc lists every valid capability with its meaning. Add one only
// when a scenario in this repository needs it.
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
	slices.Sort(out)
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

// Contract is the runtime interface an app declares in its app.env. Scenarios
// depend on the contract rather than on a specific app, so one scenario can run
// against many apps.
type Contract struct {
	// Port is the port serving HTTP.
	Port string
	// HealthPath and ReadyPath back the liveness and readiness probes.
	HealthPath string
	ReadyPath  string
	// MetricsPath serves Prometheus exposition. Meaningful only with
	// CapPrometheusMetrics.
	MetricsPath string
	// RequestMetric is the request-duration histogram's name. Each language's
	// instrumentation library picks its own, so the app declares it.
	RequestMetric string
	// Capabilities are the optional behaviours this app claims. `labctl app
	// verify` checks the claims against a running pod.
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

// Defaults for the base interface. Capabilities have no default: an app has
// only the ones it declares.
const (
	DefaultHealthPath  = "/health"
	DefaultReadyPath   = "/ready"
	DefaultMetricsPath = "/metrics"
)

// ParseContract builds a contract from an app.env's key/value pairs. Unset base
// fields take their defaults. An unknown or malformed value is an error, so a
// typo is caught when the app is loaded rather than during a scenario.
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
		for part := range strings.SplitSeq(raw, ",") {
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
		slices.Sort(c.Capabilities)
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
	return slices.Contains(c.Capabilities, want)
}

// Missing returns the capabilities in required that this contract does not
// claim, in the order given.
func (c Contract) Missing(required []Capability) []Capability {
	var out []Capability
	for _, r := range required {
		if !c.Has(r) {
			out = append(out, r)
		}
	}
	return out
}

// Workload returns a binding for the given deployment name and namespace, with
// the port and metric taken from this contract.
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
