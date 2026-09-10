// SPDX-License-Identifier: Apache-2.0
package scenario

import (
	"github.com/sagar2395/snowopslabs/internal/tmpl"
	"github.com/sagar2395/snowopslabs/internal/workload"
	"github.com/sagar2395/snowopslabs/pkg/checks"
)

// ResolvedForDisplay returns a copy of the scenario with every reader-facing
// field expanded against an explicit binding and parameter set.
//
// It exists so exactly one place decides which fields a reader sees resolved.
// That list was previously spread through the API handler and grew a field at a
// time, which is how stage descriptions, explore labels and check remediations
// each shipped a literal "{{.WorkloadName}}" to the UI. Anything an author can
// write and a reader can read belongs here.
//
// It mutates nothing: the engine is shared across requests, and a display call
// must not rebind it.
func (e *Engine) ResolvedForDisplay(s *Scenario, params map[string]string, bound workload.Workload) *Scenario {
	ctx := e.templateContextFor(bound)
	overlay := make(map[string]string, len(e.resolvedParams)+len(params))
	for k, v := range e.resolvedParams {
		overlay[k] = v
	}
	for k, v := range params {
		overlay[k] = v
	}
	resolve := func(in string) string { return tmpl.Expand(in, ctx, overlay) }
	each := func(in []string) []string {
		if in == nil {
			return nil
		}
		out := make([]string, len(in))
		for i, v := range in {
			out[i] = resolve(v)
		}
		return out
	}
	components := func(in []Component) []Component {
		if in == nil {
			return nil
		}
		out := make([]Component, len(in))
		for i, c := range in {
			c.Name = resolve(c.Name)
			c.Namespace = resolve(c.Namespace)
			if c.Set != nil {
				set := make(map[string]string, len(c.Set))
				for k, v := range c.Set {
					set[k] = resolve(v)
				}
				c.Set = set
			}
			out[i] = c
		}
		return out
	}

	c := *s
	c.DisplayName = resolve(s.DisplayName)
	c.Description = resolve(s.Description)
	c.Objectives = each(s.Objectives)
	c.Prerequisites.Apps = each(s.Prerequisites.Apps)
	c.Components = components(s.Components)

	if s.Stages != nil {
		stages := make([]Stage, len(s.Stages))
		for i, st := range s.Stages {
			st.Description = resolve(st.Description)
			st.Components = components(st.Components)
			stages[i] = st
		}
		c.Stages = stages
	}

	if s.Checks != nil {
		cs := make([]checks.Check, len(s.Checks))
		for i, ck := range s.Checks {
			cs[i] = resolveCheckWith(ck, resolve)
		}
		c.Checks = cs
	}

	urls := make([]ExploreURL, len(s.Explore.URLs))
	for i, u := range s.Explore.URLs {
		u.Label, u.URL = resolve(u.Label), resolve(u.URL)
		urls[i] = u
	}
	cmds := make([]ExploreCommand, len(s.Explore.Commands))
	for i, cm := range s.Explore.Commands {
		cm.Label, cm.Command = resolve(cm.Label), resolve(cm.Command)
		cmds[i] = cm
	}
	c.Explore = Explore{URLs: urls, Commands: cmds, Tips: each(s.Explore.Tips)}

	if s.References != nil {
		refs := make([]Reference, len(s.References))
		for i, r := range s.References {
			r.Label, r.URL, r.Note = resolve(r.Label), resolve(r.URL), resolve(r.Note)
			refs[i] = r
		}
		c.References = refs
	}

	if s.Snippets != nil {
		snips := make([]Snippet, len(s.Snippets))
		for i, sn := range s.Snippets {
			// The UI has no access to the scenario directory, so a snippet stored
			// as a file has to travel with its body or it renders empty.
			if body, err := e.SnippetContent(s, sn); err == nil {
				sn.YAML = body
			}
			sn.Label = resolve(sn.Label)
			sn.Description = resolve(sn.Description)
			sn.Apply = resolve(sn.Apply)
			snips[i] = sn
		}
		c.Snippets = snips
	}
	return &c
}
