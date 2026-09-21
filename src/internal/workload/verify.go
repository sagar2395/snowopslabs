// SPDX-License-Identifier: Apache-2.0

package workload

import (
	"fmt"

	"github.com/sagar2395/snowopslabs/pkg/checks"
)

// Checks returns the checks that confirm a deployed workload honours the
// contract it declares. They are ordinary checks.Check values, run by the same
// runner as scenario checks. Each claimed capability adds only its own checks,
// so an app is never failed for something it did not claim.
func (c Contract) Checks(w Workload) []checks.Check {
	w = w.WithDefaults()
	deploy := "deployment/" + w.Name

	out := []checks.Check{
		{
			Name:      "workload-ready",
			Type:      "kubectl",
			Resource:  deploy,
			Namespace: w.Namespace,
			JSONPath:  "{.status.readyReplicas}",
			Operator:  ">=",
			Value:     "1",
			Remediation: fmt.Sprintf(
				"%s has no ready replicas. Deploy it first: labctl app deploy %s", deploy, w.Name),
		},
		{
			Name:      "serves-contract-port",
			Type:      "kubectl",
			Resource:  deploy,
			Namespace: w.Namespace,
			JSONPath:  "{.spec.template.spec.containers[*].ports[*].containerPort}",
			Operator:  "contains",
			Value:     c.Port,
			Remediation: fmt.Sprintf(
				"No container exposes port %s. Either fix the chart's containerPort or correct %s in apps/%s/app.env.",
				c.Port, KeyPort, w.Name),
		},
		{
			Name:      "liveness-probe-path",
			Type:      "kubectl",
			Resource:  deploy,
			Namespace: w.Namespace,
			JSONPath:  "{.spec.template.spec.containers[0].livenessProbe.httpGet.path}",
			Operator:  "==",
			Value:     c.HealthPath,
			Remediation: fmt.Sprintf(
				"The liveness probe does not target %s. A scenario that restarts the app relies on this path; align the chart with %s.",
				c.HealthPath, KeyHealthPath),
		},
		{
			Name:      "readiness-probe-path",
			Type:      "kubectl",
			Resource:  deploy,
			Namespace: w.Namespace,
			JSONPath:  "{.spec.template.spec.containers[0].readinessProbe.httpGet.path}",
			Operator:  "==",
			Value:     c.ReadyPath,
			Remediation: fmt.Sprintf(
				"The readiness probe does not target %s. Align the chart with %s.",
				c.ReadyPath, KeyReadyPath),
		},
	}

	if c.Has(CapPrometheusMetrics) {
		// Query Prometheus rather than the pod, so the check also proves the
		// metric is being scraped. Every histogram has a _count series.
		out = append(out, checks.Check{
			Name:     "request-metric-scraped",
			Type:     "promql",
			Query:    fmt.Sprintf(`count(%s_count{app=%q})`, c.RequestMetric, w.Name),
			Operator: ">=",
			Value:    "1",
			Remediation: fmt.Sprintf(
				"Prometheus has no %s_count for app=%q. Either the app does not expose %s (check %s), it is not being scraped (the pod needs prometheus.io/scrape and an app=%s label), or no traffic has reached it yet — try: labctl traffic start --profile steady --rps 10",
				c.RequestMetric, w.Name, c.RequestMetric, KeyRequestMetric, w.Name),
		})
	}

	return out
}

// unprovable maps each capability that cannot be checked from cluster state
// to the reason. These capabilities only show when something triggers them, so
// an idle deployment looks the same whether or not the app supports them.
var unprovable = map[Capability]string{
	CapOTLPTracing:     "the app exports only once OTEL_EXPORTER_OTLP_ENDPOINT is set, which a tracing scenario does; proving it means wiring a collector and finding spans",
	CapReadinessToggle: "proving it means deliberately making the app unready, which a read-only verification must not do",
}

// UnverifiableCapabilities returns the claimed capabilities that Checks does
// not test, so `app verify` can report them as claimed but untested.
func (c Contract) UnverifiableCapabilities() []Capability {
	var out []Capability
	for _, declared := range c.Capabilities {
		if _, ok := unprovable[declared]; ok {
			out = append(out, declared)
		}
	}
	return out
}

// WhyUnverifiable returns the reason a capability cannot be graded from cluster
// state, or "" if it can be.
func WhyUnverifiable(c Capability) string { return unprovable[c] }
