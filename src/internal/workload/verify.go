// SPDX-License-Identifier: Apache-2.0
package workload

import (
	"fmt"

	"github.com/sagar2395/snowopslabs/pkg/checks"
)

// Checks returns the assertions that prove a deployed workload actually honours
// the contract it declares. A declaration is only a claim; these are what turn
// it into evidence.
//
// They are ordinary checks.Check values rather than bespoke logic so they run
// through the same runner, comparison and remediation machinery as a scenario's
// own checks — and so a claim is verified exactly the way a scenario would use
// it. Each capability contributes only the checks that capability implies, so an
// app is never failed for lacking something it never claimed.
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
		// Query Prometheus rather than the pod: it proves the metric exists AND
		// that scraping works, which is the state every metric-driven scenario
		// actually depends on. A histogram always carries a _count series.
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

// unprovable maps each capability that cluster state cannot demonstrate to the
// reason why. Both are conditional promises — "if you set this env var I will
// export", "if you call this endpoint I will go unready" — so a baseline
// deployment looks identical whether or not the app honours them. Failing an app
// for one would fail a conforming app; claiming it passed would be a lie.
var unprovable = map[Capability]string{
	CapOTLPTracing:     "the app exports only once OTEL_EXPORTER_OTLP_ENDPOINT is set, which a tracing scenario does; proving it means wiring a collector and finding spans",
	CapReadinessToggle: "proving it means deliberately making the app unready, which a read-only verification must not do",
}

// UnverifiableCapabilities are the declared capabilities Checks deliberately does
// not grade, so `app verify` reports them as claimed rather than pretending to
// have tested them. WhyUnverifiable explains each one.
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
