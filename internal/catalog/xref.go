// SPDX-License-Identifier: Apache-2.0
package catalog

import (
	"fmt"

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

// validateTemplates resolves every templated content field against the typed
// template context. A reference to an unknown key (a typo) or a malformed
// template is reported with the file and line, so it fails at validation rather
// than producing a broken URL or namespace at run time (W2-T04).
func (c *Catalog) validateTemplates(projectRoot string) {
	ctx := DefaultTemplateContext(projectRoot)
	for _, s := range c.scenarios {
		key := sourceKey(KindScenario, s.Name)
		c.resolveFields(KindScenario, s.Name, c.sources[key], c.nodes[key], scenarioTemplated(s), ctx)
	}
	for _, f := range c.incidents {
		key := sourceKey(KindIncident, f.Name)
		c.resolveFields(KindIncident, f.Name, c.sources[key], c.nodes[key], incidentTemplated(f), ctx)
	}
}

func (c *Catalog) resolveFields(kind Kind, name, file string, node *yaml.Node, fields []string, ctx TemplateContext) {
	for _, raw := range fields {
		if _, err := Resolve(raw, ctx); err != nil {
			c.problems = append(c.problems, Problem{
				Kind: kind, Name: name, File: file, Line: lineOfValue(node, raw), Message: err.Error(),
			})
		}
	}
}

// scenarioTemplated returns every scenario string field that authors template.
func scenarioTemplated(s *scenario.Scenario) []string {
	var out []string
	for _, u := range s.Explore.URLs {
		out = append(out, u.URL)
	}
	for _, comp := range s.AllComponents() {
		out = append(out, comp.Namespace)
	}
	for _, chk := range s.Checks {
		out = append(out, chk.URL, chk.Resource, chk.Namespace, chk.Query, chk.Value)
	}
	return nonEmpty(out)
}

// incidentTemplated returns every incident string field that authors template.
func incidentTemplated(f *incident.Fault) []string {
	d := f.Detection
	return nonEmpty([]string{d.URL, d.Resource, d.Namespace, d.Query, d.Value, d.BodyContains})
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
