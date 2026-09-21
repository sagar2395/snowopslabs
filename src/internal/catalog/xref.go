// SPDX-License-Identifier: Apache-2.0

package catalog

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/sagar2395/snowopslabs/internal/incident"
	"github.com/sagar2395/snowopslabs/pkg/scenario"
)

// crossReference checks integrity between items after every item is loaded: a
// learning path or challenge that points at a scenario or incident must point at
// one that actually exists, and a challenge that grades on the detection check
// must set up an incident (only incidents have one).
func (c *Catalog) crossReference() {
	for _, p := range c.paths {
		key := sourceKey(KindPath, p.Name)
		file, node := c.sources[key], c.nodes[key]
		for i, m := range p.Modules {
			switch m.Action.Type {
			case "scenario":
				if _, ok := c.scenarios[m.Action.Ref]; !ok {
					c.refProblem(KindPath, p.Name, file, node, m.Action.Ref,
						fmt.Sprintf("module %q references unknown scenario %q", moduleLabel(m.Name, i), m.Action.Ref))
				}
			case "incident":
				if _, ok := c.incidents[m.Action.Ref]; !ok {
					c.refProblem(KindPath, p.Name, file, node, m.Action.Ref,
						fmt.Sprintf("module %q references unknown incident %q", moduleLabel(m.Name, i), m.Action.Ref))
				}
			}
		}
	}

	for _, ch := range c.challenges {
		key := sourceKey(KindChallenge, ch.Name)
		file, node := c.sources[key], c.nodes[key]
		switch ch.Setup.Type {
		case "scenario":
			if _, ok := c.scenarios[ch.Setup.Ref]; !ok {
				c.refProblem(KindChallenge, ch.Name, file, node, ch.Setup.Ref,
					fmt.Sprintf("setup references unknown scenario %q", ch.Setup.Ref))
			}
		case "incident":
			if _, ok := c.incidents[ch.Setup.Ref]; !ok {
				c.refProblem(KindChallenge, ch.Name, file, node, ch.Setup.Ref,
					fmt.Sprintf("setup references unknown incident %q", ch.Setup.Ref))
			}
		}
		if ch.Grading.UseDetectionCheck && ch.Setup.Type != "incident" {
			c.problems = append(c.problems, Problem{
				Kind: KindChallenge, Name: ch.Name, File: file,
				Message: "grading.useDetectionCheck requires setup.type: incident (only incidents have a detection check)",
			})
		}
	}

	c.checkSnippetPaths()
	c.checkScriptBindings()
	c.checkDashboards()
}

// checkSnippetPaths reports every snippet whose `path` does not point at a
// file inside the item's directory.
func (c *Catalog) checkSnippetPaths() {
	for _, s := range c.scenarios {
		key := sourceKey(KindScenario, s.Name)
		c.checkItemSnippetPaths(KindScenario, s.Name, c.sources[key], c.nodes[key], s.Dir, s.Snippets)
	}
	for _, f := range c.incidents {
		key := sourceKey(KindIncident, f.Name)
		c.checkItemSnippetPaths(KindIncident, f.Name, c.sources[key], c.nodes[key], f.Dir, f.Snippets)
	}
}

func (c *Catalog) checkItemSnippetPaths(kind Kind, name, file string, node *yaml.Node, dir string, snips []scenario.Snippet) {
	for _, sn := range snips {
		if sn.Path == "" {
			continue // inline yaml, or already reported malformed by Validate
		}
		// Validate has already rejected absolute and ".." paths.
		abs := filepath.Join(dir, filepath.FromSlash(sn.Path))
		if info, err := os.Stat(abs); err != nil || info.IsDir() {
			c.problems = append(c.problems, Problem{
				Kind: kind, Name: name, File: file, Line: lineOfValue(node, sn.Path),
				Message: fmt.Sprintf("snippet %q: path %q not found in the %s directory", snippetLabel(sn), sn.Path, kind),
			})
		}
	}
}

func snippetLabel(s scenario.Snippet) string {
	if s.Label != "" {
		return s.Label
	}
	return s.Path
}

func moduleLabel(name string, i int) string {
	if name != "" {
		return name
	}
	return fmt.Sprintf("#%d", i+1)
}

func (c *Catalog) refProblem(kind Kind, name, file string, node *yaml.Node, ref, msg string) {
	c.problems = append(c.problems, Problem{
		Kind: kind, Name: name, File: file, Line: lineOfValue(node, ref), Message: msg,
	})
}

// validateTemplates checks every templated field against the template context
// and reports unknown variables and malformed templates with file and line.
func (c *Catalog) validateTemplates(_ string) {
	for _, s := range c.scenarios {
		key := sourceKey(KindScenario, s.Name)
		// A scenario's own parameters are legal variables inside it.
		params := make([]string, 0, len(s.Parameters))
		for _, p := range s.Parameters {
			params = append(params, p.Name)
		}
		c.resolveFields(KindScenario, s.Name, c.sources[key], c.nodes[key], scenarioTemplated(s), params...)
	}
	for _, f := range c.incidents {
		key := sourceKey(KindIncident, f.Name)
		c.resolveFields(KindIncident, f.Name, c.sources[key], c.nodes[key], incidentTemplated(f))
	}
}

func (c *Catalog) resolveFields(kind Kind, name, file string, node *yaml.Node, fields []string, extra ...string) {
	for _, raw := range fields {
		if err := Validate(raw, extra...); err != nil {
			c.problems = append(c.problems, Problem{
				Kind: kind, Name: name, File: file, Line: lineOfValue(node, raw), Message: err.Error(),
			})
		}
	}
}

// scenarioTemplated returns every scenario string field that authors template.
func scenarioTemplated(s *scenario.Scenario) []string {
	out := []string{s.Description}
	out = append(out, s.Objectives...)
	out = append(out, s.Explore.Tips...)
	for _, u := range s.Explore.URLs {
		out = append(out, u.URL, u.Label)
	}
	for _, c := range s.Explore.Commands {
		out = append(out, c.Command, c.Label)
	}
	for _, comp := range s.AllComponents() {
		out = append(out, comp.Namespace)
	}
	for _, chk := range s.Checks {
		out = append(out, chk.URL, chk.Resource, chk.Namespace, chk.Query, chk.Value)
	}
	out = append(out, snippetTemplated(s.Snippets)...)
	for _, r := range s.References {
		out = append(out, r.URL)
	}
	return nonEmpty(out)
}

// incidentTemplated returns every incident string field that authors template.
func incidentTemplated(f *incident.Fault) []string {
	d := f.Detection
	out := []string{d.URL, d.Resource, d.Namespace, d.Query, d.Value, d.BodyContains, f.Description}
	// The target may be templated so a fault can follow the workload binding.
	out = append(out, f.Target.Namespace, f.Target.Workload)
	out = append(out, snippetTemplated(f.Snippets)...)
	for _, r := range f.References {
		out = append(out, r.URL)
	}
	return nonEmpty(out)
}

// snippetTemplated returns the inline YAML body of every snippet.
func snippetTemplated(snips []scenario.Snippet) []string {
	out := make([]string, 0, len(snips))
	for _, s := range snips {
		if s.YAML != "" {
			out = append(out, s.YAML)
		}
	}
	return out
}

func nonEmpty(in []string) []string {
	out := in[:0]
	for _, s := range in {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
