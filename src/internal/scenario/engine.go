// SPDX-License-Identifier: Apache-2.0

// Package scenario loads scenarios from scenarios/<name>/scenario.yaml,
// installs and removes their components with helm and kubectl, expands their
// templates for the bound workload, and runs their checks.
package scenario

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/sagar2395/snowopslabs/internal/config"
	"github.com/sagar2395/snowopslabs/internal/snippet"
	"github.com/sagar2395/snowopslabs/internal/tmpl"
	"github.com/sagar2395/snowopslabs/internal/workload"
	"github.com/sagar2395/snowopslabs/pkg/checks"
	"github.com/sagar2395/snowopslabs/pkg/extension"
	schema "github.com/sagar2395/snowopslabs/pkg/scenario"
)

// CommandExecutor runs the helm and kubectl commands and scenario scripts the
// engine issues. *executor.Executor implements it; tests use a recorder to
// check the exact commands without a cluster.
type CommandExecutor interface {
	RunCommandStreamed(actionLabel, name string, args ...string) (string, error)
	RunScriptStreamed(actionLabel, scriptPath string, args ...string) (string, error)
}

// ErrAlreadyActive is returned by Up when the scenario is already active.
// Callers should treat this as a no-op, not a failure.
var ErrAlreadyActive = errors.New("scenario already active")

// ErrNoChecks is returned by Verify when the scenario declares no checks.
var ErrNoChecks = errors.New("scenario defines no checks")

// Aliases for the scenario types defined in the public package pkg/scenario,
// where their validation also lives.
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
	Reference      = schema.Reference
)

// Engine discovers, loads, and manages scenarios.
type Engine struct {
	ProjectRoot         string
	DomainSuffix        string
	Profile             string // active runtime profile (k3d|kind|incluster), used for preflight
	MonitoringNamespace string // namespace for monitoring/logging/tracing (default: "monitoring")
	IngressClass        string // ingress class for scenario Ingress manifests (default: "traefik")

	// Workload is the app scenarios run against. Content refers to it as
	// {{.WorkloadName}} and similar, so one scenario works with any app
	// (ADR-0014).
	Workload workload.Workload
	// Contract is Workload's declared contract. Preflight checks a scenario's
	// required capabilities against it.
	Contract workload.Contract

	// Hooks let a build add behaviour around activation and checks (ADR-0008).
	// The default hooks do nothing.
	Hooks extension.Hooks

	scenarios  map[string]*Scenario
	loadErrors map[string]error // scenario dir name → why it failed to load
	stateDir   string

	// out receives Up and Down's progress output; nil means os.Stdout. The
	// scenario service points it at the run transcript.
	out io.Writer

	// activationParams holds the user's overrides for the next Up, set with
	// SetActivationParams. resolvedParams holds the values actually used
	// (defaults plus overrides), which {{.ParamName}} expands to during Up.
	activationParams map[string]string
	resolvedParams   map[string]string
	// activatedAt is when the scenario being checked was activated.
	// {{.SinceActivation}} is measured from it.
	activatedAt time.Time
}

// SetActivationParams sets parameter overrides for the next Up; nil clears
// them. Do not call it while another activation on this engine is running.
func (e *Engine) SetActivationParams(params map[string]string) { e.activationParams = params }

// SetOutput sets where Up and Down write progress; nil means os.Stdout. Do not
// call it while another activation on this engine is running.
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
	// Start bound to the default app, with its declared contract, so the
	// capability check works even if the caller never binds another app.
	bound := workload.Default(workload.DefaultApp)
	var contract workload.Contract
	if appCfg, err := config.LoadAppConfig(projectRoot, workload.DefaultApp); err == nil {
		bound, contract = appCfg.Workload(), appCfg.Contract
	}
	e := &Engine{
		ProjectRoot:         projectRoot,
		DomainSuffix:        domainSuffix,
		Profile:             profile,
		MonitoringNamespace: ns,
		Workload:            bound,
		Contract:            contract,
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

// catalogLess orders scenarios by category, then display name, then name, so
// listings are stable.
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

// Preflight checks that a scenario can be activated: the runtime, the
// prerequisite apps and platform components, the app's capabilities, and the
// component and check files. It returns one error listing every problem.
func (e *Engine) Preflight(s *Scenario) error {
	var errs []string

	// 1. Runtime compatibility — only checked when the scenario restricts runtimes.
	if len(s.Runtimes) > 0 && e.Profile != "" {
		if !slices.Contains(s.Runtimes, e.Profile) {
			errs = append(errs, fmt.Sprintf(
				"active profile %q is not in supported runtimes %v", e.Profile, s.Runtimes))
		}
	}

	// 2. Prerequisite apps — check apps/<name>/app.env exists.
	for _, app := range e.ResolvedPrereqApps(s) {
		appEnv := filepath.Join(e.ProjectRoot, "apps", app, "app.env")
		if _, err := os.Stat(appEnv); err != nil {
			errs = append(errs, fmt.Sprintf(
				"prerequisite app %q not found — run 'labctl app build %s && labctl app deploy %s' (expected %s)",
				app, app, app, appEnv))
		}
	}

	// 3. Workload capabilities — the bound app must declare every capability
	// the scenario requires.
	var required []workload.Capability
	for _, name := range s.Prerequisites.Capabilities {
		c, err := workload.ParseCapability(name)
		if err != nil {
			errs = append(errs, fmt.Sprintf("prerequisite capability: %v", err))
			continue
		}
		required = append(required, c)
	}
	if missing := e.Contract.Missing(required); len(missing) > 0 {
		names := make([]string, len(missing))
		for i, m := range missing {
			names[i] = string(m)
		}
		errs = append(errs, fmt.Sprintf(
			"workload %q does not declare: %s — either bind an app that does (APP_NAME=<app>, see 'labctl app list') or add the capability to apps/%s/app.env once the app truly provides it",
			e.Workload.Name, strings.Join(names, ", "), e.Workload.Name))
	}

	// 4. Prerequisite platform components — check platform/<category>/ directory exists.
	for _, p := range s.Prerequisites.Platform {
		platformDir := filepath.Join(e.ProjectRoot, "platform", p)
		if _, err := os.Stat(platformDir); err != nil {
			errs = append(errs, fmt.Sprintf(
				"prerequisite platform %q not found — run 'labctl platform up' (expected %s)",
				p, platformDir))
		}
	}

	// 5. Component asset files.
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

	// 6. Check script files.
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

// withActivationParams sets resolvedParams to the values the scenario was
// activated with, or its defaults, and returns a function that restores the
// previous values. Anything that renders the scenario's files after Up, such
// as Down and Verify, must call it so {{.Param}} expands the same way.
func (e *Engine) withActivationParams(s *Scenario) func() {
	params := e.activeParams(s.Name)
	if params == nil {
		params, _ = e.effectiveParams(s)
	}
	prevParams, prevAt := e.resolvedParams, e.activatedAt
	e.resolvedParams, e.activatedAt = params, e.activationTime(s.Name)
	return func() { e.resolvedParams, e.activatedAt = prevParams, prevAt }
}

// BindTo binds the engine to the named app and returns a function that
// restores the previous binding. An unknown or malformed app returns an error
// and leaves the binding unchanged.
func (e *Engine) BindTo(app string) (func(), error) {
	restore := func(w workload.Workload, c workload.Contract) func() {
		return func() { e.Workload, e.Contract = w, c }
	}(e.Workload, e.Contract)
	if app == "" || app == e.Workload.Name {
		return restore, nil
	}
	cfg, err := config.LoadAppConfig(e.ProjectRoot, app)
	if err != nil {
		return restore, fmt.Errorf("app %s: %w", app, err)
	}
	e.Workload, e.Contract = cfg.Workload(), cfg.Contract
	return restore, nil
}

// withActivationWorkload binds the engine to the app the scenario was
// activated against and returns the restore function. Verify and Down usually
// run in a later process than Up, so they cannot rely on the current
// configuration.
func (e *Engine) withActivationWorkload(name string) func() {
	restore, err := e.BindTo(e.ActiveApp(name))
	if err != nil {
		// The recorded app is gone or broken. Carry on with the current
		// binding, with a warning, so teardown can still run.
		fmt.Fprintf(e.output(), "Warning: %v — continuing against %s.\n", err, e.Workload.Name)
	}
	return restore
}

// ResolvedPrereqApps returns the scenario's prerequisite apps with templates
// expanded. Use it instead of reading Prerequisites.Apps directly.
func (e *Engine) ResolvedPrereqApps(s *Scenario) []string {
	if len(s.Prerequisites.Apps) == 0 {
		return nil
	}
	out := make([]string, 0, len(s.Prerequisites.Apps))
	for _, declared := range s.Prerequisites.Apps {
		if app := e.resolveTemplate(declared); app != "" {
			out = append(out, app)
		}
	}
	return out
}

// PinnedPrereqApps returns the prerequisite apps the scenario names literally,
// such as "go-api". An entry written as {{.WorkloadName}} follows the binding
// and is not a real requirement, so it is left out. It takes a name because it
// must read the scenario as written, before templates are expanded.
func (e *Engine) PinnedPrereqApps(name string) []string {
	out := []string{}
	sc, ok := e.scenarios[name]
	if !ok {
		return out
	}
	for _, declared := range sc.Prerequisites.Apps {
		if declared != "" && !tmpl.IsTemplated(declared) {
			out = append(out, declared)
		}
	}
	return out
}

// Up activates a scenario by installing all its components. If the scenario is
// already active it returns ErrAlreadyActive, unless force is true, in which
// case it installs again; every install is idempotent.
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

	// Expand labels as well as commands; both are printed.
	fmt.Fprintf(e.output(), "Activating scenario: %s\n", e.resolveTemplate(s.DisplayName))
	fmt.Fprintf(e.output(), "  %s\n\n", e.resolveTemplate(s.Description))

	if len(s.Objectives) > 0 {
		fmt.Fprintln(e.output(), "Objectives:")
		for _, o := range s.Objectives {
			fmt.Fprintf(e.output(), "  - %s\n", e.resolveTemplate(o))
		}
		fmt.Fprintln(e.output())
	}

	// Print the parameter values used, marking the user's overrides.
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
				fmt.Fprintf(e.output(), "    %s\n", e.resolveTemplate(st.Description))
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

	if err := e.markActive(name, params); err != nil {
		return fmt.Errorf("marking scenario active: %w", err)
	}

	if len(s.Checks) > 0 {
		fmt.Fprintf(e.output(), "\nThis scenario has %d verifiable checks. Run: labctl scenario verify %s\n", len(s.Checks), s.Name)
		fmt.Fprintf(e.output(), "Pods may still be starting — add --watch to wait for them:\n  labctl scenario verify %s --watch\n", s.Name)
	}

	e.printExploreHints(s)

	return nil
}

// Verify runs the scenario's checks, with templates expanded, and returns one
// result per check. The scenario does not have to be active.
func (e *Engine) Verify(ctx context.Context, name string, runner *checks.Runner) ([]checks.Result, error) {
	s, err := e.Get(name)
	if err != nil {
		return nil, err
	}
	if len(s.Checks) == 0 {
		return nil, fmt.Errorf("%w: %s (add a checks block — see docs/reference/scenario-schema.md)", ErrNoChecks, name)
	}

	runner.ScriptDir = s.Dir

	// Expand {{.Param}} with the values the scenario was activated with.
	defer e.withActivationWorkload(name)()
	defer e.withActivationParams(s)()

	resolved := make([]checks.Check, len(s.Checks))
	for i, c := range s.Checks {
		resolved[i] = e.resolveCheck(c)
	}

	// A PreCheck hook that returns an error stops verification. The default
	// hooks do nothing.
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

// ResolveCheck returns c with its template variables expanded, as Verify does.
// Callers outside this package that run a scenario's checks must use it.
func (e *Engine) ResolveCheck(c checks.Check) checks.Check {
	return e.resolveCheck(c)
}

func (e *Engine) resolveCheck(c checks.Check) checks.Check {
	return resolveCheckWith(c, e.resolveTemplate)
}

// ResolveCheckWithParams returns c expanded with the given parameter values,
// for display.
func (e *Engine) ResolveCheckWithParams(c checks.Check, params map[string]string) checks.Check {
	return resolveCheckWith(c, func(in string) string { return e.resolveTemplateWith(in, params) })
}

func resolveCheckWith(c checks.Check, resolve func(string) string) checks.Check {
	c.URL = resolve(c.URL)
	c.BodyContains = resolve(c.BodyContains)
	c.Resource = resolve(c.Resource)
	c.Namespace = resolve(c.Namespace)
	c.JSONPath = resolve(c.JSONPath)
	c.Query = resolve(c.Query)
	c.Value = resolve(c.Value)
	// The remediation is shown to the user as a command to run.
	c.Remediation = resolve(c.Remediation)
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

	// Render with the same parameters and workload as the install, so every
	// object is deleted by the name it was created with.
	defer e.withActivationWorkload(name)()
	defer e.withActivationParams(s)()

	// Uninstall in reverse install order, across all stages.
	all := s.AllComponents()
	for i, comp := range slices.Backward(all) {
		fmt.Fprintf(e.output(), "[%d/%d] Uninstalling %s...\n", len(all)-i, len(all), comp.Name)
		if err := e.uninstallComponent(s, &comp, exec); err != nil {
			fmt.Fprintf(e.output(), "  Warning: %v\n", err)
		}
	}

	e.markInactive(name)
	fmt.Fprintln(e.output(), "\nScenario deactivated.")
	return nil
}

// Status returns a summary of active scenarios.
func (e *Engine) Status() []ScenarioStatus {
	var result []ScenarioStatus
	for _, s := range e.scenarios {
		// Expand templates in the description shown in the catalog.
		bound, app := e.Workload, e.ActiveApp(s.Name)
		if app == "" {
			app = e.Workload.Name
		} else if cfg, err := config.LoadAppConfig(e.ProjectRoot, app); err == nil {
			bound = cfg.Workload()
		}
		resolve := func(in string) string { return tmpl.Expand(in, e.templateContextFor(bound), e.resolvedParams) }
		result = append(result, ScenarioStatus{
			Name:        s.Name,
			DisplayName: resolve(s.DisplayName),
			Description: resolve(s.Description),
			Category:    s.Category,
			Runtimes:    s.Runtimes,
			Active:      e.isActive(s.Name),
			App:         app,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return catalogLess(result[i].Category, result[i].DisplayName, result[i].Name,
			result[j].Category, result[j].DisplayName, result[j].Name)
	})
	return result
}

// DeactivateAll clears every active-scenario marker without running teardown
// and returns the names it cleared. Lab reset uses it, because after a reset
// the scenarios must show as inactive even if their teardown failed.
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
	// App is the workload this scenario runs against: the one it was activated
	// against if it is active, otherwise the current binding.
	App string `json:"app,omitempty"`
}

func (e *Engine) scan() {
	// In-repo scenarios are loaded first. Scenarios from SNOWOPS_CONTENT_PATH
	// roots come after and cannot replace one with the same name.
	scenariosDir := filepath.Join(e.ProjectRoot, "scenarios")
	if entries, err := os.ReadDir(scenariosDir); err == nil {
		for _, entry := range entries {
			// "_"-prefixed directories hold shared scripts, not scenarios.
			if !entry.IsDir() || strings.HasPrefix(entry.Name(), "_") {
				continue
			}
			e.loadInto(filepath.Join(scenariosDir, entry.Name()), "", entry.Name())
		}
	}
}

// loadInto loads one scenario directory. A failure or name collision is
// recorded under key and reported by LoadErrors, so one broken scenario does
// not stop the others loading.
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
	maps.Copy(out, e.loadErrors)
	return out
}

func (e *Engine) loadScenario(path string) (*Scenario, error) {
	return loadScenarioFile(path)
}

// loadScenarioFile parses and validates a scenario.yaml.
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

	// Expand chart, repo and version too: a local chart is written as
	// {{.ProjectRoot}}/apps/.../helm.
	chart := e.resolveTemplate(comp.Chart)
	repo := e.resolveTemplate(comp.Repo)
	version := e.resolveTemplate(comp.Version)

	// Add the helm repo if one is given. Errors are ignored: if the repo is
	// really unreachable, `helm upgrade --install` fails with a clear error.
	if repo != "" {
		repoName := strings.Split(chart, "/")[0]
		_, _ = exec.RunCommandStreamed("Helm repo add "+repoName, "helm", "repo", "add", repoName, repo, "--force-update")
		_, _ = exec.RunCommandStreamed("Helm repo update", "helm", "repo", "update")
	}

	// With adopt set, a release the platform already installed is used as it
	// is. Upgrading it would rewrite its spec, and most of a StatefulSet's spec
	// cannot change.
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

	// The platform component's values first, then the scenario's; helm applies
	// -f files in order, so the scenario's values win.
	if comp.PlatformValues != "" {
		// Either a component directory ("logging/loki" means its values.yaml)
		// or a specific file ("logging/loki/promtail-values.yaml").
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

	// If the upgrade failed because a StatefulSet's immutable fields changed,
	// delete the StatefulSet with --cascade=orphan, keeping its pods and PVCs,
	// and retry so the new spec can be applied.
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

// statefulSetImmutableRe matches the two error messages Helm gives when a
// StatefulSet's immutable fields differ from the live object.
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
	// An adopted release belongs to the platform, so teardown leaves it in
	// place.
	if comp.Adopt {
		fmt.Fprintf(e.output(),
			"  %s: release is owned by the platform (adopted) — leaving it installed.\n", comp.Name)
		return nil
	}

	// Resolve the namespace exactly as installHelm does.
	ns := e.componentNamespace(comp, "default")
	_, err := exec.RunCommandStreamed("Helm uninstall "+comp.Name, "helm", "uninstall", comp.Name, "--namespace", ns)
	return err
}

// writeTempManifest writes content to a temporary YAML file and returns its
// path and a function that removes it.
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

	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("reading manifest %s: %w", manifestPath, err)
	}

	resolved := e.resolveTemplate(string(data))

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

	// Resolve the namespace like every other installer.
	ns := e.componentNamespace(comp, e.MonitoringNamespace)

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		raw, err := os.ReadFile(filepath.Join(dashDir, entry.Name()))
		if err != nil {
			return fmt.Errorf("reading dashboard %s: %w", entry.Name(), err)
		}
		// Expand labctl variables so panels can query the bound workload.
		// Grafana's own {{namespace}} and $variables are left alone.
		data := e.resolveTemplate(string(raw))

		cmName := fmt.Sprintf("scenario-%s-%s", s.Name, strings.TrimSuffix(entry.Name(), ".json"))

		// The label makes Grafana's sidecar load the dashboard.
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
			cmName, ns, entry.Name(), indentJSON(data, "    "))

		tmpPath, cleanup, err := writeTempManifest(cm)
		if err != nil {
			return fmt.Errorf("writing dashboard manifest %s: %w", entry.Name(), err)
		}
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

	// Resolve the namespace as install does.
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
	// RunScriptStreamed takes a path relative to the project root.
	relPath, err := filepath.Rel(e.ProjectRoot, filepath.Join(s.Dir, script))
	if err != nil {
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

// ResolveTemplate expands labctl template variables, such as {{.DomainSuffix}},
// in input.
func (e *Engine) ResolveTemplate(input string) string {
	return e.resolveTemplate(input)
}

// effectiveParams returns each declared parameter's value: the user's override
// or the default, validated for type, bounds and notGreaterThan. An override
// for an undeclared parameter is an error.
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

// ResolveTemplateWithParams expands template variables, using params for
// {{.Param}} references. Display code passes the parameter defaults.
func (e *Engine) ResolveTemplateWithParams(input string, params map[string]string) string {
	return e.resolveTemplateWith(input, params)
}

// resolveTemplateWith expands {{.Var}} placeholders. Built-in variables win
// over resolvedParams, which win over extra.
func (e *Engine) resolveTemplateWith(input string, extra map[string]string) string {
	overlay := make(map[string]string, len(e.resolvedParams)+len(extra))
	maps.Copy(overlay, e.resolvedParams)
	for k, v := range extra {
		if _, taken := overlay[k]; !taken {
			overlay[k] = v
		}
	}
	return tmpl.Expand(input, e.templateContext(), overlay)
}

// templateContext returns the template variables for the engine's current
// binding.
func (e *Engine) templateContext() tmpl.Context {
	return e.templateContextFor(e.Workload)
}

// templateContextFor is templateContext for an explicit workload, such as the
// app an active scenario was activated against.
func (e *Engine) templateContextFor(bound workload.Workload) tmpl.Context {
	w := bound.WithDefaults()
	return tmpl.Context{
		DomainSuffix:        e.DomainSuffix,
		MonitoringNamespace: e.MonitoringNamespace,
		ProjectRoot:         e.ProjectRoot,
		LokiRetentionPeriod: lokiRetentionPeriod(),
		IngressClass:        ingressClassOr(e.IngressClass),
		WorkloadName:        w.Name,
		WorkloadNamespace:   w.Namespace,
		WorkloadService:     w.Service(),
		WorkloadPort:        w.Port,
		WorkloadMetric:      w.Metric,
		SinceActivation:     tmpl.Since(e.activatedAt, time.Now()),
	}
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

// RenderFile returns a file from the scenario's directory with templates
// expanded as the engine would expand them, so a learner can pipe it into
// kubectl.
func (e *Engine) RenderFile(s *Scenario, rel string) (string, error) {
	raw, err := snippet.ReadFile(s.Dir, rel)
	if err != nil {
		return "", err
	}
	defer e.withActivationParams(s)()
	return e.resolveTemplate(raw), nil
}

// ingressClassOr returns the configured ingress class, or "traefik" (the k3d
// default) when none is set.
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

// markActive writes the activation marker, recording the parameters and app
// the scenario was activated with. Verify and Down read them back.
func (e *Engine) markActive(name string, params map[string]string) error {
	if err := os.MkdirAll(e.stateDir, 0755); err != nil {
		return err
	}
	// With nothing to record, write the plain "active" marker.
	body := []byte("active")
	if len(params) > 0 || e.Workload.Name != "" {
		if encoded, err := json.Marshal(activationState{Params: params, App: e.Workload.Name}); err == nil {
			body = encoded
		}
	}
	return os.WriteFile(filepath.Join(e.stateDir, name+".active"), body, 0644)
}

// activationState is the JSON content of an .active marker. A marker may
// instead contain the plain text "active", meaning no parameters and no app.
type activationState struct {
	Params map[string]string `json:"params"`
	// App is the workload the scenario was activated against. Verify and
	// Down use it rather than the current configuration.
	App string `json:"app,omitempty"`
}

// activationRecord returns what a scenario was activated with, or the zero
// value when it is inactive or its marker is the plain "active".
func (e *Engine) activationRecord(name string) activationState {
	data, err := os.ReadFile(filepath.Join(e.stateDir, name+".active"))
	if err != nil {
		return activationState{}
	}
	var st activationState
	if err := json.Unmarshal(data, &st); err != nil {
		return activationState{} // the plain "active" marker
	}
	return st
}

// activationTime returns when a scenario was activated, or zero if it is not
// active. It is the marker file's modification time: Up writes the marker as
// its last step, and a re-activation rewrites it.
func (e *Engine) activationTime(name string) time.Time {
	fi, err := os.Stat(filepath.Join(e.stateDir, name+".active"))
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

// activeParams returns the parameters a scenario was activated with, or nil.
func (e *Engine) activeParams(name string) map[string]string {
	return e.activationRecord(name).Params
}

// ActiveApp returns the app a scenario was activated against, or "" when it is
// inactive or its marker records no app.
func (e *Engine) ActiveApp(name string) string { return e.activationRecord(name).App }

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
			fmt.Fprintf(e.output(), "  %-30s %s\n", e.resolveTemplate(u.Label)+":", resolved)
		}
	}

	if len(s.Explore.Commands) > 0 {
		fmt.Fprintln(e.output(), "\nCommands to try:")
		for _, c := range s.Explore.Commands {
			resolved := e.resolveTemplate(c.Command)
			fmt.Fprintf(e.output(), "  %s:\n    %s\n", e.resolveTemplate(c.Label), resolved)
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
	for line := range strings.SplitSeq(s, "\n") {
		result.WriteString(prefix)
		result.WriteString(line)
		result.WriteString("\n")
	}
	return result.String()
}

func manifestHasExplicitNamespace(manifest string) bool {
	decoder := yaml.NewDecoder(strings.NewReader(manifest))

	for {
		var doc map[string]any
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

		metadata, ok := metadataRaw.(map[string]any)
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

// Clone returns a shallow copy that shares the scanned content but owns its own
// binding and per-activation state.
//
// The API server shares one engine across requests, so a request that needs a
// different binding works on a clone. Sharing the scenarios map is safe because
// it is never modified after loading, and activation state is on disk.
func (e *Engine) Clone() *Engine {
	c := *e
	return &c
}
