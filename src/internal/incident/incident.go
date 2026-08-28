// SPDX-License-Identifier: Apache-2.0
// Package incident implements the fault library engine (tasks 045/046):
// discovery and validation of incidents/<name>/ fault definitions, injection
// and resolution through their scripts, and resolution detection via the
// shared checks primitive. Faults are declarative content — this package
// never encodes fault logic in Go (golden rules 2 and 9).
package incident

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/sagar2395/snowopslabs/internal/executor"
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
	// Verified marks the incident confirmed end-to-end on a fresh cluster
	// (W4-T07). Absent/false is unverified.
	Verified      bool          `yaml:"verified,omitempty" json:"verified"`
	Category      string        `yaml:"category" json:"category"` // workload | network | resources | storage | config
	Severity      string        `yaml:"severity" json:"severity"` // low | medium | high
	Target        Target        `yaml:"target" json:"target"`
	Prerequisites Prerequisites `yaml:"prerequisites" json:"prerequisites"`
	Detection     checks.Check  `yaml:"detection" json:"detection"` // passes ⇔ the fault is RESOLVED
	// ExpectAlert names the Alertmanager alert this fault should fire (task
	// 049). inject.sh arms the matching PrometheusRule from alerts/rule.yaml;
	// `incident status` reports whether the page went out.
	ExpectAlert string `yaml:"expectAlert,omitempty" json:"expectAlert,omitempty"`

	// References and Snippets (M2) present upstream docs and applyable manifest
	// fragments to whoever is working the incident. They reuse the shared SDK
	// types so scenarios and incidents display them identically.
	References []scenario.Reference `yaml:"references,omitempty" json:"references,omitempty"`
	Snippets   []scenario.Snippet   `yaml:"snippets,omitempty" json:"snippets,omitempty"`

	Dir string `yaml:"-" json:"-"`
}

// Target names what the fault breaks; exported to scripts as
// TARGET_NAMESPACE / TARGET_WORKLOAD.
type Target struct {
	Namespace string `yaml:"namespace" json:"namespace"`
	Workload  string `yaml:"workload" json:"workload"`
}

// Prerequisites name what must already be present for the fault to inject,
// detect and resolve correctly. Platform components (e.g. "ingress" when the
// detection check calls the app through the ingress) are not auto-installed —
// the UI surfaces them so the user installs them first.
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
	add := func(format string, args ...interface{}) {
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
	// FirstCheckedAt is when `incident status` first ran — the proxy for
	// time-to-detect in the run record (task 048).
	FirstCheckedAt time.Time `yaml:"firstCheckedAt,omitempty" json:"firstCheckedAt,omitempty"`
}

// Engine discovers faults and manages the (single) active incident.
type Engine struct {
	ProjectRoot  string
	DomainSuffix string
	// AlertmanagerURL is where Status queries fired alerts (task 049).
	// Callers set it from ALERTMANAGER_URL or the ingress default.
	AlertmanagerURL string
	faults          map[string]*Fault
	loadErrors      map[string]error
	stateDir        string
}

// NewEngine scans incidents/ under the project root.
func NewEngine(projectRoot, domainSuffix string) *Engine {
	e := &Engine{
		ProjectRoot:  projectRoot,
		DomainSuffix: domainSuffix,
		faults:       make(map[string]*Fault),
		loadErrors:   make(map[string]error),
		stateDir:     filepath.Join(projectRoot, ".labctl", "incidents"),
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
		if !entry.IsDir() {
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
func (e *Engine) List() []*Fault {
	var out []*Fault
	for _, f := range e.faults {
		out = append(out, f)
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
	return f, nil
}

// LoadErrors returns faults that failed to load, keyed by directory name.
func (e *Engine) LoadErrors() map[string]error {
	out := make(map[string]error, len(e.loadErrors))
	for k, v := range e.loadErrors {
		out[k] = v
	}
	return out
}

// --- active-incident state ---------------------------------------------------

func (e *Engine) activeFile() string { return filepath.Join(e.stateDir, "active.yaml") }

// Active returns the current incident, or (nil, nil) when none is active.
func (e *Engine) Active() (*Active, error) {
	data, err := os.ReadFile(e.activeFile())
	if err != nil {
		if os.IsNotExist(err) {
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

// --- operations ----------------------------------------------------------------

// Preflight checks a fault's prerequisites before injection. Exported so the
// durable inject path (which submits inject.sh through the run engine) can gate
// itself with exactly the checks Inject applies.
func (e *Engine) Preflight(f *Fault) error { return e.preflight(f) }

// MarkInjected records a fault as the active incident. The durable inject path
// runs inject.sh through the run engine and, on success, calls this to persist
// the active-incident state that hints, status and scoring read — the state
// Inject writes inline on the legacy path.
func (e *Engine) MarkInjected(name string, silent bool) error {
	if _, err := e.Get(name); err != nil {
		return err
	}
	return e.saveActive(&Active{Fault: name, InjectedAt: time.Now().UTC(), Silent: silent})
}

// RecordScriptResolved scores and clears the active incident after resolve.sh
// has run through the engine — the durable analogue of the tail of Resolve. It
// is a no-op when the named fault is not the active one, so force-resolving an
// already-clean lab is harmless.
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
	for _, app := range f.Prerequisites.Apps {
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

// targetEnv exports the fault's target to its scripts.
func (f *Fault) targetEnv() map[string]string {
	return map[string]string{
		"TARGET_NAMESPACE": f.Target.Namespace,
		"TARGET_WORKLOAD":  f.Target.Workload,
	}
}

func (e *Engine) runFaultScript(f *Fault, script, label string, exec *executor.Executor) error {
	for k, v := range f.targetEnv() {
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
	if err := e.saveActive(&Active{Fault: f.Name, InjectedAt: time.Now().UTC(), Silent: silent}); err != nil {
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

// PickRandom selects a random eligible fault, optionally filtered by
// category. A non-zero seed makes the pick reproducible for team exercises.
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
	// Small deterministic LCG keeps the pick reproducible across runs
	// without importing math/rand semantics that may change between Go versions.
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
// incident is marked resolved and the active state cleared.
// user attributes an auto-detected resolution record to the authenticated API
// user (task 062); pass "" from the CLI to fall back to the OS username.
func (e *Engine) Status(ctx context.Context, runner *checks.Runner, user string) (*StatusResult, error) {
	active, err := e.Active()
	if err != nil {
		return nil, err
	}
	if active == nil {
		return nil, ErrNoActive
	}
	f, err := e.Get(active.Fault)
	if err != nil {
		return nil, fmt.Errorf("active incident %q no longer exists in the library: %w", active.Fault, err)
	}

	// First status call timestamps "detection" for the MTTR record.
	if active.FirstCheckedAt.IsZero() {
		active.FirstCheckedAt = time.Now().UTC()
		if err := e.saveActive(active); err != nil {
			return nil, fmt.Errorf("recording first check: %w", err)
		}
	}

	runner.ScriptDir = f.Dir
	for k, v := range f.targetEnv() {
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

// Resolve runs resolve.sh — the escape hatch. With an empty name it
// resolves the active incident; an explicit name works even if the active
// state was lost.
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
	f, err := e.Get(name)
	if err != nil {
		return nil, err
	}
	if err := e.runFaultScript(f, "resolve.sh", "Resolve incident: "+f.Name, exec); err != nil {
		return f, fmt.Errorf("resolving %s: %w", f.Name, err)
	}
	if active, _ := e.Active(); active != nil && active.Fault == f.Name {
		// The escape hatch resolves the lab but scores as a non-completion.
		e.finishRun(active, f, "auto", user)
		e.clearActive()
	}
	return f, nil
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

// ResolveTemplate expands the template variables an author may use in an
// incident's display fields (references, snippets), mirroring the scenario
// engine's method so the CLI renders both content kinds the same way.
func (e *Engine) ResolveTemplate(input string) string {
	return e.resolveTemplate(input)
}

func (e *Engine) resolveTemplate(input string) string {
	if input == "" || !strings.Contains(input, "{{") {
		return input
	}
	tmpl, err := template.New("fault").Parse(input)
	if err != nil {
		return input
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, map[string]string{
		"DomainSuffix": e.DomainSuffix,
		"ProjectRoot":  e.ProjectRoot,
	}); err != nil {
		return input
	}
	return buf.String()
}
