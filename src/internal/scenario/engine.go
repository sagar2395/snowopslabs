// SPDX-License-Identifier: Apache-2.0
package scenario

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/sagar2395/snowopslabs/pkg/checks"
	"github.com/sagar2395/snowopslabs/pkg/extension"
	schema "github.com/sagar2395/snowopslabs/pkg/scenario"
)

// CommandExecutor is the slice of *executor.Executor that the scenario engine
// needs to run helm/kubectl and scenario scripts. Defining it here (a
// consumer-side interface) lets tests substitute a recorder and assert the exact
// commands the engine would run — namespaces, flags, ordering — without a real
// cluster. *executor.Executor satisfies it, so production callers are unchanged.
type CommandExecutor interface {
	RunCommandStreamed(actionLabel, name string, args ...string) (string, error)
	RunScriptStreamed(actionLabel, scriptPath string, args ...string) (string, error)
}

// ErrAlreadyActive is returned by Up when the scenario is already active.
// Callers should treat this as a no-op, not a failure.
var ErrAlreadyActive = errors.New("scenario already active")

// ErrNoChecks is returned by Verify when the scenario declares no checks.
var ErrNoChecks = errors.New("scenario defines no checks")

// The scenario schema types and their validation live in the public SDK
// package pkg/scenario. These aliases keep the engine's internal references
// stable while the canonical definitions live in the SDK (RFC 0001).
type (
	Scenario       = schema.Scenario
	Stage          = schema.Stage
	Component      = schema.Component
	Prerequisites  = schema.Prerequisites
	Explore        = schema.Explore
	ExploreURL     = schema.ExploreURL
	ExploreCommand = schema.ExploreCommand
)

// Engine discovers, loads, and manages scenarios.
type Engine struct {
	ProjectRoot         string
	DomainSuffix        string
	Profile             string // active runtime profile (k3d|kind|incluster), used for preflight
	MonitoringNamespace string // namespace for monitoring/logging/tracing (default: "monitoring")
	IngressClass        string // ingress class for scenario Ingress manifests (default: "traefik")

	// Extension seam (ADR-0008). Defaults to the open no-op implementation, so
	// the engine behaves identically unless a build injects custom hooks.
	Hooks extension.Hooks

	scenarios  map[string]*Scenario
	loadErrors map[string]error // scenario dir name → why it failed to load
	stateDir   string
}

// NewEngine creates a scenario engine by scanning the scenarios/ directory.
func NewEngine(projectRoot, domainSuffix, profile string, monitoringNamespace ...string) *Engine {
	ns := "monitoring"
	if len(monitoringNamespace) > 0 && monitoringNamespace[0] != "" {
		ns = monitoringNamespace[0]
	}
	e := &Engine{
		ProjectRoot:         projectRoot,
		DomainSuffix:        domainSuffix,
		Profile:             profile,
		MonitoringNamespace: ns,
		Hooks:               extension.DefaultHooks(),
		scenarios:           make(map[string]*Scenario),
		loadErrors:          make(map[string]error),
		stateDir:            filepath.Join(projectRoot, ".labctl", "scenarios"),
	}
	e.scan()
	return e
}

// List returns all discovered scenarios.
func (e *Engine) List() []*Scenario {
	var result []*Scenario
	for _, s := range e.scenarios {
		s.Active = e.isActive(s.Name)
		result = append(result, s)
	}
	return result
}

// Get returns a scenario by name.
func (e *Engine) Get(name string) (*Scenario, error) {
	s, ok := e.scenarios[name]
	if !ok {
		return nil, fmt.Errorf("scenario %q not found", name)
	}
	s.Active = e.isActive(name)
	return s, nil
}

// Preflight validates a scenario before activation: checks runtime compatibility,
// prerequisite directory existence, and component asset file existence.
// It returns a combined error listing all failures so the user can fix them all at once.
func (e *Engine) Preflight(s *Scenario) error {
	var errs []string

	// 1. Runtime compatibility — only checked when the scenario restricts runtimes.
	if len(s.Runtimes) > 0 && e.Profile != "" {
		ok := false
		for _, r := range s.Runtimes {
			if r == e.Profile {
				ok = true
				break
			}
		}
		if !ok {
			errs = append(errs, fmt.Sprintf(
				"active profile %q is not in supported runtimes %v", e.Profile, s.Runtimes))
		}
	}

	// 2. Prerequisite apps — check apps/<name>/app.env exists.
	for _, app := range s.Prerequisites.Apps {
		appEnv := filepath.Join(e.ProjectRoot, "apps", app, "app.env")
		if _, err := os.Stat(appEnv); err != nil {
			errs = append(errs, fmt.Sprintf(
				"prerequisite app %q not found — run 'labctl app build %s && labctl app deploy %s' (expected %s)",
				app, app, app, appEnv))
		}
	}

	// 3. Prerequisite platform components — check platform/<category>/ directory exists.
	for _, p := range s.Prerequisites.Platform {
		platformDir := filepath.Join(e.ProjectRoot, "platform", p)
		if _, err := os.Stat(platformDir); err != nil {
			errs = append(errs, fmt.Sprintf(
				"prerequisite platform %q not found — run 'labctl platform up' (expected %s)",
				p, platformDir))
		}
	}

	// 4. Component asset files.
	for _, comp := range s.AllComponents() {
		if comp.ValuesFile != "" {
			p := filepath.Join(s.Dir, comp.ValuesFile)
			if _, err := os.Stat(p); err != nil {
				errs = append(errs, fmt.Sprintf("component %q: valuesFile %q not found", comp.Name, p))
			}
		}
		if comp.Path != "" && (comp.Type == "manifest" || comp.Type == "grafana-dashboard") {
			p := filepath.Join(s.Dir, comp.Path)
			if _, err := os.Stat(p); err != nil {
				errs = append(errs, fmt.Sprintf("component %q: path %q not found", comp.Name, p))
			}
		}
		if comp.Script != "" {
			p := filepath.Join(s.Dir, comp.Script)
			if _, err := os.Stat(p); err != nil {
				errs = append(errs, fmt.Sprintf("component %q: script %q not found", comp.Name, p))
			}
		}
	}

	// 5. Check script files.
	for _, c := range s.Checks {
		if c.Type == checks.TypeScript && c.Script != "" && !filepath.IsAbs(c.Script) {
			p := filepath.Join(s.Dir, c.Script)
			if _, err := os.Stat(p); err != nil {
				errs = append(errs, fmt.Sprintf("check %q: script %q not found", c.Name, p))
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("preflight failed for scenario %q:\n  - %s", s.Name, strings.Join(errs, "\n  - "))
	}
	return nil
}

// Up activates a scenario by installing all its components. When force is true
// an already-active scenario is reinstalled (components are helm upgrade
// --install / kubectl apply, so this converges) instead of returning
// ErrAlreadyActive.
func (e *Engine) Up(name string, exec CommandExecutor, force bool) error {
	s, err := e.Get(name)
	if err != nil {
		return err
	}

	if e.isActive(name) && !force {
		return fmt.Errorf("%w: %s", ErrAlreadyActive, name)
	}

	if err := e.Preflight(s); err != nil {
		return err
	}

	fmt.Printf("Activating scenario: %s\n", s.DisplayName)
	fmt.Printf("  %s\n\n", s.Description)

	if len(s.Objectives) > 0 {
		fmt.Println("Objectives:")
		for _, o := range s.Objectives {
			fmt.Printf("  - %s\n", o)
		}
		fmt.Println()
	}

	ctx := context.Background()
	total := len(s.AllComponents())
	i := 0
	for _, st := range s.StagesOrDefault() {
		if st.Name != "" {
			fmt.Printf("=== Stage: %s ===\n", st.Name)
			if st.Description != "" {
				fmt.Printf("    %s\n", st.Description)
			}
		}
		ev := extension.Event{Scenario: s.Name, Stage: st.Name}
		if err := e.hooks().PreStage(ctx, ev); err != nil {
			return fmt.Errorf("pre-stage hook (%s): %w", st.Name, err)
		}
		for _, comp := range st.Components {
			i++
			fmt.Printf("[%d/%d] Installing %s (%s)...\n", i, total, comp.Name, comp.Type)
			if err := e.installComponent(s, &comp, exec); err != nil {
				return fmt.Errorf("installing component %s: %w", comp.Name, err)
			}
		}
		if err := e.hooks().PostStage(ctx, ev); err != nil {
			return fmt.Errorf("post-stage hook (%s): %w", st.Name, err)
		}
	}

	// Mark as active
	if err := e.markActive(name); err != nil {
		return fmt.Errorf("marking scenario active: %w", err)
	}

	if len(s.Checks) > 0 {
		fmt.Printf("\nThis scenario has %d verifiable checks. Run: labctl scenario verify %s\n", len(s.Checks), s.Name)
		fmt.Printf("Pods may still be starting — add --watch to wait for them:\n  labctl scenario verify %s --watch\n", s.Name)
	}

	// Print explore hints
	e.printExploreHints(s)

	return nil
}

// Verify runs the scenario's checks (template-resolved) through the supplied
// runner and returns one result per check. It does not require the scenario
// to be active — verifying an inactive scenario is how you prove it is down.
func (e *Engine) Verify(ctx context.Context, name string, runner *checks.Runner) ([]checks.Result, error) {
	s, err := e.Get(name)
	if err != nil {
		return nil, err
	}
	if len(s.Checks) == 0 {
		return nil, fmt.Errorf("%w: %s (add a checks block — see docs/scenarios.md)", ErrNoChecks, name)
	}

	runner.ScriptDir = s.Dir
	resolved := make([]checks.Check, len(s.Checks))
	for i, c := range s.Checks {
		resolved[i] = e.resolveCheck(c)
	}

	// Check lifecycle hooks (task 070): no-op in OSS. A Pre-check hook returning
	// an error aborts verification, letting premium policy gate checks.
	for _, c := range resolved {
		if err := e.hooks().PreCheck(ctx, extension.Event{Scenario: name, Check: c.Name}); err != nil {
			return nil, fmt.Errorf("pre-check hook (%s): %w", c.Name, err)
		}
	}
	results := runner.RunAll(ctx, resolved)
	for _, c := range resolved {
		if err := e.hooks().PostCheck(ctx, extension.Event{Scenario: name, Check: c.Name}); err != nil {
			return nil, fmt.Errorf("post-check hook (%s): %w", c.Name, err)
		}
	}
	return results, nil
}

// hooks returns the engine's lifecycle hooks, defaulting to the open no-op set
// when unset (e.g. an Engine built without NewEngine in tests).
func (e *Engine) hooks() extension.Hooks {
	if e.Hooks == nil {
		return extension.DefaultHooks()
	}
	return e.Hooks
}

// resolveCheck resolves template variables in a check's templatable fields.
func (e *Engine) resolveCheck(c checks.Check) checks.Check {
	c.URL = e.resolveTemplate(c.URL)
	c.BodyContains = e.resolveTemplate(c.BodyContains)
	c.Resource = e.resolveTemplate(c.Resource)
	c.Namespace = e.resolveTemplate(c.Namespace)
	c.JSONPath = e.resolveTemplate(c.JSONPath)
	c.Query = e.resolveTemplate(c.Query)
	c.Value = e.resolveTemplate(c.Value)
	return c
}

// Down deactivates a scenario by uninstalling all its components in reverse order.
func (e *Engine) Down(name string, exec CommandExecutor) error {
	s, err := e.Get(name)
	if err != nil {
		return err
	}

	if !e.isActive(name) {
		return fmt.Errorf("scenario %q is not active", name)
	}

	fmt.Printf("Deactivating scenario: %s\n\n", s.DisplayName)

	// Uninstall in reverse order (across all stages)
	all := s.AllComponents()
	for i := len(all) - 1; i >= 0; i-- {
		comp := all[i]
		fmt.Printf("[%d/%d] Uninstalling %s...\n", len(all)-i, len(all), comp.Name)
		if err := e.uninstallComponent(s, &comp, exec); err != nil {
			fmt.Printf("  Warning: %v\n", err)
		}
	}

	// Mark as inactive
	e.markInactive(name)
	fmt.Println("\nScenario deactivated.")
	return nil
}

// Status returns a summary of active scenarios.
func (e *Engine) Status() []ScenarioStatus {
	var result []ScenarioStatus
	for _, s := range e.scenarios {
		result = append(result, ScenarioStatus{
			Name:        s.Name,
			DisplayName: s.DisplayName,
			Description: s.Description,
			Category:    s.Category,
			Runtimes:    s.Runtimes,
			Active:      e.isActive(s.Name),
		})
	}
	return result
}

// ScenarioStatus is a lightweight status for listing scenarios.
type ScenarioStatus struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"displayName"`
	Description string   `json:"description,omitempty"`
	Category    string   `json:"category"`
	Runtimes    []string `json:"runtimes,omitempty"`
	Active      bool     `json:"active"`
}

func (e *Engine) scan() {
	// In-repo scenarios. External content roots (SNOWOPS_CONTENT_PATH) scan
	// after these and lose name collisions to them — see W2-T09.
	scenariosDir := filepath.Join(e.ProjectRoot, "scenarios")
	if entries, err := os.ReadDir(scenariosDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			e.loadInto(filepath.Join(scenariosDir, entry.Name()), "", entry.Name())
		}
	}
}

// loadInto loads one scenario dir, recording failures and collisions under
// the given key. One broken scenario must not hide the rest; LoadErrors()
// surfaces these (and the repo test fails CI on in-repo ones).
func (e *Engine) loadInto(dir, source, key string) {
	s, err := e.loadScenario(filepath.Join(dir, "scenario.yaml"))
	if err != nil {
		e.loadErrors[key] = err
		return
	}
	if existing, ok := e.scenarios[s.Name]; ok {
		from := "the repository"
		if existing.Source != "" {
			from = existing.Source
		}
		e.loadErrors[key] = fmt.Errorf("scenario name %q already provided by %s — skipped", s.Name, from)
		return
	}
	s.Dir = dir
	s.Source = source
	e.scenarios[s.Name] = s
}

// LoadErrors returns scenarios that were discovered but failed to load or
// validate, keyed by directory name.
func (e *Engine) LoadErrors() map[string]error {
	out := make(map[string]error, len(e.loadErrors))
	for k, v := range e.loadErrors {
		out[k] = v
	}
	return out
}

func (e *Engine) loadScenario(path string) (*Scenario, error) {
	return loadScenarioFile(path)
}

// loadScenarioFile parses and validates a scenario.yaml. It is package-level
// so pack validation (catalog.go) can use it without an Engine.
func loadScenarioFile(path string) (*Scenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var s Scenario
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	if err := s.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	return &s, nil
}

func (e *Engine) installComponent(s *Scenario, comp *Component, exec CommandExecutor) error {
	switch comp.Type {
	case "helm":
		return e.installHelm(s, comp, exec)
	case "manifest":
		return e.installManifest(s, comp, exec)
	case "grafana-dashboard":
		return e.installGrafanaDashboard(s, comp, exec)
	case "script":
		return e.runScript(s, comp, exec)
	default:
		return fmt.Errorf("unknown component type: %s", comp.Type)
	}
}

func (e *Engine) uninstallComponent(s *Scenario, comp *Component, exec CommandExecutor) error {
	switch comp.Type {
	case "helm":
		return e.uninstallHelm(comp, exec)
	case "manifest":
		return e.uninstallManifest(s, comp, exec)
	case "grafana-dashboard":
		// Grafana dashboards are removed when grafana restarts or scenario configmap is deleted
		return e.uninstallGrafanaDashboard(s, comp, exec)
	case "script":
		// Scripts don't have a clean uninstall; skip
		return nil
	default:
		return nil
	}
}

func (e *Engine) installHelm(s *Scenario, comp *Component, exec CommandExecutor) error {
	ns := e.componentNamespace(comp, "default")

	// Resolve template vars in chart/repo/version — a local-chart component uses
	// chart: {{.ProjectRoot}}/apps/.../helm, and without resolving it helm sees
	// the literal "{{.ProjectRoot}}" and fails with "repo {{.ProjectRoot}} not
	// found". Set values and the values file are already resolved below.
	chart := e.resolveTemplate(comp.Chart)
	repo := e.resolveTemplate(comp.Repo)
	version := e.resolveTemplate(comp.Version)

	// Add helm repo if specified. Errors here are non-fatal: the subsequent
	// `helm upgrade --install` surfaces a clear failure if the repo/chart is
	// genuinely unreachable, and repo add/update is idempotent.
	if repo != "" {
		repoName := strings.Split(chart, "/")[0]
		_, _ = exec.RunCommandStreamed("Helm repo add "+repoName, "helm", "repo", "add", repoName, repo, "--force-update")
		_, _ = exec.RunCommandStreamed("Helm repo update", "helm", "repo", "update")
	}

	args := []string{
		"upgrade", "--install", comp.Name, chart,
		"--namespace", ns, "--create-namespace",
		"--wait", "--timeout", "5m",
	}

	if version != "" {
		args = append(args, "--version", version)
	}

	if comp.ValuesFile != "" {
		valuesPath := filepath.Join(s.Dir, comp.ValuesFile)
		if _, err := os.Stat(valuesPath); err == nil {
			resolvedValuesPath, cleanup, err := e.resolveFileTemplate(valuesPath, "labctl-values-*.yaml")
			if err != nil {
				return err
			}
			defer cleanup()
			args = append(args, "-f", resolvedValuesPath)
		}
	}

	for k, v := range comp.Set {
		resolved := e.resolveTemplate(v)
		args = append(args, "--set", k+"="+resolved)
	}

	_, err := exec.RunCommandStreamed("Helm install "+comp.Name, "helm", args...)
	return err
}

func (e *Engine) uninstallHelm(comp *Component, exec CommandExecutor) error {
	// Resolve the namespace exactly as installHelm does. Using the raw
	// comp.Namespace here (e.g. the literal "{{.MonitoringNamespace}}") sent the
	// uninstall to the wrong namespace, so `helm uninstall` reported "release not
	// found" and the chart was left running while `scenario down` claimed success.
	ns := e.componentNamespace(comp, "default")
	_, err := exec.RunCommandStreamed("Helm uninstall "+comp.Name, "helm", "uninstall", comp.Name, "--namespace", ns)
	return err
}

// writeTempManifest writes content to a temp YAML file and returns its path and
// a cleanup func that removes it. It centralises the create/write/close/remove
// dance (and its error handling) shared by the manifest and dashboard installers.
func writeTempManifest(content string) (path string, cleanup func(), err error) {
	f, err := os.CreateTemp("", "labctl-manifest-*.yaml")
	if err != nil {
		return "", func() {}, err
	}
	name := f.Name()
	remove := func() { _ = os.Remove(name) }
	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		remove()
		return "", func() {}, err
	}
	if err := f.Close(); err != nil {
		remove()
		return "", func() {}, err
	}
	return name, remove, nil
}

func (e *Engine) installManifest(s *Scenario, comp *Component, exec CommandExecutor) error {
	manifestPath := filepath.Join(s.Dir, comp.Path)

	// Template the manifest
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("reading manifest %s: %w", manifestPath, err)
	}

	resolved := e.resolveTemplate(string(data))

	// Write to temp file and apply
	tmpPath, cleanup, err := writeTempManifest(resolved)
	if err != nil {
		return err
	}
	defer cleanup()

	args := []string{"apply", "-f", tmpPath}
	ns := e.componentNamespace(comp, "")
	if ns != "" && !manifestHasExplicitNamespace(resolved) {
		args = append(args, "--namespace", ns)
	}

	_, err = exec.RunCommandStreamed("Apply manifest "+comp.Name, "kubectl", args...)
	return err
}

func (e *Engine) uninstallManifest(s *Scenario, comp *Component, exec CommandExecutor) error {
	manifestPath := filepath.Join(s.Dir, comp.Path)

	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil // Already gone
	}

	resolved := e.resolveTemplate(string(data))

	tmpPath, cleanup, err := writeTempManifest(resolved)
	if err != nil {
		return err
	}
	defer cleanup()

	args := []string{"delete", "-f", tmpPath, "--ignore-not-found"}
	ns := e.componentNamespace(comp, "")
	if ns != "" && !manifestHasExplicitNamespace(resolved) {
		args = append(args, "--namespace", ns)
	}

	_, err = exec.RunCommandStreamed("Delete manifest "+comp.Name, "kubectl", args...)
	return err
}

func (e *Engine) installGrafanaDashboard(s *Scenario, comp *Component, exec CommandExecutor) error {
	dashDir := filepath.Join(s.Dir, comp.Path)
	entries, err := os.ReadDir(dashDir)
	if err != nil {
		return fmt.Errorf("reading dashboard dir: %w", err)
	}

	ns := comp.Namespace
	if ns == "" {
		ns = e.MonitoringNamespace
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dashDir, entry.Name()))
		if err != nil {
			continue
		}

		cmName := fmt.Sprintf("scenario-%s-%s", s.Name, strings.TrimSuffix(entry.Name(), ".json"))

		// Create ConfigMap with Grafana sidecar label
		cm := fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
  labels:
    grafana_dashboard: "1"
data:
  %s: |
%s`,
			cmName, ns, entry.Name(), indentJSON(string(data), "    "))

		tmpPath, cleanup, err := writeTempManifest(cm)
		if err != nil {
			continue
		}
		_, _ = exec.RunCommandStreamed("Apply dashboard "+entry.Name(), "kubectl", "apply", "-f", tmpPath)
		cleanup()
	}

	return nil
}

func (e *Engine) uninstallGrafanaDashboard(s *Scenario, comp *Component, exec CommandExecutor) error {
	dashDir := filepath.Join(s.Dir, comp.Path)
	entries, err := os.ReadDir(dashDir)
	if err != nil {
		return nil //nolint:nilerr // no dashboard dir means nothing to delete — uninstall is a no-op
	}

	ns := comp.Namespace
	if ns == "" {
		ns = e.MonitoringNamespace
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		cmName := fmt.Sprintf("scenario-%s-%s", s.Name, strings.TrimSuffix(entry.Name(), ".json"))
		// Best-effort delete; --ignore-not-found makes a missing ConfigMap a no-op.
		_, _ = exec.RunCommandStreamed("Delete dashboard "+entry.Name(), "kubectl", "delete", "configmap", cmName, "--namespace", ns, "--ignore-not-found")
	}

	return nil
}

func (e *Engine) runScript(s *Scenario, comp *Component, exec CommandExecutor) error {
	// Compute the path relative to the project root for RunScriptStreamed
	relPath, err := filepath.Rel(e.ProjectRoot, filepath.Join(s.Dir, comp.Script))
	if err != nil {
		// Fallback to absolute path
		relPath = filepath.Join(s.Dir, comp.Script)
	}
	_, err = exec.RunScriptStreamed("Run script "+comp.Name, relPath)
	return err
}

func (e *Engine) componentNamespace(comp *Component, defaultNamespace string) string {
	ns := strings.TrimSpace(comp.Namespace)
	if ns == "" {
		return defaultNamespace
	}
	return e.resolveTemplate(ns)
}

func (e *Engine) resolveFileTemplate(path, pattern string) (string, func(), error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", func() {}, fmt.Errorf("reading values file %s: %w", path, err)
	}
	resolved := e.resolveTemplate(string(data))
	if resolved == string(data) {
		return path, func() {}, nil
	}

	tmpFile, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", func() {}, err
	}
	if _, err := tmpFile.WriteString(resolved); err != nil {
		name := tmpFile.Name()
		_ = tmpFile.Close()
		_ = os.Remove(name)
		return "", func() {}, err
	}
	if err := tmpFile.Close(); err != nil {
		name := tmpFile.Name()
		_ = os.Remove(name)
		return "", func() {}, err
	}
	name := tmpFile.Name()
	return name, func() { _ = os.Remove(name) }, nil
}

// ResolveTemplate resolves Go template variables in a string (e.g., {{.DomainSuffix}}).
func (e *Engine) ResolveTemplate(input string) string {
	return e.resolveTemplate(input)
}

// labctlVar matches labctl's own template placeholders: a single dotted
// identifier like {{.DomainSuffix}} or {{ .MonitoringNamespace }}. It is
// deliberately narrow so it never touches the OTHER templating languages that
// legitimately share the file: Prometheus rule annotations ({{ $value }},
// {{ $labels.pod }}), Grafana legends ({{namespace}}), and Helm/sprig
// expressions ({{ index .data "x" | base64decode }}). Parsing the whole
// document as one Go template used to choke on those and silently return the
// input unrendered, so a manifest's {{.MonitoringNamespace}} reached kubectl
// verbatim and the apply failed.
var labctlVar = regexp.MustCompile(`{{\s*\.(\w+)\s*}}`)

func (e *Engine) resolveTemplate(input string) string {
	data := map[string]string{
		"DomainSuffix":        e.DomainSuffix,
		"ProjectRoot":         e.ProjectRoot,
		"MonitoringNamespace": e.MonitoringNamespace,
		"LokiRetentionPeriod": lokiRetentionPeriod(),
		"IngressClass":        ingressClassOr(e.IngressClass),
	}
	return labctlVar.ReplaceAllStringFunc(input, func(match string) string {
		key := labctlVar.FindStringSubmatch(match)[1]
		if v, ok := data[key]; ok {
			return v
		}
		return match // unknown {{.Var}} — leave it for whoever else consumes it
	})
}

// ingressClassOr falls back to traefik (the k3d default) when no class is set,
// so scenario Ingress manifests always resolve to a usable class.
func ingressClassOr(class string) string {
	if strings.TrimSpace(class) == "" {
		return "traefik"
	}
	return class
}

func lokiRetentionPeriod() string {
	hours := strings.TrimSpace(os.Getenv("LOKI_RETENTION_HOURS"))
	if hours == "" {
		hours = "168"
	}
	return hours + "h"
}

func (e *Engine) isActive(name string) bool {
	statePath := filepath.Join(e.stateDir, name+".active")
	_, err := os.Stat(statePath)
	return err == nil
}

func (e *Engine) markActive(name string) error {
	if err := os.MkdirAll(e.stateDir, 0755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(e.stateDir, name+".active"), []byte("active"), 0644)
}

func (e *Engine) markInactive(name string) {
	// Best-effort: a missing marker already means inactive.
	_ = os.Remove(filepath.Join(e.stateDir, name+".active"))
}

func (e *Engine) printExploreHints(s *Scenario) {
	if len(s.Explore.URLs) == 0 && len(s.Explore.Commands) == 0 && len(s.Explore.Tips) == 0 {
		return
	}

	fmt.Println("\n=== Explore This Scenario ===")

	if len(s.Explore.URLs) > 0 {
		fmt.Println("\nURLs:")
		for _, u := range s.Explore.URLs {
			resolved := e.resolveTemplate(u.URL)
			fmt.Printf("  %-30s %s\n", u.Label+":", resolved)
		}
	}

	if len(s.Explore.Commands) > 0 {
		fmt.Println("\nCommands to try:")
		for _, c := range s.Explore.Commands {
			resolved := e.resolveTemplate(c.Command)
			fmt.Printf("  %s:\n    %s\n", c.Label, resolved)
		}
	}

	if len(s.Explore.Tips) > 0 {
		fmt.Println("\nTips:")
		for _, t := range s.Explore.Tips {
			fmt.Printf("  - %s\n", e.resolveTemplate(t))
		}
	}

	fmt.Println()
}

func indentJSON(s, prefix string) string {
	var result strings.Builder
	for _, line := range strings.Split(s, "\n") {
		result.WriteString(prefix)
		result.WriteString(line)
		result.WriteString("\n")
	}
	return result.String()
}

func manifestHasExplicitNamespace(manifest string) bool {
	decoder := yaml.NewDecoder(strings.NewReader(manifest))

	for {
		var doc map[string]interface{}
		if err := decoder.Decode(&doc); err != nil {
			break
		}

		if len(doc) == 0 {
			continue
		}

		metadataRaw, ok := doc["metadata"]
		if !ok {
			continue
		}

		metadata, ok := metadataRaw.(map[string]interface{})
		if !ok {
			continue
		}

		nsRaw, ok := metadata["namespace"]
		if !ok {
			continue
		}

		ns, ok := nsRaw.(string)
		if ok && strings.TrimSpace(ns) != "" {
			return true
		}
	}

	return false
}
