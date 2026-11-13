// SPDX-License-Identifier: Apache-2.0

package scenario

// These tests check every scenario.yaml in the repository: it must parse,
// pass schema validation, and pass the file checks in preflight.

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/snowops-oss/snowopslabs/internal/tmpl"
	"github.com/snowops-oss/snowopslabs/internal/workload"
)

// repoRoot walks up from the package directory until it finds the project
// root (identified by the scenarios/ directory plus the Makefile, so Go
// package dirs can't be mistaken for it).
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if dirExists(filepath.Join(dir, "scenarios")) && dirExists(filepath.Join(dir, "platform")) && fileExists(filepath.Join(dir, "Makefile")) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skip("repository root not found (running outside the repo checkout)")
		}
		dir = parent
	}
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

func TestRepoScenarios_AllLoadAndValidate(t *testing.T) {
	root := repoRoot(t)
	engine := NewEngine(root, "k3d.local", "") // empty profile: skip runtime gate

	// Any scenario that failed to load is a hard failure with its reason.
	for dir, err := range engine.LoadErrors() {
		t.Errorf("scenario %q failed to load: %v", dir, err)
	}

	// Every directory under scenarios/ must produce a loaded scenario -
	// catches scenarios silently skipped for any reason.
	entries, err := os.ReadDir(filepath.Join(root, "scenarios"))
	if err != nil {
		t.Fatalf("reading scenarios dir: %v", err)
	}
	loaded := map[string]bool{}
	for _, s := range engine.List() {
		loaded[filepath.Base(s.Dir)] = true
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), "_") {
			continue
		}
		if !loaded[e.Name()] {
			t.Errorf("scenarios/%s did not load (no scenario.yaml or it was skipped)", e.Name())
		}
	}
}

func TestRepoScenarios_PreflightPassesStatically(t *testing.T) {
	root := repoRoot(t)
	engine := NewEngine(root, "k3d.local", "")

	for _, s := range engine.List() {
		if err := engine.Preflight(s); err != nil {
			t.Errorf("scenario %q fails static preflight:\n%v", s.Name, err)
		}
	}
}

// TestRepoScenarios_DeclareWhatTheyUse checks that a scenario lists the
// platform components its own parts need, so `scenario up --deploy-prereqs`
// on a bare cluster installs them: a promql or http check reads through the
// ingress, a promql check needs Prometheus, and a dashboard is a ConfigMap in
// the monitoring namespace.
func TestRepoScenarios_DeclareWhatTheyUse(t *testing.T) {
	root := repoRoot(t)
	engine := NewEngine(root, "k3d.local", "")

	for _, s := range engine.List() {
		declared := func(prefix string) bool {
			return slices.ContainsFunc(s.Prerequisites.Platform, func(p string) bool {
				return p == prefix || strings.HasPrefix(p, prefix+"/")
			})
		}
		need := map[string]string{}
		for _, c := range s.Checks {
			switch c.Type {
			case "promql":
				need["ingress"] = "promql check " + c.Name
				need["monitoring/metrics"] = "promql check " + c.Name
			case "http":
				need["ingress"] = "http check " + c.Name
			}
		}
		for _, comp := range s.AllComponents() {
			if comp.Type == "grafana-dashboard" {
				need["monitoring"] = "grafana-dashboard component " + comp.Name
			}
		}
		for prereq, why := range need {
			if !declared(prereq) {
				t.Errorf("scenario %q has a %s but does not list %s under prerequisites.platform", s.Name, why, prereq)
			}
		}
	}
}

// serviceMonitorOn matches a values file that turns on its chart's
// ServiceMonitor.
var serviceMonitorOn = regexp.MustCompile(`(?m)^serviceMonitor:\s*\n\s+enabled:\s*true`)

// TestRepoScenarios_MetricsBeforeWhatTheyScrape checks that monitoring/metrics
// comes before any prerequisite whose chart ships a ServiceMonitor. Such a
// chart renders it only when the Prometheus CRDs already exist, so a component
// installed first is never scraped.
func TestRepoScenarios_MetricsBeforeWhatTheyScrape(t *testing.T) {
	root := repoRoot(t)
	engine := NewEngine(root, "k3d.local", "")

	for _, s := range engine.List() {
		metrics := slices.Index(s.Prerequisites.Platform, "monitoring/metrics")
		if metrics < 0 {
			continue
		}
		for _, pre := range s.Prerequisites.Platform[:metrics] {
			values, err := os.ReadFile(filepath.Join(root, "platform", pre, "values.yaml"))
			if err != nil {
				continue // a bare category, or a component with no values of its own
			}
			if serviceMonitorOn.Match(values) {
				t.Errorf("scenario %q lists %s before monitoring/metrics, so its ServiceMonitor is never created", s.Name, pre)
			}
		}
	}
}

// TestRepoScenarios_ObservabilityIsV2 checks that observability-sre, the
// reference v2 scenario, keeps its stages, objectives and checks.
func TestRepoScenarios_ObservabilityIsV2(t *testing.T) {
	root := repoRoot(t)
	engine := NewEngine(root, "k3d.local", "")

	s, err := engine.Get("observability-sre")
	if err != nil {
		t.Fatalf("observability-sre must exist: %v", err)
	}
	if len(s.Stages) < 1 {
		t.Errorf("observability-sre must declare at least 1 stage, has %d", len(s.Stages))
	}
	if len(s.Objectives) < 2 {
		t.Errorf("observability-sre must declare at least 2 objectives, has %d", len(s.Objectives))
	}
	if len(s.Checks) < 3 {
		t.Errorf("observability-sre must declare at least 3 checks, has %d", len(s.Checks))
	}
}

// unexpandedVar matches one of labctl's own template variables, or one of the
// given scenario parameters, left in rendered text. Other template syntaxes
// content carries (Loki line formats, Grafana legends) are not labctl's.
func unexpandedVar(params []string) *regexp.Regexp {
	names := append(tmpl.FieldNames(), params...)
	return regexp.MustCompile(`\{\{\s*\.(` + strings.Join(names, "|") + `)\b`)
}

// namesDefaultApp lists the scenarios whose text names the default app on
// purpose, with the reason.
var namesDefaultApp = map[string]string{
	"env-promotion": "points at go-api's releaseNote as an example of a visible code change",
}

// TestRepoScenarios_RenderForAnyWorkload renders everything a reader sees of
// each scenario for an app that is not the default one. A template variable
// left in the output, or the default app's name, means that text ignores the
// binding.
func TestRepoScenarios_RenderForAnyWorkload(t *testing.T) {
	root := repoRoot(t)
	engine := NewEngine(root, "lab.test", "k3d")
	bound := workload.Workload{Name: "zz-app", Namespace: "zz-ns", Port: "9999", Metric: "zz_metric"}

	scenarios := engine.List()
	if len(scenarios) == 0 {
		t.Fatal("no scenarios found in the repository")
	}
	for _, s := range scenarios {
		t.Run(s.Name, func(t *testing.T) {
			params := make([]string, 0, len(s.Parameters))
			for _, p := range s.Parameters {
				params = append(params, p.Name)
			}
			rendered, err := json.Marshal(engine.ResolvedForDisplay(s, engine.ParamDefaults(s), bound))
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			text := string(rendered)
			if m := unexpandedVar(params).FindString(text); m != "" {
				t.Errorf("an unexpanded template variable reaches the reader: %s", m)
			}
			if i := strings.Index(text, workload.DefaultApp); i >= 0 && namesDefaultApp[s.Name] == "" {
				t.Errorf("names %q although bound to %q: …%s…",
					workload.DefaultApp, bound.Name, text[max(0, i-60):min(len(text), i+40)])
			}
			if !strings.Contains(text, bound.Name) && strings.Contains(scenarioSource(t, s.Dir), "{{.WorkloadName}}") {
				t.Errorf("uses {{.WorkloadName}} but the rendered scenario never names %q", bound.Name)
			}
		})
	}
}

// TestRepoScenarios_BringTheExternalSecretsTheyCheck checks that a scenario
// which grades an ExternalSecret applies it itself, rendered for the bound app.
// A platform component is installed once, for whichever app is bound at that
// moment, so an ExternalSecret left to it is missing for every other app.
func TestRepoScenarios_BringTheExternalSecretsTheyCheck(t *testing.T) {
	root := repoRoot(t)
	engine := NewEngine(root, "lab.test", "k3d")
	engine.Workload = workload.Workload{Name: "zz-app", Namespace: "zz-ns", Port: "9999", Metric: "zz_metric"}

	graded := 0
	for _, s := range engine.List() {
		params := engine.ParamDefaults(s)
		var applied []manifestObject
		for _, comp := range s.AllComponents() {
			if comp.Type != "manifest" {
				continue
			}
			text, err := engine.RenderFile(s, comp.Path)
			if err != nil {
				t.Fatalf("scenario %q: render %s: %v", s.Name, comp.Path, err)
			}
			applied = append(applied, manifestObjects(t, text, engine.ResolveTemplateWithParams(comp.Namespace, params))...)
		}
		for _, c := range s.Checks {
			name, ok := strings.CutPrefix(engine.ResolveTemplateWithParams(c.Resource, params), "externalsecret/")
			if c.Type != "kubectl" || !ok {
				continue
			}
			graded++
			want := manifestObject{Kind: "ExternalSecret", Name: name, Namespace: engine.ResolveTemplateWithParams(c.Namespace, params)}
			if !slices.Contains(applied, want) {
				t.Errorf("scenario %q checks externalsecret/%s in %q, but none of its manifests applies it", s.Name, want.Name, want.Namespace)
			}
		}
	}
	if graded == 0 {
		t.Fatal("no scenario checks an ExternalSecret, so this test guards nothing")
	}
}

// manifestObject is what kubectl apply would create from one YAML document.
type manifestObject struct {
	Kind, Name, Namespace string
}

// manifestObjects lists the objects in a rendered manifest. A document with no
// namespace of its own lands in the component's namespace.
func manifestObjects(t *testing.T, text, componentNS string) []manifestObject {
	t.Helper()
	var out []manifestObject
	dec := yaml.NewDecoder(strings.NewReader(text))
	for {
		var doc struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name      string `yaml:"name"`
				Namespace string `yaml:"namespace"`
			} `yaml:"metadata"`
		}
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatalf("parse manifest: %v", err)
		}
		ns := doc.Metadata.Namespace
		if ns == "" {
			ns = componentNS
		}
		out = append(out, manifestObject{Kind: doc.Kind, Name: doc.Metadata.Name, Namespace: ns})
	}
}

// scenarioSource returns scenario.yaml as written.
func scenarioSource(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "scenario.yaml"))
	if err != nil {
		t.Fatalf("read scenario.yaml: %v", err)
	}
	return string(data)
}
