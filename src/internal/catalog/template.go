// SPDX-License-Identifier: Apache-2.0
package catalog

import (
	"fmt"
	"strings"
	"text/template"
)

// TemplateContext is the typed set of variables content templates may reference.
// It is deliberately a struct, not a map: text/template rejects a reference to a
// field that does not exist, so a typo like {{.DomainSufix}} fails loudly at
// validation time instead of silently resolving to empty.
type TemplateContext struct {
	// DomainSuffix is the ingress domain suffix, e.g. "k3d.local".
	DomainSuffix string
	// MonitoringNamespace is where the monitoring stack lives, e.g. "monitoring".
	MonitoringNamespace string
	// ProjectRoot is the absolute path to the repository/content root.
	ProjectRoot string
	// LokiRetentionPeriod is Loki's configured retention, e.g. "72h".
	LokiRetentionPeriod string
}

// DefaultTemplateContext returns a context populated with the standard defaults
// used when a caller only wants to validate that templates are well-formed and
// reference known keys (values need not be the deployment's real ones).
func DefaultTemplateContext(projectRoot string) TemplateContext {
	return TemplateContext{
		DomainSuffix:        "k3d.local",
		MonitoringNamespace: "monitoring",
		ProjectRoot:         projectRoot,
		LokiRetentionPeriod: "72h",
	}
}

// Resolve expands the Go-template variables in input against ctx. Input without
// "{{" is returned unchanged. Referencing an unknown key — or any malformed
// template — is an error naming the offending template, so authoring mistakes
// surface at validation instead of producing a broken URL or namespace at run
// time.
func Resolve(input string, ctx TemplateContext) (string, error) {
	if !strings.Contains(input, "{{") {
		return input, nil
	}
	tmpl, err := template.New("content").Option("missingkey=error").Parse(input)
	if err != nil {
		return "", fmt.Errorf("malformed template %q: %w", input, err)
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, ctx); err != nil {
		return "", fmt.Errorf("template %q: %w", input, err)
	}
	return buf.String(), nil
}
