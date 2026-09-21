// SPDX-License-Identifier: Apache-2.0

// Package tmpl defines the variables content templates may use, such as
// {{.DomainSuffix}}, and expands them: strictly when content is validated,
// leniently at run time. The catalog validator, the scenario engine and the
// incident engine all use it, so they agree on the same set of variables.
package tmpl

import (
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Context holds the variables content templates may reference. It is a
// struct, not a map, so validation rejects a misspelt name such as
// {{.DomainSufix}} instead of expanding it to "".
//
// Keep the fields flat: Expand only matches a single name, so a nested
// {{.Workload.Name}} would be left unexpanded.
type Context struct {
	// DomainSuffix is the ingress domain suffix, e.g. "k3d.local".
	DomainSuffix string
	// MonitoringNamespace is where the monitoring stack lives, e.g. "monitoring".
	MonitoringNamespace string
	// ProjectRoot is the absolute path to the repository/content root.
	ProjectRoot string
	// LokiRetentionPeriod is Loki's configured retention, e.g. "72h".
	LokiRetentionPeriod string
	// IngressClass is the ingress class name, e.g. "traefik".
	IngressClass string

	// The Workload fields describe the application a scenario or fault is bound
	// to. Content names these instead of a literal app, so the same scenario can
	// run against go-api, another built-in app, or one the user brings.

	// WorkloadName is the bound app's name, e.g. "go-api".
	WorkloadName string
	// WorkloadNamespace is the namespace it is deployed into.
	WorkloadNamespace string
	// WorkloadService is its in-cluster host, e.g. "go-api.go-api.svc.cluster.local".
	WorkloadService string
	// WorkloadPort is the port serving HTTP, e.g. "8080".
	WorkloadPort string
	// WorkloadMetric is the request-duration histogram it exposes, e.g.
	// "http_server_request_duration_seconds". Named rather than assumed, because
	// each language's instrumentation library picks its own.
	WorkloadMetric string

	// SinceActivation is how long this run has been going — since the scenario
	// was activated or the fault injected — as a PromQL range such as "95m". A
	// fixed window forgets work done early; an open one counts a previous run's.
	SinceActivation string
}

// Since renders the time from start to now as a PromQL range, rounded up to
// whole minutes so start falls inside it. With nothing active there is no run
// to window, and the smallest valid range stands in for it.
func Since(start, now time.Time) string {
	if start.IsZero() || !now.After(start) {
		return "1m"
	}
	return fmt.Sprintf("%dm", int(math.Ceil(now.Sub(start).Minutes())))
}

// Vars returns the context as the name-to-value map Expand substitutes from.
// Every exported field of Context must appear here; TestVarsCoversEveryField
// fails if one is missing.
func (c Context) Vars() map[string]string {
	return map[string]string{
		"DomainSuffix":        c.DomainSuffix,
		"MonitoringNamespace": c.MonitoringNamespace,
		"ProjectRoot":         c.ProjectRoot,
		"LokiRetentionPeriod": c.LokiRetentionPeriod,
		"IngressClass":        c.IngressClass,
		"WorkloadName":        c.WorkloadName,
		"WorkloadNamespace":   c.WorkloadNamespace,
		"WorkloadService":     c.WorkloadService,
		"WorkloadPort":        c.WorkloadPort,
		"WorkloadMetric":      c.WorkloadMetric,
		"SinceActivation":     c.SinceActivation,
	}
}

// FieldNames lists Context's exported fields, i.e. every legal variable name.
func FieldNames() []string {
	t := reflect.TypeFor[Context]()
	out := make([]string, 0, t.NumField())
	for i := range t.NumField() {
		out = append(out, t.Field(i).Name)
	}
	return out
}

// varRef matches labctl's own placeholders: one dotted name, such as
// {{.DomainSuffix}} or {{ .MonitoringNamespace }}. It is kept narrow so it
// leaves alone the other template syntaxes content files contain: Prometheus
// annotations ({{ $value }}), Grafana legends ({{namespace}}) and Helm
// expressions ({{ index .data "x" | base64decode }}).
var varRef = regexp.MustCompile(`{{\s*\.(\w+)\s*}}`)

// Validate reports authoring mistakes in a templated content string.
//
// It matches placeholders the same way Expand does. Content files also hold
// other systems' templates, such as a Loki line_format ({{.method}}), so only a
// name starting with an upper-case letter is treated as one of labctl's
// variables and must be known; anything else is ignored.
//
// extra lists additional legal names, such as a scenario's own parameters.
func Validate(input string, extra ...string) error {
	if !strings.Contains(input, "{{") {
		return nil
	}
	known := Context{}.Vars()
	for _, name := range extra {
		known[name] = ""
	}
	for _, m := range varRef.FindAllStringSubmatch(input, -1) {
		name := m[1]
		r, _ := utf8.DecodeRuneInString(name)
		if !unicode.IsUpper(r) {
			continue // another system's templating
		}
		if _, ok := known[name]; !ok {
			legal := FieldNames()
			legal = append(legal, extra...)
			return fmt.Errorf("unknown template variable %q in %q (known: %s)",
				"{{."+name+"}}", input, strings.Join(legal, ", "))
		}
	}
	return nil
}

// Expand replaces the {{.Var}} placeholders it knows in input and leaves every
// other placeholder unchanged for the tool that reads the document next. A name
// in extra never overrides a built-in variable of the same name.
func Expand(input string, ctx Context, extra map[string]string) string {
	if input == "" || !strings.Contains(input, "{{") {
		return input
	}
	data := ctx.Vars()
	for k, v := range extra {
		if _, taken := data[k]; !taken {
			data[k] = v
		}
	}
	return varRef.ReplaceAllStringFunc(input, func(match string) string {
		if v, ok := data[varRef.FindStringSubmatch(match)[1]]; ok {
			return v
		}
		return match // unknown {{.Var}} — leave it for whoever else consumes it
	})
}

// IsTemplated reports whether input contains a placeholder for Expand to fill.
// Callers use it to tell a fixed value, such as "go-api", from one that follows
// the workload binding, such as "{{.WorkloadName}}".
func IsTemplated(input string) bool { return strings.Contains(input, "{{") }
