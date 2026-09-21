// SPDX-License-Identifier: Apache-2.0
package catalog

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

var (
	// A query without "by" keeps every label of the series it selects, which
	// cannot be known from the text, so only grouped queries are judged.
	byClause       = regexp.MustCompile(`\bby\s*\(`)
	groupingClause = regexp.MustCompile(`\b(?:by|group_left|group_right)\s*\(([^)]*)\)`)
	legendLabel    = regexp.MustCompile(`\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}`)
)

// checkDashboards lints the Grafana dashboards each scenario installs. A chart
// nobody can read is as broken as one that errors, and neither fails at install.
func (c *Catalog) checkDashboards() {
	for _, s := range c.scenarios {
		for _, comp := range s.AllComponents() {
			if comp.Type != "grafana-dashboard" || comp.Path == "" {
				continue
			}
			files, _ := filepath.Glob(filepath.Join(s.Dir, filepath.FromSlash(comp.Path), "*.json"))
			for _, file := range files {
				data, err := os.ReadFile(file) //nolint:gosec // a dashboard inside the content root
				if err == nil {
					var findings []string
					findings, err = lintDashboard(data)
					for _, f := range findings {
						c.problems = append(c.problems, Problem{Kind: KindScenario, Name: s.Name, File: file, Message: f})
					}
				}
				if err != nil {
					c.problems = append(c.problems, Problem{Kind: KindScenario, Name: s.Name, File: file, Message: err.Error()})
				}
			}
		}
	}
}

type dashboardPanel struct {
	Title   string           `json:"title"`
	Type    string           `json:"type"`
	Panels  []dashboardPanel `json:"panels"`
	Targets []struct {
		Expr         string `json:"expr"`
		LegendFormat string `json:"legendFormat"`
	} `json:"targets"`
}

// lintDashboard reports series a reader cannot tell apart: a query grouped by a
// label whose legend names none of the grouping labels, and a legend naming a
// label the query does not return, which renders empty. Tables have no legend.
func lintDashboard(data []byte) ([]string, error) {
	var d struct {
		Panels []dashboardPanel `json:"panels"`
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("dashboard is not valid JSON: %w", err)
	}

	var findings []string
	var walk func([]dashboardPanel)
	walk = func(panels []dashboardPanel) {
		for _, p := range panels {
			walk(p.Panels)
			if p.Type == "table" {
				continue
			}
			for _, t := range p.Targets {
				findings = append(findings, lintLegend(p.Title, t.Expr, t.LegendFormat)...)
			}
		}
	}
	walk(d.Panels)
	return findings, nil
}

func lintLegend(title, expr, legend string) []string {
	// Label rewriting functions and "without" change the result's labels in ways
	// a pattern cannot follow, so those queries are not judged.
	unknowable := strings.Contains(expr, "without") || strings.Contains(expr, "label_replace") || strings.Contains(expr, "label_join")
	if legend == "" || unknowable || !byClause.MatchString(expr) || collapsesToOneSeries(expr) {
		return nil
	}

	returned := map[string]bool{}
	var varying []string
	for _, m := range groupingClause.FindAllStringSubmatch(expr, -1) {
		for _, label := range strings.Split(m[1], ",") {
			label = strings.TrimSpace(label)
			if label == "" || label == "le" || returned[label] {
				continue
			}
			returned[label] = true
			// A label pinned by an equality matcher yields a single series.
			if !regexp.MustCompile(`\b` + regexp.QuoteMeta(label) + `\s*=\s*"`).MatchString(expr) {
				varying = append(varying, label)
			}
		}
	}

	var findings []string
	named := legendLabel.FindAllStringSubmatch(legend, -1)
	if len(varying) > 0 && len(named) == 0 {
		slices.Sort(varying)
		findings = append(findings, fmt.Sprintf(
			"panel %q: one series per %s, but every line is labelled %q — name the label in the legend, e.g. {{%s}}",
			title, strings.Join(varying, ", "), legend, varying[0]))
	}
	for _, m := range named {
		if !returned[m[1]] {
			findings = append(findings, fmt.Sprintf(
				"panel %q: legend names {{%s}}, but the query only returns %s, so it renders empty",
				title, m[1], strings.Join(slices.Sorted(maps.Keys(returned)), ", ")))
		}
	}
	return findings
}

var outerAggregation = regexp.MustCompile(`^\s*(?:sum|avg|min|max|count|group|stddev|stdvar)\s*\(`)

// collapsesToOneSeries reports an expression wrapped in an aggregation with no
// grouping of its own, such as sum(max by (pod) (x)) or vector(0): whatever the
// inner query groups by, the result is a single series.
func collapsesToOneSeries(expr string) bool {
	expr = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(expr), "or vector(0)"))
	loc := outerAggregation.FindStringIndex(expr)
	if loc == nil {
		return false
	}
	depth := 0
	for i := loc[1] - 1; i < len(expr); i++ {
		switch expr[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return strings.TrimSpace(expr[i+1:]) == ""
			}
		}
	}
	return false
}
