// SPDX-License-Identifier: Apache-2.0

package catalog

import (
	"time"

	"github.com/sagar2395/snowopslabs/internal/tmpl"
)

// TemplateContext is the set of variables content templates may reference. It
// is defined in internal/tmpl, which the run-time engines also use.
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
		SinceActivation:     tmpl.Since(time.Time{}, time.Now()),
	}
}

// Validate reports a reference to an unknown labctl template variable. Other
// systems' template syntax in the same string (Loki, Prometheus, Grafana,
// Helm) is ignored, as it is at run time. See internal/tmpl.
func Validate(input string, extra ...string) error {
	return tmpl.Validate(input, extra...)
}
