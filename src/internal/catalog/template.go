// SPDX-License-Identifier: Apache-2.0
package catalog

import "github.com/sagar2395/snowopslabs/internal/tmpl"

// TemplateContext is the typed set of variables content templates may reference.
// The definition lives in internal/tmpl so the validator and the two run-time
// engines cannot drift apart on which variables exist.
type TemplateContext = tmpl.Context

// DefaultTemplateContext returns a context populated with the standard defaults
// used when a caller only wants to validate that templates are well-formed and
// reference known keys (values need not be the deployment's real ones).
func DefaultTemplateContext(projectRoot string) TemplateContext {
	return TemplateContext{
		DomainSuffix:        "k3d.local",
		MonitoringNamespace: "monitoring",
		ProjectRoot:         projectRoot,
		LokiRetentionPeriod: "72h",
		IngressClass:        "traefik",
		WorkloadName:        "go-api",
		WorkloadNamespace:   "go-api",
		WorkloadService:     "go-api.go-api.svc.cluster.local",
		WorkloadPort:        "8080",
		WorkloadMetric:      "http_server_request_duration_seconds",
	}
}

// Validate reports a reference to an unknown labctl template variable, so an
// authoring typo surfaces at validation instead of producing a broken URL or
// namespace at run time. Other systems' templating in the same string — Loki,
// Prometheus, Grafana, Helm — is left alone, exactly as the run-time expander
// leaves it. See internal/tmpl.
func Validate(input string, extra ...string) error {
	return tmpl.Validate(input, extra...)
}
