// SPDX-License-Identifier: Apache-2.0

// Package scenario defines the public, versioned schema for a SnowOps Labs
// scenario (scenario.yaml) and its validation. It is part of the stable SDK
// surface (see docs/authoring/sdk-stability-policy.md): external pack authors
// and tools depend on these types and on the JSON Schema generated alongside
// them. It must not import anything under internal/.
package scenario

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/sagar2395/snowopslabs/pkg/checks"
)

// DefaultScenarioAPIVersion is the schema version assumed when a scenario
// omits apiVersion. The engine also accepts the previous version (see
// SupportedScenarioAPIVersions) and rejects any other.
const DefaultScenarioAPIVersion = "scenario.snowops.net/v2"

// SupportedScenarioAPIVersions lists the schema versions this engine understands
// (newest first). An empty apiVersion in a scenario is treated as the default.
var SupportedScenarioAPIVersions = []string{
	"scenario.snowops.net/v2",
}

// APIVersionSupported reports whether the engine understands the given scenario
// schema apiVersion. An empty value means "use the default" and is supported.
func APIVersionSupported(v string) bool {
	return v == "" || slices.Contains(SupportedScenarioAPIVersions, v)
}

var validComponentTypes = map[string]bool{
	"helm": true, "manifest": true, "grafana-dashboard": true, "script": true,
}

// Scenario represents a lab scenario loaded from scenario.yaml.
//
// Format v1 declares a flat `components` list. Format v2 may instead group
// components into ordered `stages`, and add human-readable `objectives` and
// machine-verifiable `checks` (run by `labctl scenario verify`). A scenario
// must use either `components` or `stages`, never both.
type Scenario struct {
	// APIVersion is the scenario schema version. It is optional; an empty
	// value means DefaultScenarioAPIVersion. See SupportedScenarioAPIVersions.
	APIVersion string `yaml:"apiVersion,omitempty" json:"apiVersion,omitempty"`

	Name        string `yaml:"name" json:"name"`
	DisplayName string `yaml:"displayName" json:"displayName"`
	Description string `yaml:"description" json:"description"`
	Category    string `yaml:"category" json:"category"`
	// Verified marks a scenario tested end to end on a fresh cluster. The UI
	// and CLI flag scenarios without it.
	Verified      bool          `yaml:"verified,omitempty" json:"verified"`
	Prerequisites Prerequisites `yaml:"prerequisites" json:"prerequisites"`
	Runtimes      []string      `yaml:"runtimes" json:"runtimes"`
	Components    []Component   `yaml:"components" json:"components"`
	Explore       Explore       `yaml:"explore" json:"explore"`

	// Format v2 (optional)
	Objectives []string       `yaml:"objectives,omitempty" json:"objectives,omitempty"`
	Stages     []Stage        `yaml:"stages,omitempty" json:"stages,omitempty"`
	Checks     []checks.Check `yaml:"checks,omitempty" json:"checks,omitempty"`

	// References link to the documentation of the tools behind the scenario.
	// Snippets are manifest fragments the learner can apply while working
	// through it.
	References []Reference `yaml:"references,omitempty" json:"references,omitempty"`
	Snippets   []Snippet   `yaml:"snippets,omitempty" json:"snippets,omitempty"`

	// Parameters are values the learner can set at activation, such as an
	// autoscaler's replica limits. Manifests refer to them as {{.Name}}.
	Parameters []Parameter `yaml:"parameters,omitempty" json:"parameters,omitempty"`

	// Runtime fields (not from YAML)
	Dir    string `yaml:"-" json:"-"`
	Active bool   `yaml:"-" json:"active"`
	Source string `yaml:"-" json:"source,omitempty"` // "" = in-repo; else the catalog pack name
}

// Stage is an ordered group of components that can be reasoned about
// independently (baseline, inject-failure, …). Stages install in declaration
// order; uninstall runs in reverse.
type Stage struct {
	Name        string      `yaml:"name" json:"name"`
	Description string      `yaml:"description,omitempty" json:"description,omitempty"`
	Components  []Component `yaml:"components" json:"components"`
}

// Prerequisites defines what must be running before a scenario can activate.
type Prerequisites struct {
	Platform []string `yaml:"platform" json:"platform"`
	Apps     []string `yaml:"apps" json:"apps"`
	// Capabilities the bound workload must declare, such as
	// prometheus-metrics, so the scenario can run against any app that has
	// them. Valid names are defined in internal/workload.
	Capabilities []string `yaml:"capabilities,omitempty" json:"capabilities,omitempty"`
}

// Component defines a single deployable unit within a scenario.
type Component struct {
	Name       string            `yaml:"name" json:"name"`
	Type       string            `yaml:"type" json:"type"` // helm, manifest, grafana-dashboard, script
	Chart      string            `yaml:"chart,omitempty" json:"chart,omitempty"`
	Repo       string            `yaml:"repo,omitempty" json:"repo,omitempty"`
	Version    string            `yaml:"version,omitempty" json:"version,omitempty"`
	Namespace  string            `yaml:"namespace,omitempty" json:"namespace,omitempty"`
	ValuesFile string            `yaml:"valuesFile,omitempty" json:"valuesFile,omitempty"`
	Path       string            `yaml:"path,omitempty" json:"path,omitempty"`
	Set        map[string]string `yaml:"set,omitempty" json:"set,omitempty"`
	Script     string            `yaml:"script,omitempty" json:"script,omitempty"`

	// UninstallScript undoes a script component's changes on `scenario down`.
	UninstallScript string `yaml:"uninstallScript,omitempty" json:"uninstallScript,omitempty"`

	// PlatformValues names a platform component ("logging/loki"), whose
	// values.yaml is used as the base values, or a specific file
	// ("logging/loki/promtail-values.yaml"). ValuesFile is applied on top.
	// Use it instead of copying the platform's values into the scenario.
	PlatformValues string `yaml:"platformValues,omitempty" json:"platformValues,omitempty"`

	// Adopt uses an already-installed Helm release as it is instead of
	// upgrading it, and teardown leaves it in place.
	Adopt bool `yaml:"adopt,omitempty" json:"adopt,omitempty"`
}

// Explore contains hints for the user on how to explore the scenario.
type Explore struct {
	URLs     []ExploreURL     `yaml:"urls" json:"urls"`
	Commands []ExploreCommand `yaml:"commands" json:"commands"`
	Tips     []string         `yaml:"tips" json:"tips"`
}

// ExploreURL is a URL hint.
type ExploreURL struct {
	Label string `yaml:"label" json:"label"`
	URL   string `yaml:"url" json:"url"`
}

// ExploreCommand is a command hint.
type ExploreCommand struct {
	Label   string `yaml:"label" json:"label"`
	Command string `yaml:"command" json:"command"`
}

// Reference is a documentation link for a scenario or incident.
type Reference struct {
	Label string `yaml:"label" json:"label"`
	URL   string `yaml:"url" json:"url"`
	Note  string `yaml:"note,omitempty" json:"note,omitempty"`
}

// Snippet is a manifest fragment shown to the learner by a scenario or
// incident. Exactly one of YAML (inline) or Path (a file in the item's
// directory) is set. Templates in it are expanded before display.
type Snippet struct {
	Label       string `yaml:"label" json:"label"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	YAML        string `yaml:"yaml,omitempty" json:"yaml,omitempty"`
	Path        string `yaml:"path,omitempty" json:"path,omitempty"`
	// Apply is the complete command that uses the snippet. Empty means it is a
	// manifest piped into "kubectl apply -f -".
	Apply string `yaml:"apply,omitempty" json:"apply,omitempty"`
	// Exercise marks a snippet nothing installs: applying it is the learner's task.
	Exercise bool `yaml:"exercise,omitempty" json:"exercise,omitempty"`
	// ApplyCommand is filled for display only: a ready-to-run command for an
	// exercise, with the manifest inlined when Apply is empty.
	ApplyCommand string `yaml:"-" json:"applyCommand,omitempty"`
}

// Parameter is a value the learner can set when activating a scenario.
// Manifests refer to it as {{.Name}}, so Name may contain only letters,
// digits and underscores.
type Parameter struct {
	Name        string `yaml:"name" json:"name"`
	DisplayName string `yaml:"displayName,omitempty" json:"displayName,omitempty"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	// Default is used when the user overrides nothing. Required.
	Default string `yaml:"default" json:"default"`
	// Type is "int" or "string" (default "string"). An int parameter is bounds-
	// checked against Min/Max and parsed before substitution.
	Type string `yaml:"type,omitempty" json:"type,omitempty"`
	// Min/Max are inclusive bounds for an int parameter (ignored for strings).
	Min *int `yaml:"min,omitempty" json:"min,omitempty"`
	Max *int `yaml:"max,omitempty" json:"max,omitempty"`
	// NotGreaterThan names another int parameter this one must not exceed
	// (e.g. minReplicas ≤ maxReplicas), enforced against the effective values.
	NotGreaterThan string `yaml:"notGreaterThan,omitempty" json:"notGreaterThan,omitempty"`
}

// IsInt reports whether the parameter is an integer parameter.
func (p Parameter) IsInt() bool { return p.Type == "int" }

// ValidateValue checks a value against the parameter's type and (for ints) its
// Min/Max bounds. label names the value's origin in messages ("default" or
// "override").
func (p Parameter) ValidateValue(raw, label string) error {
	if !p.IsInt() {
		return nil // string parameters accept any value
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("parameter %q %s %q is not an integer", p.Name, label, raw)
	}
	if p.Min != nil && n < *p.Min {
		return fmt.Errorf("parameter %q %s %d is below the minimum %d", p.Name, label, n, *p.Min)
	}
	if p.Max != nil && n > *p.Max {
		return fmt.Errorf("parameter %q %s %d is above the maximum %d", p.Name, label, n, *p.Max)
	}
	return nil
}

var validParamTypes = map[string]bool{"": true, "string": true, "int": true}

// ValidateParameters reports every structural problem in a parameter list: each
// needs a template-safe name, unique across the list; a type from the allowed
// set; a Default that satisfies its own type/bounds; and any NotGreaterThan must
// reference another declared int parameter.
func ValidateParameters(params []Parameter) []string {
	var errs []string
	names := map[string]bool{}
	for i, p := range params {
		where := fmt.Sprintf("parameter %d", i+1)
		if p.Name != "" {
			where = fmt.Sprintf("parameter %q", p.Name)
		}
		switch {
		case strings.TrimSpace(p.Name) == "":
			errs = append(errs, fmt.Sprintf("%s: name is required", where))
		case !templateIdent.MatchString(p.Name):
			errs = append(errs, fmt.Sprintf("%s: name must be letters, digits or underscore (used as a {{.Name}} template var)", where))
		case names[p.Name]:
			errs = append(errs, fmt.Sprintf("%s: duplicate parameter name", where))
		}
		names[p.Name] = true
		if !validParamTypes[p.Type] {
			errs = append(errs, fmt.Sprintf("%s: unknown type %q (expected int or string)", where, p.Type))
		}
		if strings.TrimSpace(p.Default) == "" {
			errs = append(errs, fmt.Sprintf("%s: default is required", where))
		} else if err := p.ValidateValue(p.Default, "default"); err != nil {
			errs = append(errs, err.Error())
		}
	}
	// NotGreaterThan references are checked after all names are known.
	for _, p := range params {
		if p.NotGreaterThan == "" {
			continue
		}
		if !names[p.NotGreaterThan] {
			errs = append(errs, fmt.Sprintf("parameter %q: notGreaterThan references unknown parameter %q", p.Name, p.NotGreaterThan))
		}
	}
	return errs
}

// templateIdent matches a parameter name usable as a {{.Name}} template var.
var templateIdent = regexp.MustCompile(`^\w+$`)

// ValidateReferences reports every problem in a reference list. where, such as
// "reference", prefixes each message.
func ValidateReferences(refs []Reference) []string {
	var errs []string
	for i, r := range refs {
		at := fmt.Sprintf("reference %d", i+1)
		if strings.TrimSpace(r.Label) != "" {
			at = fmt.Sprintf("reference %q", r.Label)
		}
		if strings.TrimSpace(r.Label) == "" {
			errs = append(errs, at+": label is required")
		}
		if strings.TrimSpace(r.URL) == "" {
			errs = append(errs, at+": url is required")
		} else if !strings.HasPrefix(r.URL, "http://") && !strings.HasPrefix(r.URL, "https://") {
			errs = append(errs, fmt.Sprintf("%s: url %q must start with http:// or https://", at, r.URL))
		}
	}
	return errs
}

// ValidateSnippets reports every problem in a snippet list: each snippet needs
// a label and exactly one of yaml or path, and a path must stay inside the
// item's directory. It does not check that the file exists.
func ValidateSnippets(snips []Snippet) []string {
	var errs []string
	for i, s := range snips {
		at := fmt.Sprintf("snippet %d", i+1)
		if strings.TrimSpace(s.Label) != "" {
			at = fmt.Sprintf("snippet %q", s.Label)
		}
		if strings.TrimSpace(s.Label) == "" {
			errs = append(errs, at+": label is required")
		}
		hasYAML := strings.TrimSpace(s.YAML) != ""
		hasPath := strings.TrimSpace(s.Path) != ""
		switch {
		case hasYAML && hasPath:
			errs = append(errs, at+": set either yaml or path, not both")
		case !hasYAML && !hasPath:
			errs = append(errs, at+": one of yaml or path is required")
		}
		if hasPath && unsafePath(s.Path) {
			errs = append(errs, fmt.Sprintf("%s: path %q must be a relative path inside the item directory", at, s.Path))
		}
	}
	return errs
}

// AllComponents returns the scenario's components in install order,
// regardless of whether they are declared flat (v1) or in stages (v2).
func (s *Scenario) AllComponents() []Component {
	if len(s.Stages) == 0 {
		return s.Components
	}
	var all []Component
	for _, st := range s.Stages {
		all = append(all, st.Components...)
	}
	return all
}

// StagesOrDefault returns the stage list, wrapping a v1 flat component list
// in a single anonymous stage so install logic has one code path.
func (s *Scenario) StagesOrDefault() []Stage {
	if len(s.Stages) > 0 {
		return s.Stages
	}
	return []Stage{{Components: s.Components}}
}

// unsafePath reports whether a scenario asset path could escape the
// scenario directory (absolute, or containing a ".." segment).
func unsafePath(p string) bool {
	if p == "" {
		return false
	}
	if filepath.IsAbs(p) {
		return true
	}
	for seg := range strings.SplitSeq(filepath.ToSlash(p), "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

// Validate reports every schema problem in the scenario at once, naming the
// offending fields. It accepts all valid v1 scenarios unchanged.
func (s *Scenario) Validate() error {
	var errs []string
	add := func(format string, args ...any) {
		errs = append(errs, fmt.Sprintf(format, args...))
	}

	if strings.TrimSpace(s.Name) == "" {
		add("name is required")
	}
	if !APIVersionSupported(s.APIVersion) {
		add("unsupported apiVersion %q (supported: %s)",
			s.APIVersion, strings.Join(SupportedScenarioAPIVersions, ", "))
	}
	if len(s.Components) > 0 && len(s.Stages) > 0 {
		add("declare either components (v1) or stages (v2), not both")
	}

	stageNames := map[string]bool{}
	for i, st := range s.Stages {
		switch {
		case strings.TrimSpace(st.Name) == "":
			add("stage %d: name is required", i+1)
		case stageNames[st.Name]:
			add("stage %q: duplicate stage name", st.Name)
		default:
			stageNames[st.Name] = true
		}
	}

	compNames := map[string]bool{}
	for _, c := range s.AllComponents() {
		where := fmt.Sprintf("component %q", c.Name)
		if strings.TrimSpace(c.Name) == "" {
			add("component with empty name")
			continue
		}
		if compNames[c.Name] {
			add("%s: duplicate component name", where)
		}
		compNames[c.Name] = true
		switch {
		case !validComponentTypes[c.Type]:
			add("%s: unknown type %q (expected helm | manifest | grafana-dashboard | script)", where, c.Type)
		case c.Type == "helm" && c.Chart == "":
			add("%s: helm component requires chart", where)
		case (c.Type == "manifest" || c.Type == "grafana-dashboard") && c.Path == "":
			add("%s: %s component requires path", where, c.Type)
		case c.Type == "script" && c.Script == "":
			add("%s: script component requires script", where)
		}
		// Asset paths must stay inside the scenario directory; packs from
		// external sources are untrusted.
		for field, p := range map[string]string{"valuesFile": c.ValuesFile, "path": c.Path, "script": c.Script, "uninstallScript": c.UninstallScript} {
			if unsafePath(p) {
				add("%s: %s %q must be a relative path inside the scenario directory", where, field, p)
			}
		}
		if unsafePath(c.PlatformValues) {
			add("%s: platformValues %q must be a relative path under platform/ (e.g. \"logging/loki\")", where, c.PlatformValues)
		}
	}

	checkNames := map[string]bool{}
	for _, c := range s.Checks {
		if err := c.Validate(); err != nil {
			add("%s", err.Error())
		}
		if c.Name != "" {
			if checkNames[c.Name] {
				add("check %q: duplicate check name", c.Name)
			}
			checkNames[c.Name] = true
		}
		if unsafePath(c.Script) {
			add("check %q: script %q must be a relative path inside the scenario directory", c.Name, c.Script)
		}
	}

	errs = append(errs, ValidateReferences(s.References)...)
	errs = append(errs, ValidateSnippets(s.Snippets)...)
	errs = append(errs, ValidateParameters(s.Parameters)...)

	if len(errs) > 0 {
		name := s.Name
		if name == "" {
			name = "(unnamed)"
		}
		return fmt.Errorf("invalid scenario %q:\n  - %s", name, strings.Join(errs, "\n  - "))
	}
	return nil
}
