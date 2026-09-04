// SPDX-License-Identifier: Apache-2.0
package scenario

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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
	Parameter      = schema.Parameter
	Snippet        = schema.Snippet
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

	// out is where Up/Down write their human-readable progress. It defaults to
	// os.Stdout (unchanged for the legacy path); the durable scenario service
	// points it at the run's transcript so activation output is recorded and
	// streamed through the run engine rather than racing on os.Stdout.
	out io.Writer

	// activationParams: raw user overrides for the next Up (set via
	// SetActivationParams, cleared after). resolvedParams: the effective values
	// (defaults overlaid with overrides) that resolveTemplate substitutes for
	// {{.ParamName}} during that Up. Both follow the SetOutput serialisation rule.
	activationParams map[string]string
	resolvedParams   map[string]string
}

// SetActivationParams stages parameter overrides for the next Up (nil clears).
// Like SetOutput, callers must serialise per engine.
func (e *Engine) SetActivationParams(params map[string]string) { e.activationParams = params }

// SetOutput redirects the progress output of Up/Down. Not safe to call while an
// activation is in flight on the same engine; callers serialise per engine.
func (e *Engine) SetOutput(w io.Writer) { e.out = w }

// output returns the configured progress writer, defaulting to os.Stdout.
func (e *Engine) output() io.Writer {
	if e.out != nil {
		return e.out
	}
	return os.Stdout
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

// List returns all discovered scenarios in catalog order.
func (e *Engine) List() []*Scenario {
	var result []*Scenario
	for _, s := range e.scenarios {
		s.Active = e.isActive(s.Name)
		result = append(result, s)
	}
	sort.Slice(result, func(i, j int) bool {
		return catalogLess(result[i].Category, result[i].DisplayName, result[i].Name,
			result[j].Category, result[j].DisplayName, result[j].Name)
	})
	return result
}

// catalogLess orders scenarios by category, then display name, then name. The
// engine stores scenarios in a map, so without an explicit order every call
// returns a different sequence and the UI reshuffles on each refresh.
func catalogLess(catA, dispA, nameA, catB, dispB, nameB string) bool {
	if catA != catB {
		return catA < catB
	}
	if dispA != dispB {
		return dispA < dispB
	}
	return nameA < nameB
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

	// Validate parameter overrides before installing anything, so a bad value
	// (out of range, or minReplicas > maxReplicas) fails up front.
	params, err := e.effectiveParams(s)
	if err != nil {
		return err
	}
	e.resolvedParams = params
	defer func() { e.resolvedParams = nil }()

	fmt.Fprintf(e.output(), "Activating scenario: %s\n", s.DisplayName)
	fmt.Fprintf(e.output(), "  %s\n\n", s.Description)

	if len(s.Objectives) > 0 {
		fmt.Fprintln(e.output(), "Objectives:")
		for _, o := range s.Objectives {
			fmt.Fprintf(e.output(), "  - %s\n", o)
		}
		fmt.Fprintln(e.output())
	}

	// Echo the effective parameter values (marking any the user overrode) so the
	// activation transcript records exactly what was applied.
	if len(s.Parameters) > 0 {
		fmt.Fprintln(e.output(), "Parameters:")
		for _, p := range s.Parameters {
			marker := ""
			if _, overridden := e.activationParams[p.Name]; overridden {
				marker = "  (overridden)"
			}
			fmt.Fprintf(e.output(), "  - %s = %s%s\n", p.Name, params[p.Name], marker)
		}
		fmt.Fprintln(e.output())
	}

	ctx := context.Background()
	total := len(s.AllComponents())
	i := 0
	for _, st := range s.StagesOrDefault() {
		if st.Name != "" {
			fmt.Fprintf(e.output(), "=== Stage: %s ===\n", st.Name)
			if st.Description != "" {
				fmt.Fprintf(e.output(), "    %s\n", st.Description)
			}
		}
		ev := extension.Event{Scenario: s.Name, Stage: st.Name}
		if err := e.hooks().PreStage(ctx, ev); err != nil {
			return fmt.Errorf("pre-stage hook (%s): %w", st.Name, err)
		}
		for _, comp := range st.Components {
			i++
			fmt.Fprintf(e.output(), "[%d/%d] Installing %s (%s)...\n", i, total, comp.Name, comp.Type)
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
		fmt.Fprintf(e.output(), "\nThis scenario has %d verifiable checks. Run: labctl scenario verify %s\n", len(s.Checks), s.Name)
		fmt.Fprintf(e.output(), "Pods may still be starting — add --watch to wait for them:\n  labctl scenario verify %s --watch\n", s.Name)
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
		return nil, fmt.Errorf("%w: %s (add a checks block — see docs/reference/scenario-schema.md)", ErrNoChecks, name)
	}

	runner.ScriptDir = s.Dir
	resolved := make([]checks.Check, len(s.Checks))
	for i, c := range s.Checks {
		resolved[i] = e.resolveCheck(c)
	}

	// Check lifecycle hooks: no-op in OSS. A Pre-check hook returning
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
// ResolveCheck expands template variables in a check's fields, so a caller
// outside this package grades against the same resolved check the engine runs.
func (e *Engine) ResolveCheck(c checks.Check) checks.Check {
	return e.resolveCheck(c)
}

func (e *Engine) resolveCheck(c checks.Check) checks.Check {
	c.URL = e.resolveTemplate(c.URL)
	c.BodyContains = e.resolveTemplate(c.BodyContains)
	c.Resource = e.resolveTemplate(c.Resource)
	c.Namespace = e.resolveTemplate(c.Namespace)
	c.JSONPath = e.resolveTemplate(c.JSONPath)
	c.Query = e.resolveTemplate(c.Query)
	c.Value = e.resolveTemplate(c.Value)
	// Remediation is shown to the user verbatim, so an unresolved
	// {{.MonitoringNamespace}} in it becomes a command they cannot run.
	c.Remediation = e.resolveTemplate(c.Remediation)
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

	fmt.Fprintf(e.output(), "Deactivating scenario: %s\n\n", s.DisplayName)

	// Uninstall in reverse order (across all stages)
	all := s.AllComponents()
	for i := len(all) - 1; i >= 0; i-- {
		comp := all[i]
		fmt.Fprintf(e.output(), "[%d/%d] Uninstalling %s...\n", len(all)-i, len(all), comp.Name)
		if err := e.uninstallComponent(s, &comp, exec); err != nil {
			fmt.Fprintf(e.output(), "  Warning: %v\n", err)
		}
	}

	// Mark as inactive
	e.markInactive(name)
	fmt.Fprintln(e.output(), "\nScenario deactivated.")
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
	sort.Slice(result, func(i, j int) bool {
		return catalogLess(result[i].Category, result[i].DisplayName, result[i].Name,
			result[j].Category, result[j].DisplayName, result[j].Name)
	})
	return result
}

// DeactivateAll clears every active-scenario marker without running teardown,
// and returns the names it cleared. Lab reset uses this: after a reset the
// scenarios' prerequisites are gone, so they must read as inactive (and be
// re-activatable) even when their normal component teardown could not complete.
func (e *Engine) DeactivateAll() []string {
	entries, err := os.ReadDir(e.stateDir)
	if err != nil {
		return nil
	}
	var cleared []string
	for _, en := range entries {
		if en.IsDir() || !strings.HasSuffix(en.Name(), ".active") {
			continue
		}
		name := strings.TrimSuffix(en.Name(), ".active")
		e.markInactive(name)
		cleared = append(cleared, name)
	}
	return cleared
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
	// after these and lose name collisions to them.
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
		if comp.UninstallScript == "" {
			return nil
		}
		return e.runScriptPath(s, comp.Name, comp.UninstallScript, exec)
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

	// A component the platform may already own is adopted rather than upgraded:
	// re-running `helm upgrade` over someone else's release rewrites its spec,
	// and for a StatefulSet most of that spec is immutable.
	if comp.Adopt && helmReleaseExists(comp.Name, ns, exec) {
		fmt.Fprintf(e.output(),
			"  %s: release already installed in %s — adopting it instead of re-installing.\n", comp.Name, ns)
		return nil
	}

	args := []string{
		"upgrade", "--install", comp.Name, chart,
		"--namespace", ns, "--create-namespace",
		"--wait", "--timeout", "5m",
	}

	if version != "" {
		args = append(args, "--version", version)
	}

	// Base values from the platform component, then the scenario's overlay. Order
	// matters: helm applies -f files left to right, so the overlay wins.
	if comp.PlatformValues != "" {
		// A component directory ("logging/loki" -> values.yaml inside it) or a
		// specific file ("logging/loki/promtail-values.yaml") for the components
		// whose values do not sit at the conventional path.
		rel := filepath.FromSlash(comp.PlatformValues)
		if filepath.Ext(rel) == "" {
			rel = filepath.Join(rel, "values.yaml")
		}
		base := filepath.Join(e.ProjectRoot, "platform", rel)
		if _, err := os.Stat(base); err != nil {
			return fmt.Errorf("component %q: platformValues %q: %w", comp.Name, comp.PlatformValues, err)
		}
		resolved, cleanup, err := e.resolveFileTemplate(base, "labctl-platform-values-*.yaml")
		if err != nil {
			return err
		}
		defer cleanup()
		args = append(args, "-f", resolved)
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

	out, err := exec.RunCommandStreamed("Helm install "+comp.Name, "helm", args...)
	if err == nil {
		return nil
	}

	// Recover from the one Helm failure a lab hits routinely: a StatefulSet whose
	// immutable fields (volumeClaimTemplates, serviceName, selector) differ from
	// the release already in the cluster. Orphaning the StatefulSet leaves the
	// pods and PVCs running while the new spec is applied on retry.
	if target := immutableStatefulSet(out + err.Error()); target != "" {
		fmt.Fprintf(e.output(),
			"  %s: StatefulSet %q has immutable fields that differ from the chart. "+
				"Deleting it with --cascade=orphan (pods and PVCs stay up) and retrying the upgrade.\n",
			comp.Name, target)
		_, _ = exec.RunCommandStreamed(
			"Recreating immutable StatefulSet "+target,
			"kubectl", "delete", "statefulset", target, "--namespace", ns, "--cascade=orphan", "--ignore-not-found")
		_, retryErr := exec.RunCommandStreamed("Helm install "+comp.Name+" (retry)", "helm", args...)
		return retryErr
	}

	return err
}

// helmReleaseExists reports whether a release is already installed in ns.
func helmReleaseExists(name, ns string, exec CommandExecutor) bool {
	_, err := exec.RunCommandStreamed(
		"Check for existing release "+name, "helm", "status", name, "--namespace", ns)
	return err == nil
}

// statefulSetImmutableRe matches the two shapes Helm reports when a StatefulSet's
// immutable spec fields differ from the live object — server-side apply and the
// older three-way-merge patch path.
var statefulSetImmutableRe = regexp.MustCompile(
	`(?:object\s+\S+/(\S+)\s+apps/v1,\s*Kind=StatefulSet|cannot patch "([^"]+)" with kind StatefulSet)`)

// immutableStatefulSet returns the StatefulSet name in a Helm "forbidden"
// upgrade error, or "" when the failure is something else.
func immutableStatefulSet(helmOutput string) string {
	if !strings.Contains(helmOutput, "updates to statefulset spec for fields other than") {
		return ""
	}
	m := statefulSetImmutableRe.FindStringSubmatch(helmOutput)
	if m == nil {
		return ""
	}
	for _, g := range m[1:] {
		if g != "" {
			return g
		}
	}
	return ""
}

func (e *Engine) uninstallHelm(comp *Component, exec CommandExecutor) error {
	// Adopt means borrow, not own. A scenario that reused a platform release must
	// not delete it on teardown — that would take Loki and Tempo down with the
	// scenario and leave every other scenario without them.
	if comp.Adopt {
		fmt.Fprintf(e.output(),
			"  %s: release is owned by the platform (adopted) — leaving it installed.\n", comp.Name)
		return nil
	}

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

	// Template the namespace like every other installer. Using the raw
	// comp.Namespace shipped a literal "{{.MonitoringNamespace}}" that kubectl
	// rejected — and the error was swallowed, so Up wrongly reported success.
	ns := e.componentNamespace(comp, e.MonitoringNamespace)

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dashDir, entry.Name()))
		if err != nil {
			return fmt.Errorf("reading dashboard %s: %w", entry.Name(), err)
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
			return fmt.Errorf("writing dashboard manifest %s: %w", entry.Name(), err)
		}
		// Propagate apply failures instead of discarding them, so a broken
		// dashboard surfaces as a failed activation rather than a false success.
		_, err = exec.RunCommandStreamed("Apply dashboard "+entry.Name(), "kubectl", "apply", "-f", tmpPath)
		cleanup()
		if err != nil {
			return fmt.Errorf("applying dashboard %s: %w", entry.Name(), err)
		}
	}

	return nil
}

func (e *Engine) uninstallGrafanaDashboard(s *Scenario, comp *Component, exec CommandExecutor) error {
	dashDir := filepath.Join(s.Dir, comp.Path)
	entries, err := os.ReadDir(dashDir)
	if err != nil {
		return nil //nolint:nilerr // no dashboard dir means nothing to delete — uninstall is a no-op
	}

	// Template the namespace as install does, or delete hits the wrong namespace
	// and the ConfigMap leaks while `scenario down` reports success.
	ns := e.componentNamespace(comp, e.MonitoringNamespace)

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
	return e.runScriptPath(s, comp.Name, comp.Script, exec)
}

// runScriptPath runs a scenario-relative script, labelled with the component name.
func (e *Engine) runScriptPath(s *Scenario, name, script string, exec CommandExecutor) error {
	// Compute the path relative to the project root for RunScriptStreamed
	relPath, err := filepath.Rel(e.ProjectRoot, filepath.Join(s.Dir, script))
	if err != nil {
		// Fallback to absolute path
		relPath = filepath.Join(s.Dir, script)
	}
	_, err = exec.RunScriptStreamed("Run script "+name, relPath)
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

// effectiveParams resolves each declared parameter to its default or user
// override, validated for type, bounds, and NotGreaterThan. An unknown override
// key is an error so a typo'd name fails loudly instead of being ignored.
func (e *Engine) effectiveParams(s *Scenario) (map[string]string, error) {
	declared := make(map[string]Parameter, len(s.Parameters))
	for _, p := range s.Parameters {
		declared[p.Name] = p
	}
	for k := range e.activationParams {
		if _, ok := declared[k]; !ok {
			names := make([]string, 0, len(s.Parameters))
			for _, p := range s.Parameters {
				names = append(names, p.Name)
			}
			sort.Strings(names)
			if len(names) == 0 {
				return nil, fmt.Errorf("scenario %q accepts no parameters", s.Name)
			}
			return nil, fmt.Errorf("unknown parameter %q (scenario %q accepts: %s)", k, s.Name, strings.Join(names, ", "))
		}
	}

	values := make(map[string]string, len(s.Parameters))
	ints := make(map[string]int, len(s.Parameters))
	for _, p := range s.Parameters {
		val, label := p.Default, "default"
		if ov, ok := e.activationParams[p.Name]; ok {
			val, label = strings.TrimSpace(ov), "override"
		}
		if err := p.ValidateValue(val, label); err != nil {
			return nil, err
		}
		values[p.Name] = val
		if p.IsInt() {
			n, _ := strconv.Atoi(val) // safe: ValidateValue parsed it
			ints[p.Name] = n
		}
	}

	// Relational constraints, e.g. minReplicas ≤ maxReplicas.
	for _, p := range s.Parameters {
		if p.NotGreaterThan == "" {
			continue
		}
		if this, ok1 := ints[p.Name]; ok1 {
			if other, ok2 := ints[p.NotGreaterThan]; ok2 && this > other {
				return nil, fmt.Errorf("parameter %q (%d) must not be greater than %q (%d)", p.Name, this, p.NotGreaterThan, other)
			}
		}
	}
	return values, nil
}

func (e *Engine) resolveTemplate(input string) string {
	return e.resolveTemplateWith(input, nil)
}

// ResolveTemplateWithParams resolves template vars using the given parameter
// values on top of the engine's built-ins. Used to render a scenario's snippets
// and explore hints with parameter defaults so {{.Param}} shows a real value.
func (e *Engine) ResolveTemplateWithParams(input string, params map[string]string) string {
	return e.resolveTemplateWith(input, params)
}

// resolveTemplateWith substitutes {{.Var}} placeholders. Precedence: built-ins,
// then the active activation's resolvedParams, then extra — so a parameter can
// never shadow a built-in, and live activation values win over display defaults.
func (e *Engine) resolveTemplateWith(input string, extra map[string]string) string {
	data := map[string]string{
		"DomainSuffix":        e.DomainSuffix,
		"ProjectRoot":         e.ProjectRoot,
		"MonitoringNamespace": e.MonitoringNamespace,
		"LokiRetentionPeriod": lokiRetentionPeriod(),
		"IngressClass":        ingressClassOr(e.IngressClass),
	}
	overlay := func(src map[string]string) {
		for k, v := range src {
			if _, taken := data[k]; !taken {
				data[k] = v
			}
		}
	}
	overlay(e.resolvedParams)
	overlay(extra)
	return labctlVar.ReplaceAllStringFunc(input, func(match string) string {
		key := labctlVar.FindStringSubmatch(match)[1]
		if v, ok := data[key]; ok {
			return v
		}
		return match // unknown {{.Var}} — leave it for whoever else consumes it
	})
}

// ParamDefaults maps each declared parameter to its default value, or nil when
// the scenario declares none.
func (e *Engine) ParamDefaults(s *Scenario) map[string]string {
	if len(s.Parameters) == 0 {
		return nil
	}
	m := make(map[string]string, len(s.Parameters))
	for _, p := range s.Parameters {
		m[p.Name] = p.Default
	}
	return m
}

// SnippetContent returns a snippet's display text — its inline YAML or the
// contents of its Path file — template-resolved with the scenario's parameter
// defaults so {{.Param}} placeholders show real values. Path reads are confined
// to the scenario directory. A file's leading comment banner is stripped so the
// UI shows clean code (the banner's explanation belongs in the snippet's
// description); inline comments on individual fields are kept.
func (e *Engine) SnippetContent(s *Scenario, sn Snippet) (string, error) {
	defaults := e.ParamDefaults(s)
	if sn.YAML != "" {
		return e.resolveTemplateWith(sn.YAML, defaults), nil
	}
	if sn.Path == "" {
		return "", nil
	}
	full := filepath.Join(s.Dir, filepath.Clean(sn.Path))
	if !strings.HasPrefix(full, filepath.Clean(s.Dir)+string(os.PathSeparator)) {
		return "", fmt.Errorf("snippet path %q escapes the scenario directory", sn.Path)
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	return e.resolveTemplateWith(stripLeadingCommentBanner(string(data)), defaults), nil
}

// stripLeadingCommentBanner drops a manifest's leading block of "#" comment and
// blank lines — the header that documents the file for repo readers — so the
// snippet shown in the UI starts at the first real line of config. Inline
// comments further down (e.g. after a YAML field) are untouched.
func stripLeadingCommentBanner(src string) string {
	lines := strings.Split(src, "\n")
	i := 0
	for i < len(lines) {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			i++
			continue
		}
		break
	}
	return strings.Join(lines[i:], "\n")
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

	fmt.Fprintln(e.output(), "\n=== Explore This Scenario ===")

	if len(s.Explore.URLs) > 0 {
		fmt.Fprintln(e.output(), "\nURLs:")
		for _, u := range s.Explore.URLs {
			resolved := e.resolveTemplate(u.URL)
			fmt.Fprintf(e.output(), "  %-30s %s\n", u.Label+":", resolved)
		}
	}

	if len(s.Explore.Commands) > 0 {
		fmt.Fprintln(e.output(), "\nCommands to try:")
		for _, c := range s.Explore.Commands {
			resolved := e.resolveTemplate(c.Command)
			fmt.Fprintf(e.output(), "  %s:\n    %s\n", c.Label, resolved)
		}
	}

	if len(s.Explore.Tips) > 0 {
		fmt.Fprintln(e.output(), "\nTips:")
		for _, t := range s.Explore.Tips {
			fmt.Fprintf(e.output(), "  - %s\n", e.resolveTemplate(t))
		}
	}

	fmt.Fprintln(e.output())
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
