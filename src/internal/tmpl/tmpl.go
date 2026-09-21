// SPDX-License-Identifier: Apache-2.0

// Package tmpl owns the variables content templates may reference, and the two
// ways they are expanded: strictly at validation time, leniently at run time.
//
// It exists because the set of variables used to be written out three times —
// once in the catalog validator, once in the scenario engine, once in the
// incident engine — and the copies drifted. {{.IngressClass}} resolved at run
// time but was rejected by the validator, and incidents never supported
// {{.MonitoringNamespace}} at all.
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

// Context is the typed set of variables content templates may reference.
// It is deliberately a struct, not a map: text/template rejects a reference to a
// field that does not exist, so a typo like {{.DomainSufix}} fails loudly at
// validation time instead of silently resolving to empty.
//
// Field names are flat by design. Expand's regex matches a single dotted
// identifier, so a nested {{.Workload.Name}} would pass through unresolved and
// reach kubectl verbatim — see the note on varRef.
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

// Vars renders the context as the map the lenient expander substitutes from.
// Every exported field of Context must appear here; TestVarsCoversEveryField
// enforces that by reflection, so adding a field without wiring it fails a test
// rather than silently resolving to nothing at run time.
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

// varRef matches labctl's own template placeholders: a single dotted identifier
// like {{.DomainSuffix}} or {{ .MonitoringNamespace }}. It is deliberately
// narrow so it never touches the OTHER templating languages that legitimately
// share the file: Prometheus rule annotations ({{ $value }}, {{ $labels.pod }}),
// Grafana legends ({{namespace}}), and Helm/sprig expressions
// ({{ index .data "x" | base64decode }}). Parsing the whole document as one Go
// template used to choke on those and silently return the input unrendered, so
// a manifest's {{.MonitoringNamespace}} reached kubectl verbatim and the apply
// failed.
var varRef = regexp.MustCompile(`{{\s*\.(\w+)\s*}}`)

// Validate reports authoring mistakes in a templated content string.
//
// It mirrors Expand's semantics rather than parsing the input as one Go
// template, because that is what actually happens at run time. A content file
// legitimately carries other systems' templating — a Loki line_format
// ({{.method}}), a Prometheus annotation ({{ $value }}), a Grafana legend — and
// parsing the whole string would reject those as unknown fields.
//
// The rule: labctl's own variables are always PascalCase, because they are
// exported fields of Context. So a {{.Name}} beginning with an upper-case letter
// is claiming to be one of ours and must resolve; anything else belongs to
// whichever engine consumes the document next and is left alone.
// extra names additional variables that are legal in this particular content —
// a scenario's own declared parameters, which are substituted alongside the
// built-ins at activation.
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

// Expand leniently substitutes {{.Var}} placeholders in input, leaving anything
// it does not recognise untouched for whichever other templating engine consumes
// the document next. extra is overlaid beneath the built-ins, so a scenario
// parameter can never shadow one.
//
// This is the run-time path. It is lenient because by then the document may hold
// Helm, Prometheus and Grafana syntax that is none of labctl's business.
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

// IsTemplated reports whether input carries a placeholder for Expand to fill.
//
// Callers use it to tell a value an author pinned from one an author delegated
// to the binding: "go-api" is a literal requirement, "{{.WorkloadName}}" is
// whatever app the lab is bound to and is not a requirement at all.
func IsTemplated(input string) bool { return strings.Contains(input, "{{") }
