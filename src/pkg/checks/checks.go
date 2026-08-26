// SPDX-License-Identifier: Apache-2.0
// Package checks defines machine-verifiable assertions used by scenario
// format v2 (and, later, incident detection and challenge grading). A Check
// is declarative YAML; the Runner executes it and reports a Result. Keeping
// the type here (not in package scenario) lets scenario depend on checks
// without an import cycle once other engines reuse the same primitive.
package checks

import (
	"fmt"
	"sort"
	"strings"
)

// Check types.
const (
	TypeHTTP    = "http"
	TypeKubectl = "kubectl"
	TypePromQL  = "promql"
	TypeScript  = "script"
)

// Valid comparison operators for kubectl (with jsonpath) and promql checks.
var validOperators = map[string]bool{
	"==": true, "!=": true, "<": true, "<=": true, ">": true, ">=": true,
	"contains": true,
}

// Check is a single verifiable assertion declared in scenario.yaml.
type Check struct {
	Name string `yaml:"name" json:"name"`
	Type string `yaml:"type" json:"type"` // http | kubectl | promql | script

	// http
	URL          string `yaml:"url,omitempty" json:"url,omitempty"`
	ExpectStatus int    `yaml:"expectStatus,omitempty" json:"expectStatus,omitempty"`
	BodyContains string `yaml:"bodyContains,omitempty" json:"bodyContains,omitempty"`

	// kubectl
	Resource  string `yaml:"resource,omitempty" json:"resource,omitempty"` // e.g. statefulset/loki or prometheusrules
	Namespace string `yaml:"namespace,omitempty" json:"namespace,omitempty"`
	JSONPath  string `yaml:"jsonpath,omitempty" json:"jsonpath,omitempty"` // e.g. {.status.readyReplicas}

	// promql
	Query string `yaml:"query,omitempty" json:"query,omitempty"`

	// comparison (kubectl with jsonpath, promql)
	Operator string `yaml:"operator,omitempty" json:"operator,omitempty"` // == != < <= > >= contains
	Value    string `yaml:"value,omitempty" json:"value,omitempty"`

	// script (path relative to the scenario directory; exit 0 = pass)
	Script string `yaml:"script,omitempty" json:"script,omitempty"`

	// TimeoutSeconds overrides the runner's default per-check timeout.
	TimeoutSeconds int `yaml:"timeoutSeconds,omitempty" json:"timeoutSeconds,omitempty"`
}

// fieldOwners maps each type-specific field to the check types allowed to set
// it, so authoring mistakes (an http check with a promql query, …) fail
// validation instead of being silently ignored.
func (c *Check) setFields() map[string][]string {
	owners := map[string]struct {
		set   bool
		types []string
	}{
		"url":          {c.URL != "", []string{TypeHTTP}},
		"expectStatus": {c.ExpectStatus != 0, []string{TypeHTTP}},
		"bodyContains": {c.BodyContains != "", []string{TypeHTTP}},
		"resource":     {c.Resource != "", []string{TypeKubectl}},
		"namespace":    {c.Namespace != "", []string{TypeKubectl}},
		"jsonpath":     {c.JSONPath != "", []string{TypeKubectl}},
		"query":        {c.Query != "", []string{TypePromQL}},
		"operator":     {c.Operator != "", []string{TypeKubectl, TypePromQL}},
		"value":        {c.Value != "", []string{TypeKubectl, TypePromQL}},
		"script":       {c.Script != "", []string{TypeScript}},
	}
	result := make(map[string][]string)
	for field, o := range owners {
		if o.set {
			result[field] = o.types
		}
	}
	return result
}

// Validate reports all schema problems with this check, naming the offending
// fields so authors can fix everything in one pass.
func (c *Check) Validate() error {
	var errs []string
	add := func(format string, args ...interface{}) {
		errs = append(errs, fmt.Sprintf(format, args...))
	}

	if strings.TrimSpace(c.Name) == "" {
		add("name is required")
	}

	switch c.Type {
	case TypeHTTP:
		if c.URL == "" {
			add("http check requires url")
		}
		if c.ExpectStatus != 0 && (c.ExpectStatus < 100 || c.ExpectStatus > 599) {
			add("expectStatus %d is not a valid HTTP status code", c.ExpectStatus)
		}
	case TypeKubectl:
		if c.Resource == "" {
			add("kubectl check requires resource (e.g. statefulset/loki)")
		}
		if c.JSONPath != "" {
			if c.Operator == "" || c.Value == "" {
				add("kubectl check with jsonpath requires operator and value")
			}
		} else if c.Operator != "" || c.Value != "" {
			add("kubectl check has operator/value but no jsonpath to compare against")
		}
	case TypePromQL:
		if c.Query == "" {
			add("promql check requires query")
		}
		if c.Operator == "" || c.Value == "" {
			add("promql check requires operator and value")
		}
	case TypeScript:
		if c.Script == "" {
			add("script check requires script (path relative to the scenario directory)")
		}
	case "":
		add("type is required (http | kubectl | promql | script)")
	default:
		add("unknown check type %q (expected http | kubectl | promql | script)", c.Type)
	}

	if c.Operator != "" && !validOperators[c.Operator] {
		add("unknown operator %q (expected == != < <= > >= contains)", c.Operator)
	}
	if c.TimeoutSeconds < 0 {
		add("timeoutSeconds must be >= 0")
	}

	// Reject fields that don't belong to this check's type.
	if validKnownType(c.Type) {
		var misplaced []string
		for field, types := range c.setFields() {
			ok := false
			for _, t := range types {
				if t == c.Type {
					ok = true
					break
				}
			}
			if !ok {
				misplaced = append(misplaced, field)
			}
		}
		sort.Strings(misplaced)
		if len(misplaced) > 0 {
			add("fields not valid for type %q: %s", c.Type, strings.Join(misplaced, ", "))
		}
	}

	if len(errs) > 0 {
		name := c.Name
		if name == "" {
			name = "(unnamed)"
		}
		return fmt.Errorf("check %s: %s", name, strings.Join(errs, "; "))
	}
	return nil
}

func validKnownType(t string) bool {
	switch t {
	case TypeHTTP, TypeKubectl, TypePromQL, TypeScript:
		return true
	}
	return false
}

// Result is the outcome of running one check.
type Result struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Pass bool   `json:"pass"`
	// Got and Want carry the observed and expected values so a failing check
	// reports *why* it failed, not just FAIL (W2-T06). Explanation is a single
	// human-readable sentence composed from them for direct display.
	Got         string `json:"got,omitempty"`
	Want        string `json:"want,omitempty"`
	Explanation string `json:"explanation,omitempty"`
	Error       string `json:"error,omitempty"`
	// Attempts is the number of times the check was evaluated. It is 1 for a
	// single Run and >=1 for Eventually, where the check is polled until it
	// passes or a deadline elapses.
	Attempts   int   `json:"attempts,omitempty"`
	DurationMS int64 `json:"durationMs"`
}

// explain composes a single human-readable sentence describing the outcome,
// so callers (CLI, UI, logs) can surface *why* a check passed or failed
// without re-deriving it from Got/Want/Error.
func (r Result) explain() string {
	name := r.Name
	if name == "" {
		name = "check"
	}
	switch {
	case r.Error != "":
		return fmt.Sprintf("%s errored: %s", name, r.Error)
	case r.Pass:
		if r.Got != "" {
			return fmt.Sprintf("%s passed: observed %s", name, r.Got)
		}
		return fmt.Sprintf("%s passed", name)
	default:
		switch {
		case r.Want != "" && r.Got != "":
			return fmt.Sprintf("%s failed: expected %s but observed %s", name, r.Want, r.Got)
		case r.Want != "":
			return fmt.Sprintf("%s failed: expected %s", name, r.Want)
		case r.Got != "":
			return fmt.Sprintf("%s failed: observed %s", name, r.Got)
		default:
			return fmt.Sprintf("%s failed", name)
		}
	}
}

// AllPass reports whether every result passed. An empty slice reports false:
// "nothing was verified" must never read as success.
func AllPass(results []Result) bool {
	if len(results) == 0 {
		return false
	}
	for _, r := range results {
		if !r.Pass {
			return false
		}
	}
	return true
}
