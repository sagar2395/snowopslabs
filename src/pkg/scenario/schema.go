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
	"strings"

	"github.com/sagar2395/snowopslabs/pkg/checks"
)

// Scenario schema versioning. The engine supports the current and previous
// schema versions; unknown versions are rejected with an actionable error so a
// pack built for a newer engine fails clearly instead of misbehaving.
const DefaultScenarioAPIVersion = "scenario.snowops.net/v2"

// SupportedScenarioAPIVersions lists the schema versions this engine understands
// (newest first). An empty apiVersion in a scenario is treated as the default.
var SupportedScenarioAPIVersions = []string{
	"scenario.snowops.net/v2",
}

// APIVersionSupported reports whether the engine understands the given scenario
// schema apiVersion. An empty value means "use the default" and is supported.
func APIVersionSupported(v string) bool {
	if v == "" {
		return true
	}
	for _, s := range SupportedScenarioAPIVersions {
		if v == s {
			return true
		}
	}
	return false
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
	// APIVersion declares the scenario schema version. Optional for backward
	// compatibility: an empty value is treated as the current default
	// (DefaultScenarioAPIVersion). The engine accepts the current and previous
	// schema versions; see SupportedScenarioAPIVersions.
	APIVersion string `yaml:"apiVersion,omitempty" json:"apiVersion,omitempty"`

	Name          string        `yaml:"name" json:"name"`
	DisplayName   string        `yaml:"displayName" json:"displayName"`
	Description   string        `yaml:"description" json:"description"`
	Category      string        `yaml:"category" json:"category"`
	Prerequisites Prerequisites `yaml:"prerequisites" json:"prerequisites"`
	Runtimes      []string      `yaml:"runtimes" json:"runtimes"`
	Components    []Component   `yaml:"components" json:"components"`
	Explore       Explore       `yaml:"explore" json:"explore"`

	// Format v2 (optional)
	Objectives []string       `yaml:"objectives,omitempty" json:"objectives,omitempty"`
	Stages     []Stage        `yaml:"stages,omitempty" json:"stages,omitempty"`
	Checks     []checks.Check `yaml:"checks,omitempty" json:"checks,omitempty"`

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
	for _, seg := range strings.Split(filepath.ToSlash(p), "/") {
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
	add := func(format string, args ...interface{}) {
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
		if strings.TrimSpace(st.Name) == "" {
			add("stage %d: name is required", i+1)
		} else if stageNames[st.Name] {
			add("stage %q: duplicate stage name", st.Name)
		} else {
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
		// Asset paths must stay inside the scenario directory — external
		// packs (task 044) are untrusted, and in-repo scenarios have no
		// business escaping their dir either.
		for field, p := range map[string]string{"valuesFile": c.ValuesFile, "path": c.Path, "script": c.Script} {
			if unsafePath(p) {
				add("%s: %s %q must be a relative path inside the scenario directory", where, field, p)
			}
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

	if len(errs) > 0 {
		name := s.Name
		if name == "" {
			name = "(unnamed)"
		}
		return fmt.Errorf("invalid scenario %q:\n  - %s", name, strings.Join(errs, "\n  - "))
	}
	return nil
}
