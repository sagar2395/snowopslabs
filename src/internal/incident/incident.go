// SPDX-License-Identifier: Apache-2.0

// Package incident loads and validates the faults under incidents/<name>/,
// injects and resolves them by running their scripts, and detects when a
// fault has been fixed by running its detection check.
package incident

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/sagar2395/snowopslabs/internal/config"
	"github.com/sagar2395/snowopslabs/internal/executor"
	"github.com/sagar2395/snowopslabs/internal/snippet"
	"github.com/sagar2395/snowopslabs/internal/tmpl"
	"github.com/sagar2395/snowopslabs/internal/workload"
	"github.com/sagar2395/snowopslabs/pkg/checks"
	"github.com/sagar2395/snowopslabs/pkg/scenario"
)

// ErrIncidentActive is returned by Inject when another incident is active.
var ErrIncidentActive = errors.New("an incident is already active")

// ErrNoActive is returned when an operation needs an active incident.
var ErrNoActive = errors.New("no incident is active")

// Fault is the parsed fault.yaml of one incident.
type Fault struct {
	Name        string `yaml:"name" json:"name"`
	DisplayName string `yaml:"displayName" json:"displayName"`
	Description string `yaml:"description" json:"description"`
	// Verified marks a fault that has been tested end to end on a fresh
	// cluster.
	Verified      bool          `yaml:"verified,omitempty" json:"verified"`
	Category      string        `yaml:"category" json:"category"` // workload | network | resources | storage | config
	Severity      string        `yaml:"severity" json:"severity"` // low | medium | high
	Target        Target        `yaml:"target" json:"target"`
	Prerequisites Prerequisites `yaml:"prerequisites" json:"prerequisites"`
	Detection     checks.Check  `yaml:"detection" json:"detection"` // passes ⇔ the fault is RESOLVED
	// ExpectAlert names the Alertmanager alert this fault should fire.
	// inject.sh applies the matching PrometheusRule from alerts/rule.yaml,
	// and `incident status` reports whether the alert is firing.
	ExpectAlert string `yaml:"expectAlert,omitempty" json:"expectAlert,omitempty"`

	// References and Snippets give the learner documentation links and
	// manifest fragments they can apply. Scenarios use the same types.
	References []scenario.Reference `yaml:"references,omitempty" json:"references,omitempty"`
	Snippets   []scenario.Snippet   `yaml:"snippets,omitempty" json:"snippets,omitempty"`

	Dir string `yaml:"-" json:"-"`
}

// Target names what the fault breaks. Scripts receive it as TARGET_NAMESPACE
// and TARGET_WORKLOAD.
type Target struct {
	Namespace string `yaml:"namespace" json:"namespace"`
	Workload  string `yaml:"workload" json:"workload"`
}

// Prerequisites name what must already be installed for the fault to work,
// such as "ingress" when the detection check calls the app through it. They
// are not installed automatically; the UI lists them for the user.
type Prerequisites struct {
	Platform []string `yaml:"platform,omitempty" json:"platform,omitempty"`
	Apps     []string `yaml:"apps" json:"apps"`
}

var validCategories = map[string]bool{
	"workload": true, "network": true, "resources": true, "storage": true, "config": true,
}
var validSeverities = map[string]bool{"low": true, "medium": true, "high": true}

// contractFiles must exist in every fault directory.
var contractFiles = []string{"fault.yaml", "inject.sh", "resolve.sh", "hints.md", "solution.md"}

// Validate reports all schema problems at once.
func (f *Fault) Validate() error {
	var errs []string
	add := func(format string, args ...any) {
		errs = append(errs, fmt.Sprintf(format, args...))
	}

	if strings.TrimSpace(f.Name) == "" {
		add("name is required")
	}
	if !validCategories[f.Category] {
		add("unknown category %q (expected workload | network | resources | storage | config)", f.Category)
	}
	if !validSeverities[f.Severity] {
		add("unknown severity %q (expected low | medium | high)", f.Severity)
	}
	if f.Target.Namespace == "" || f.Target.Workload == "" {
		add("target.namespace and target.workload are required")
	}
	if err := f.Detection.Validate(); err != nil {
		add("detection: %v", err)
	}
	if f.Detection.Script != "" {
		if filepath.IsAbs(f.Detection.Script) || strings.Contains(filepath.ToSlash(f.Detection.Script), "..") {
			add("detection script %q must be a relative path inside the fault directory", f.Detection.Script)
		}
	}
	for _, msg := range scenario.ValidateReferences(f.References) {
		add("%s", msg)
	}
	for _, msg := range scenario.ValidateSnippets(f.Snippets) {
		add("%s", msg)
	}

	if len(errs) > 0 {
		name := f.Name
		if name == "" {
			name = "(unnamed)"
		}
		return fmt.Errorf("invalid fault %q:\n  - %s", name, strings.Join(errs, "\n  - "))
	}
	return nil
}

// Active is the persisted record of the currently injected incident.
type Active struct {
	Fault         string    `yaml:"fault" json:"fault"`
	InjectedAt    time.Time `yaml:"injectedAt" json:"injectedAt"`
	Silent        bool      `yaml:"silent" json:"silent"`
	HintsRevealed int       `yaml:"hintsRevealed" json:"hintsRevealed"`
	// FirstCheckedAt is when `incident status` first ran. The run record uses
	// it as the time the problem was detected.
	FirstCheckedAt time.Time `yaml:"firstCheckedAt,omitempty" json:"firstCheckedAt,omitempty"`
	// App is the workload the fault was injected into. Status and Resolve
	// usually run in a later process than Inject, so they read the app from
	// here rather than from the current configuration.
	App string `yaml:"app,omitempty" json:"app,omitempty"`
}

// Engine discovers faults and manages the (single) active incident.
type Engine struct {
	ProjectRoot  string
	DomainSuffix string
	// AlertmanagerURL is where Status queries fired alerts.
	// Callers set it from ALERTMANAGER_URL or the ingress default.
	AlertmanagerURL string
	// MonitoringNamespace is where the monitoring stack lives. Faults refer to
	// it as {{.MonitoringNamespace}}.
	MonitoringNamespace string
	// Workload is the app faults are injected into, unless a fault names a
	// fixed target.
	Workload   workload.Workload
	faults     map[string]*Fault
	loadErrors map[string]error
	stateDir   string
	// injectedAt is when the incident being graded was injected, set only while
	// Status runs; {{.SinceActivation}} is measured from it.
	injectedAt time.Time
}

// NewEngine scans incidents/ under the project root.
func NewEngine(projectRoot, domainSuffix string) *Engine {
	e := &Engine{
		ProjectRoot:         projectRoot,
		DomainSuffix:        domainSuffix,
		MonitoringNamespace: "monitoring",
		Workload:            workload.Default(workload.DefaultApp),
		faults:              make(map[string]*Fault),
		loadErrors:          make(map[string]error),
		stateDir:            filepath.Join(projectRoot, ".labctl", "incidents"),
	}
	e.scan()
	return e
}

func (e *Engine) scan() {
	dir := filepath.Join(e.ProjectRoot, "incidents")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		// A directory starting with "_" holds shared scripts, not a fault.
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), "_") {
			continue
		}
		faultDir := filepath.Join(dir, entry.Name())
		f, err := loadFault(faultDir)
		if err != nil {
			e.loadErrors[entry.Name()] = err
			continue
		}
		if f.Name != entry.Name() {
			e.loadErrors[entry.Name()] = fmt.Errorf("fault name %q must match its directory name %q", f.Name, entry.Name())
			continue
		}
		e.faults[f.Name] = f
	}
}

func loadFault(dir string) (*Fault, error) {
	for _, file := range contractFiles {
		if _, err := os.Stat(filepath.Join(dir, file)); err != nil {
			return nil, fmt.Errorf("fault contract violated: %s is missing (see incidents/README.md)", file)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "fault.yaml"))
	if err != nil {
		return nil, err
	}
	var f Fault
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parsing fault.yaml: %w", err)
	}
	if err := f.Validate(); err != nil {
		return nil, err
	}
	if f.Detection.Script != "" {
		if _, err := os.Stat(filepath.Join(dir, f.Detection.Script)); err != nil {
			return nil, fmt.Errorf("detection script %q not found in the fault directory", f.Detection.Script)
		}
	}
	// A fault that promises a page must ship the rule that fires it.
	if f.ExpectAlert != "" {
		if _, err := os.Stat(filepath.Join(dir, "alerts", "rule.yaml")); err != nil {
			return nil, fmt.Errorf("fault declares expectAlert %q but has no alerts/rule.yaml", f.ExpectAlert)
		}
	}
	f.Dir = dir
	return &f, nil
}

// List returns all faults, sorted by name.
func (e *Engine) List() []*Fault { return e.ListBound(e.Workload) }

// ListBound is List with templates expanded for the given workload instead of
// the engine's own. It does not modify the engine, so it needs no lock.
func (e *Engine) ListBound(bound workload.Workload) []*Fault {
	out := make([]*Fault, 0, len(e.faults))
	for _, f := range e.faults {
		out = append(out, e.resolvedFor(f, bound))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get returns a fault by name.
func (e *Engine) Get(name string) (*Fault, error) {
	f, ok := e.faults[name]
	if !ok {
		return nil, fmt.Errorf("fault %q not found (see 'labctl incident list')", name)
	}
	return e.resolved(f), nil
}

// resolved returns a copy of the fault with every field shown to a reader
// expanded for the current workload binding. The binding is chosen after the
// faults are loaded, so this cannot happen at load time. Everything the UI and
// CLI display goes through here; add any new displayed field to resolvedFor.
func (e *Engine) resolved(f *Fault) *Fault { return e.resolvedFor(f, e.Workload) }

// resolvedFor is resolved for an explicit workload. It does not modify the
// engine, which other requests share.
func (e *Engine) resolvedFor(f *Fault, bound workload.Workload) *Fault {
	resolveTemplate := func(in string) string { return tmpl.Expand(in, e.templateContextFor(bound), nil) }
	c := *f
	c.DisplayName = resolveTemplate(f.DisplayName)
	c.Description = resolveTemplate(f.Description)
	c.Target = Target{
		Namespace: resolveTemplate(f.Target.Namespace),
		Workload:  resolveTemplate(f.Target.Workload),
	}
	c.Prerequisites.Apps = resolveEach(f.Prerequisites.Apps, resolveTemplate)
	if len(f.References) > 0 {
		refs := make([]scenario.Reference, len(f.References))
		for i, r := range f.References {
			r.Label = resolveTemplate(r.Label)
			r.URL = resolveTemplate(r.URL)
			r.Note = resolveTemplate(r.Note)
			refs[i] = r
		}
		c.References = refs
	}
	if len(f.Snippets) > 0 {
		snips := make([]scenario.Snippet, len(f.Snippets))
		for i, sn := range f.Snippets {
			// Include the file's content, since the UI cannot read the fault
			// directory. An unreadable file is shown without a body.
			snips[i], _ = snippet.Resolve(f.Dir, sn, resolveTemplate)
		}
		c.Snippets = snips
	}
	return &c
}

// resolveEach expands every string in a list. A nil list stays nil.
func resolveEach(in []string, resolve func(string) string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = resolve(v)
	}
	return out
}

// PinnedApps returns the app prerequisites the named fault names literally,
// such as "go-api". An entry written as {{.WorkloadName}} follows the binding
// and is not a real requirement, so it is left out. This reads the fault as
// written, before templates are expanded.
func (e *Engine) PinnedApps(name string) []string {
	out := []string{}
	f, ok := e.faults[name]
	if !ok {
		return out
	}
	for _, declared := range f.Prerequisites.Apps {
		if declared != "" && !tmpl.IsTemplated(declared) {
			out = append(out, declared)
		}
	}
	return out
}

// BindTo binds the engine to the named app and returns a function that
// restores the previous binding.
func (e *Engine) BindTo(app string) (func(), error) {
	prev := e.Workload
	restore := func() { e.Workload = prev }
	if app == "" || app == e.Workload.Name {
		return restore, nil
	}
	cfg, err := config.LoadAppConfig(e.ProjectRoot, app)
	if err != nil {
		return restore, fmt.Errorf("app %s: %w", app, err)
	}
	e.Workload = cfg.Workload()
	return restore, nil
}

// BindToActive binds the engine to the app the active incident was injected
// into, so status, hints and resolve act on that app. It returns a restore
// function, which does nothing when no incident is active.
func (e *Engine) BindToActive() func() {
	a, err := e.Active()
	if err != nil || a == nil {
		return func() {}
	}
	return e.bindToRecorded(a)
}

func (e *Engine) bindToRecorded(a *Active) func() {
	restore, _ := e.BindTo(a.App)
	return restore
}

// LoadErrors returns faults that failed to load, keyed by directory name.
func (e *Engine) LoadErrors() map[string]error {
	out := make(map[string]error, len(e.loadErrors))
	maps.Copy(out, e.loadErrors)
	return out
}

// --- active-incident state ---------------------------------------------------

func (e *Engine) activeFile() string { return filepath.Join(e.stateDir, "active.yaml") }

// Active returns the current incident, or (nil, nil) when none is active.
func (e *Engine) Active() (*Active, error) {
	data, err := os.ReadFile(e.activeFile())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var a Active
	if err := yaml.Unmarshal(data, &a); err != nil {
		return nil, fmt.Errorf("parsing active incident state: %w", err)
	}
	return &a, nil
}

func (e *Engine) saveActive(a *Active) error {
	if err := os.MkdirAll(e.stateDir, 0755); err != nil {
		return err
	}
	data, err := yaml.Marshal(a)
	if err != nil {
		return err
	}
	return os.WriteFile(e.activeFile(), data, 0644)
}

func (e *Engine) clearActive() {
	_ = os.Remove(e.activeFile())
}

// ClearActiveState clears the active incident without running resolve.sh and
// returns the fault's name, or "" if none was active. Lab reset and
// teardown use it.
func (e *Engine) ClearActiveState() string {
	a, _ := e.Active()
	e.clearActive()
	if a == nil {
		return ""
	}
	return a.Fault
}

// --- operations ----------------------------------------------------------------

// Preflight checks a fault's prerequisites before injection.
func (e *Engine) Preflight(f *Fault) error { return e.preflight(f) }

// MarkInjected records a fault as the active incident. Callers that run
// inject.sh through the run engine call it after a successful run; Inject
// does the same itself.
func (e *Engine) MarkInjected(name string, silent bool) error {
	if _, err := e.Get(name); err != nil {
		return err
	}
	return e.saveActive(&Active{Fault: name, InjectedAt: time.Now().UTC(), Silent: silent, App: e.Workload.Name})
}

// RecordScriptResolved records and clears the active incident after resolve.sh
// has run through the run engine, as Resolve does for its own run. It does
// nothing if the named fault is not the active one.
func (e *Engine) RecordScriptResolved(name, user string) {
	active, err := e.Active()
	if err != nil || active == nil || active.Fault != name {
		return
	}
	f, err := e.Get(name)
	if err != nil {
		return
	}
	e.finishRun(active, f, "auto", user)
	e.clearActive()
}

// preflight checks the fault's prerequisites before injection.
func (e *Engine) preflight(f *Fault) error {
	var errs []string
	for _, declared := range f.Prerequisites.Apps {
		// Expanded, so a {{.WorkloadName}} prerequisite names the bound app.
		app := e.resolveTemplate(declared)
		appEnv := filepath.Join(e.ProjectRoot, "apps", app, "app.env")
		if _, err := os.Stat(appEnv); err != nil {
			errs = append(errs, fmt.Sprintf("prerequisite app %q not found (expected %s)", app, appEnv))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("preflight failed for fault %q:\n  - %s", f.Name, strings.Join(errs, "\n  - "))
	}
	return nil
}

// ResolvedTarget returns the fault's target with templates expanded for the
// current binding. A target written as {{.WorkloadNamespace}} follows the bound
// app; a literal one does not. Use it wherever the target reaches a script or
// a reader.
func (e *Engine) ResolvedTarget(f *Fault) Target {
	return Target{
		Namespace: e.resolveTemplate(f.Target.Namespace),
		Workload:  e.resolveTemplate(f.Target.Workload),
	}
}

// targetEnv returns the environment for the fault's scripts: its resolved
// target, plus the domain suffix and monitoring namespace. Detection scripts
// run on checks.Runner, which does not inherit the executor's environment, so
// they get these values only from here.
func (e *Engine) targetEnv(f *Fault) map[string]string {
	t := e.ResolvedTarget(f)
	return map[string]string{
		"TARGET_NAMESPACE":     t.Namespace,
		"TARGET_WORKLOAD":      t.Workload,
		"DOMAIN_SUFFIX":        e.DomainSuffix,
		"MONITORING_NAMESPACE": e.MonitoringNamespace,
	}
}

func (e *Engine) runFaultScript(f *Fault, script, label string, exec *executor.Executor) error {
	for k, v := range e.targetEnv(f) {
		exec.SetEnv(k, v)
	}
	rel, err := filepath.Rel(e.ProjectRoot, filepath.Join(f.Dir, script))
	if err != nil {
		rel = filepath.Join(f.Dir, script)
	}
	_, err = exec.RunScriptStreamed(label, rel)
	return err
}

// Inject runs a fault's inject.sh and records it as active.
func (e *Engine) Inject(name string, exec *executor.Executor, force, silent bool) (*Fault, error) {
	f, err := e.Get(name)
	if err != nil {
		return nil, err
	}
	if active, err := e.Active(); err != nil {
		return nil, err
	} else if active != nil && !force {
		return nil, fmt.Errorf("%w: %s (resolve it first, or use --force)", ErrIncidentActive, active.Fault)
	}
	if err := e.preflight(f); err != nil {
		return nil, err
	}
	if err := e.runFaultScript(f, "inject.sh", "Inject incident: "+f.Name, exec); err != nil {
		return f, fmt.Errorf("injecting %s: %w", f.Name, err)
	}
	if err := e.saveActive(&Active{Fault: f.Name, InjectedAt: time.Now().UTC(), Silent: silent, App: e.Workload.Name}); err != nil {
		return f, fmt.Errorf("recording active incident: %w", err)
	}
	return f, nil
}

// Eligible returns faults whose prerequisites are currently met.
func (e *Engine) Eligible() []*Fault {
	var out []*Fault
	for _, f := range e.List() {
		if e.preflight(f) == nil {
			out = append(out, f)
		}
	}
	return out
}

// PickRandom picks a random eligible fault, optionally from one category. A
// non-zero seed makes the pick repeatable, so a team can all get the same one.
func (e *Engine) PickRandom(seed int64, category string) (*Fault, error) {
	var pool []*Fault
	for _, f := range e.Eligible() {
		if category == "" || f.Category == category {
			pool = append(pool, f)
		}
	}
	if len(pool) == 0 {
		return nil, fmt.Errorf("no eligible faults%s — are the demo apps deployed?", catSuffix(category))
	}
	if seed == 0 {
		seed = time.Now().UnixNano()
	}
	// A simple LCG, so the same seed picks the same fault on every Go version.
	idx := int((seed*6364136223846793005 + 1442695040888963407) % int64(len(pool)))
	if idx < 0 {
		idx = -idx
	}
	return pool[idx], nil
}

func catSuffix(category string) string {
	if category == "" {
		return ""
	}
	return " in category " + category
}

// StatusResult is the outcome of a resolution probe.
type StatusResult struct {
	Active   *Active       `json:"active"`
	Fault    *Fault        `json:"fault"`
	Check    checks.Result `json:"check"`
	Resolved bool          `json:"resolved"`
	// Alert is set when the fault declares expectAlert: did the page fire?
	Alert *AlertStatus `json:"alert,omitempty"`
}

// Status runs the active fault's detection check. When it passes, the
// incident is recorded as resolved and cleared. user is the authenticated API
// user, or "" to use the OS username.
func (e *Engine) Status(ctx context.Context, runner *checks.Runner, user string) (*StatusResult, error) {
	active, err := e.Active()
	if err != nil {
		return nil, err
	}
	if active == nil {
		return nil, ErrNoActive
	}
	// Check the workload the fault was injected into. A check pointed at the
	// wrong namespace would pass and wrongly mark the incident solved.
	defer e.bindToRecorded(active)()
	e.injectedAt = active.InjectedAt
	defer func() { e.injectedAt = time.Time{} }()
	f, err := e.Get(active.Fault)
	if err != nil {
		return nil, fmt.Errorf("active incident %q no longer exists in the library: %w", active.Fault, err)
	}

	// The first status call counts as detection in the MTTR record.
	if active.FirstCheckedAt.IsZero() {
		active.FirstCheckedAt = time.Now().UTC()
		if err := e.saveActive(active); err != nil {
			return nil, fmt.Errorf("recording first check: %w", err)
		}
	}

	runner.ScriptDir = f.Dir
	for k, v := range e.targetEnv(f) {
		runner.Env = append(runner.Env, k+"="+v)
	}
	result := runner.Run(ctx, e.resolveCheck(f.Detection))

	res := &StatusResult{Active: active, Fault: f, Check: result, Resolved: result.Pass}
	if f.ExpectAlert != "" {
		res.Alert = queryAlert(ctx, runner.HTTPClient, e.AlertmanagerURL, f.ExpectAlert)
	}
	if result.Pass {
		e.finishRun(active, f, "manual", user)
		e.clearActive()
	}
	return res, nil
}

// Resolve runs resolve.sh to restore the lab. An empty name resolves the
// active incident; a named fault can be resolved even if no incident is
// recorded as active.
func (e *Engine) Resolve(name string, exec *executor.Executor, user string) (*Fault, error) {
	if name == "" {
		active, err := e.Active()
		if err != nil {
			return nil, err
		}
		if active == nil {
			return nil, fmt.Errorf("%w (pass a fault name to force-resolve anyway)", ErrNoActive)
		}
		name = active.Fault
	}
	if active, _ := e.Active(); active != nil && active.Fault == name {
		defer e.bindToRecorded(active)()
	}
	f, err := e.Get(name)
	if err != nil {
		return nil, err
	}
	if err := e.runFaultScript(f, "resolve.sh", "Resolve incident: "+f.Name, exec); err != nil {
		return f, fmt.Errorf("resolving %s: %w", f.Name, err)
	}
	if active, _ := e.Active(); active != nil && active.Fault == f.Name {
		// Resolving this way fixes the lab but records the run as not solved.
		e.finishRun(active, f, "auto", user)
		e.clearActive()
	}
	return f, nil
}

// ResolveCheck returns check with its template variables expanded, as the
// engine does before running a detection check. Callers outside this package
// that run a fault's check must use it.
func (e *Engine) ResolveCheck(c checks.Check) checks.Check {
	return e.resolveCheck(c)
}

// resolveCheck resolves template variables in the detection check's fields.
func (e *Engine) resolveCheck(c checks.Check) checks.Check {
	c.URL = e.resolveTemplate(c.URL)
	c.BodyContains = e.resolveTemplate(c.BodyContains)
	c.Resource = e.resolveTemplate(c.Resource)
	c.Namespace = e.resolveTemplate(c.Namespace)
	c.Query = e.resolveTemplate(c.Query)
	c.Value = e.resolveTemplate(c.Value)
	return c
}

// ResolveTemplate expands template variables in an incident's display fields,
// such as references and snippets, like the scenario engine's method of the
// same name.
func (e *Engine) ResolveTemplate(input string) string {
	return e.resolveTemplate(input)
}

func (e *Engine) resolveTemplate(input string) string {
	return tmpl.Expand(input, e.templateContext(), nil)
}

// templateContext returns the template variables for the engine's current
// binding. Faults can use the same variables as scenarios.
func (e *Engine) templateContext() tmpl.Context {
	return e.templateContextFor(e.Workload)
}

// templateContextFor is templateContext for an explicit workload.
func (e *Engine) templateContextFor(bound workload.Workload) tmpl.Context {
	w := bound.WithDefaults()
	return tmpl.Context{
		DomainSuffix:        e.DomainSuffix,
		MonitoringNamespace: e.MonitoringNamespace,
		ProjectRoot:         e.ProjectRoot,
		IngressClass:        "traefik",
		WorkloadName:        w.Name,
		WorkloadNamespace:   w.Namespace,
		WorkloadService:     w.Service(),
		WorkloadPort:        w.Port,
		WorkloadMetric:      w.Metric,
		SinceActivation:     tmpl.Since(e.injectedAt, time.Now()),
	}
}

// Clone returns a shallow copy that shares the scanned faults but owns its own
// binding.
//
// The API server shares one engine across requests, so a request that needs a
// different binding works on a clone. Sharing the faults map is safe because it
// is never modified after loading, and active state is on disk.
func (e *Engine) Clone() *Engine {
	c := *e
	return &c
}
