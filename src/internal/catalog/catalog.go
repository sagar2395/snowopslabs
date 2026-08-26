// SPDX-License-Identifier: Apache-2.0

// Package catalog is SnowOps Labs's unified content model. It discovers, decodes,
// validates, cross-references and indexes every declarative content item
// (scenarios, incidents, learning paths, challenges) from one or more content
// roots, and resolves the typed templates content authors use.
//
// It is the authority behind `labctl validate` (W2). It reuses the public
// content data types (pkg/scenario, and the incident/learn/challenge types) and
// their per-item Validate methods, so validation never drifts from what the
// runtime engines accept, and layers cross-reference integrity and typed
// template resolution on top. Loaders here never execute content — that is the
// run engine's job (golden rule 7).
package catalog

import (
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/sagar2395/snowopslabs/internal/challenge"
	"github.com/sagar2395/snowopslabs/internal/incident"
	"github.com/sagar2395/snowopslabs/internal/learn"
	"github.com/sagar2395/snowopslabs/pkg/scenario"
)

// Kind names a content type. The string values match sdk/schemas ByKind and the
// directory each kind lives under (scenarios/, incidents/, learn/, challenges/).
type Kind string

const (
	KindScenario  Kind = "scenario"
	KindIncident  Kind = "incident"
	KindPath      Kind = "path"
	KindChallenge Kind = "challenge"
)

// AllKinds lists every content kind in a stable order.
var AllKinds = []Kind{KindScenario, KindIncident, KindPath, KindChallenge}

// Catalog is an immutable, validated snapshot of all content across the loaded
// roots. Build one with Load; never mutate it in place — hot reload swaps a
// whole new Catalog atomically (see Store).
type Catalog struct {
	// Roots are the content roots this snapshot was built from, in precedence
	// order (later roots override earlier ones on a name collision, so an
	// external root can shadow in-repo content — see SNOWOPS_CONTENT_PATH).
	Roots []string

	scenarios  map[string]*scenario.Scenario
	incidents  map[string]*incident.Fault
	paths      map[string]*learn.Path
	challenges map[string]*challenge.Challenge

	// sources records the file each item was loaded from, keyed by "kind/name".
	sources map[string]string
	// nodes retains the decoded YAML document per item, keyed by "kind/name",
	// so cross-reference problems can report the exact line of a bad reference.
	nodes map[string]*yaml.Node

	// problems is every validation and cross-reference error found while
	// building this snapshot. A Catalog with problems is still returned so
	// callers can report all of them at once; IsValid reports emptiness.
	problems []Problem
}

func newCatalog(roots []string) *Catalog {
	return &Catalog{
		Roots:      roots,
		scenarios:  map[string]*scenario.Scenario{},
		incidents:  map[string]*incident.Fault{},
		paths:      map[string]*learn.Path{},
		challenges: map[string]*challenge.Challenge{},
		sources:    map[string]string{},
		nodes:      map[string]*yaml.Node{},
	}
}

func sourceKey(kind Kind, name string) string { return string(kind) + "/" + name }

// Problems returns every validation and cross-reference error, sorted for stable
// output, that was found while building the snapshot.
func (c *Catalog) Problems() []Problem {
	out := append([]Problem(nil), c.problems...)
	sort.Slice(out, func(i, j int) bool { return out[i].less(out[j]) })
	return out
}

// IsValid reports whether the snapshot loaded with zero problems.
func (c *Catalog) IsValid() bool { return len(c.problems) == 0 }

// Source returns the file a named item was loaded from, or "" if unknown.
func (c *Catalog) Source(kind Kind, name string) string {
	return c.sources[sourceKey(kind, name)]
}

// --- typed accessors --------------------------------------------------------

// Scenario returns a loaded scenario by name, or (nil, false).
func (c *Catalog) Scenario(name string) (*scenario.Scenario, bool) {
	s, ok := c.scenarios[name]
	return s, ok
}

// Incident returns a loaded incident/fault by name, or (nil, false).
func (c *Catalog) Incident(name string) (*incident.Fault, bool) {
	f, ok := c.incidents[name]
	return f, ok
}

// Path returns a loaded learning path by name, or (nil, false).
func (c *Catalog) Path(name string) (*learn.Path, bool) {
	p, ok := c.paths[name]
	return p, ok
}

// Challenge returns a loaded challenge by name, or (nil, false).
func (c *Catalog) Challenge(name string) (*challenge.Challenge, bool) {
	ch, ok := c.challenges[name]
	return ch, ok
}

// Scenarios returns all scenarios sorted by name.
func (c *Catalog) Scenarios() []*scenario.Scenario {
	out := make([]*scenario.Scenario, 0, len(c.scenarios))
	for _, s := range c.scenarios {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Incidents returns all incidents sorted by name.
func (c *Catalog) Incidents() []*incident.Fault {
	out := make([]*incident.Fault, 0, len(c.incidents))
	for _, f := range c.incidents {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Paths returns all learning paths sorted by name.
func (c *Catalog) Paths() []*learn.Path {
	out := make([]*learn.Path, 0, len(c.paths))
	for _, p := range c.paths {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Challenges returns all challenges sorted by name.
func (c *Catalog) Challenges() []*challenge.Challenge {
	out := make([]*challenge.Challenge, 0, len(c.challenges))
	for _, ch := range c.challenges {
		out = append(out, ch)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Counts returns the number of loaded items per kind.
func (c *Catalog) Counts() map[Kind]int {
	return map[Kind]int{
		KindScenario:  len(c.scenarios),
		KindIncident:  len(c.incidents),
		KindPath:      len(c.paths),
		KindChallenge: len(c.challenges),
	}
}
