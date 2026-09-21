// SPDX-License-Identifier: Apache-2.0

package catalog

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestLintDashboard(t *testing.T) {
	tests := []struct {
		name   string
		expr   string
		legend string
		kind   string
		want   string // substring of the single finding; "" means clean
	}{
		{name: "grouped series with a label in the legend", expr: `sum by (namespace) (up)`, legend: `{{namespace}} up`},
		{name: "grouped series sharing one legend", expr: `sum by (namespace) (up)`, legend: `available`, want: "one series per namespace"},
		{name: "postfix by clause", expr: `sum(rate(x[1m])) by (http_response_status_code)`, legend: `{{code}}`, want: "renders empty"},
		{name: "le is not a series label", expr: `histogram_quantile(0.99, sum by (le) (rate(x[5m])))`, legend: `p99`},
		{name: "a label pinned to one value", expr: `sum by (namespace) (up{namespace="go-api"})`, legend: `ready`},
		{name: "a regex match still varies", expr: `sum by (namespace) (up{namespace=~"env-.*"})`, legend: `ready`, want: "one series per namespace"},
		{name: "group_left adds the joined label", expr: `x * on (instance) group_left (nodename) node_uname_info`, legend: `{{nodename}}`},
		{name: "an outer aggregation collapses inner grouping", expr: `sum(max by (pod, container) (x)) or vector(0)`, legend: `cores`},
		{name: "an outer aggregation with its own grouping is judged", expr: `sum by (node) (max by (pod) (x))`, legend: `cores`, want: "one series per node"},
		{name: "a join without by keeps the selected labels", expr: `(time() - a) * on(pv) group_left() (b)`, legend: `volume {{pv}}`},
		{name: "ungrouped query is not judged", expr: `container_memory_working_set_bytes`, legend: `{{pod}}`},
		{name: "label_replace is not judged", expr: `label_replace(sum by (a) (x), "b", "$1", "a", "(.*)")`, legend: `{{b}}`},
		{name: "no legend lets Grafana name the series", expr: `sum by (pod) (x)`},
		{name: "tables have no legend", expr: `sum by (pod) (x)`, legend: `pods`, kind: "table"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kind := tt.kind
			if kind == "" {
				kind = "timeseries"
			}
			body := `{"panels":[{"type":"row","panels":[{"title":"p","type":"` + kind + `","targets":[{"expr":` +
				quoteJSON(tt.expr) + `,"legendFormat":` + quoteJSON(tt.legend) + `}]}]}]}`

			findings, err := lintDashboard([]byte(body))
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case tt.want == "" && len(findings) != 0:
				t.Errorf("findings = %v, want none", findings)
			case tt.want != "" && (len(findings) != 1 || !strings.Contains(findings[0], tt.want)):
				t.Errorf("findings = %v, want one containing %q", findings, tt.want)
			}
		})
	}
}

func TestLintDashboard_RejectsInvalidJSON(t *testing.T) {
	if _, err := lintDashboard([]byte("{not json")); err == nil {
		t.Fatal("expected an error for invalid JSON")
	}
}

func TestLoad_DashboardLegendProblemsAreReported(t *testing.T) {
	root := t.TempDir()
	scn := validScenario + "  - name: dash\n    type: grafana-dashboard\n    path: dashboards\n"
	write(t, root, "scenarios", "s1", "scenario.yaml", scn)
	writeAsset(t, root, "scenarios", "s1", "dashboards/d.json",
		`{"panels":[{"title":"Ready","type":"timeseries","targets":[{"expr":"sum by (namespace) (up)","legendFormat":"ready"}]}]}`)

	c, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	problems := c.Problems()
	if len(problems) != 1 || !strings.HasSuffix(problems[0].File, "d.json") {
		t.Fatalf("problems = %v, want one naming d.json", problems)
	}
}

// Platform dashboards ship as raw files in a ConfigMap, so nothing expands a
// labctl template in them, and they must be as readable as a scenario's.
func TestRepoPlatformDashboards(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(repoRoot(t), "platform", "*", "*", "provisioning", "dashboards", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no platform dashboards found (err %v)", err)
	}
	labctlTemplate := regexp.MustCompile(`\{\{\.[A-Z]\w*\}\}`)
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if m := labctlTemplate.FindString(string(data)); m != "" {
				t.Errorf("contains %s, which nothing resolves for a platform dashboard", m)
			}
			findings, err := lintDashboard(data)
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range findings {
				t.Error(f)
			}
		})
	}
}

func quoteJSON(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}
