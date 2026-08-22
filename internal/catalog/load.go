// SPDX-License-Identifier: Apache-2.0
package catalog

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/sagar2395/snowopslabs/internal/challenge"
	"github.com/sagar2395/snowopslabs/internal/incident"
	"github.com/sagar2395/snowopslabs/internal/learn"
	"github.com/sagar2395/snowopslabs/pkg/scenario"
)

// kindDir maps each kind to the directory and item filename it lives in under a
// content root.
var kindLayout = map[Kind]struct{ dir, file string }{
	KindScenario:  {"scenarios", "scenario.yaml"},
	KindIncident:  {"incidents", "fault.yaml"},
	KindPath:      {"learn", "path.yaml"},
	KindChallenge: {"challenges", "challenge.yaml"},
}

// Load builds a validated snapshot from the given content roots, in precedence
// order (later roots override earlier ones on a name collision). Every content
// defect is collected into the returned Catalog's problems rather than aborting,
// so a single pass reports them all; the returned error is non-nil only on a
// failure to read a root that was expected to exist.
func Load(roots ...string) (*Catalog, error) {
	c := newCatalog(roots)
	for _, root := range roots {
		for _, kind := range AllKinds {
			c.loadKind(root, kind)
		}
	}
	c.crossReference()
	c.validateTemplates(defaultProjectRoot(roots))
	return c, nil
}

func defaultProjectRoot(roots []string) string {
	if len(roots) > 0 {
		return roots[0]
	}
	return "."
}

// loadKind scans one kind's directory under one root and loads every item.
func (c *Catalog) loadKind(root string, kind Kind) {
	layout := kindLayout[kind]
	dir := filepath.Join(root, layout.dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		// A missing kind directory is not an error: not every root ships every
		// kind (an external root may hold only scenarios, for instance).
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		file := filepath.Join(dir, entry.Name(), layout.file)
		if _, err := os.Stat(file); err != nil {
			continue // directory without the kind's item file — skip quietly
		}
		c.loadItem(kind, entry.Name(), file)
	}
}

// loadItem decodes, validates and indexes a single content item.
func (c *Catalog) loadItem(kind Kind, dirName, file string) {
	data, err := os.ReadFile(file)
	if err != nil {
		c.problems = append(c.problems, Problem{Kind: kind, Name: dirName, File: file, Message: err.Error()})
		return
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		c.problems = append(c.problems, Problem{Kind: kind, Name: dirName, File: file, Message: "parsing YAML: " + err.Error()})
		return
	}

	switch kind {
	case KindScenario:
		c.loadScenario(dirName, file, &doc)
	case KindIncident:
		c.loadIncident(dirName, file, &doc)
	case KindPath:
		c.loadPath(dirName, file, &doc)
	case KindChallenge:
		c.loadChallenge(dirName, file, &doc)
	}
}

func (c *Catalog) loadScenario(dirName, file string, doc *yaml.Node) {
	var s scenario.Scenario
	if err := doc.Decode(&s); err != nil {
		c.problems = append(c.problems, Problem{Kind: KindScenario, Name: dirName, File: file, Message: "decoding: " + err.Error()})
		return
	}
	s.Dir = filepath.Dir(file)
	c.checkName(KindScenario, dirName, s.Name, file, doc)
	if err := s.Validate(); err != nil {
		c.problems = append(c.problems, Problem{Kind: KindScenario, Name: nameOr(s.Name, dirName), File: file, Message: err.Error()})
	}
	c.index(KindScenario, dirName, file, doc, func() {
		s.Source = ""
		c.scenarios[dirName] = &s
	})
}

func (c *Catalog) loadIncident(dirName, file string, doc *yaml.Node) {
	var f incident.Fault
	if err := doc.Decode(&f); err != nil {
		c.problems = append(c.problems, Problem{Kind: KindIncident, Name: dirName, File: file, Message: "decoding: " + err.Error()})
		return
	}
	f.Dir = filepath.Dir(file)
	c.checkName(KindIncident, dirName, f.Name, file, doc)
	if err := f.Validate(); err != nil {
		c.problems = append(c.problems, Problem{Kind: KindIncident, Name: nameOr(f.Name, dirName), File: file, Message: err.Error()})
	}
	c.index(KindIncident, dirName, file, doc, func() { c.incidents[dirName] = &f })
}

func (c *Catalog) loadPath(dirName, file string, doc *yaml.Node) {
	var p learn.Path
	if err := doc.Decode(&p); err != nil {
		c.problems = append(c.problems, Problem{Kind: KindPath, Name: dirName, File: file, Message: "decoding: " + err.Error()})
		return
	}
	c.checkName(KindPath, dirName, p.Name, file, doc)
	for _, msg := range p.Validate() {
		c.problems = append(c.problems, Problem{Kind: KindPath, Name: nameOr(p.Name, dirName), File: file, Message: msg})
	}
	c.index(KindPath, dirName, file, doc, func() { c.paths[dirName] = &p })
}

func (c *Catalog) loadChallenge(dirName, file string, doc *yaml.Node) {
	var ch challenge.Challenge
	if err := doc.Decode(&ch); err != nil {
		c.problems = append(c.problems, Problem{Kind: KindChallenge, Name: dirName, File: file, Message: "decoding: " + err.Error()})
		return
	}
	c.checkName(KindChallenge, dirName, ch.Name, file, doc)
	for _, msg := range ch.Validate() {
		c.problems = append(c.problems, Problem{Kind: KindChallenge, Name: nameOr(ch.Name, dirName), File: file, Message: msg})
	}
	c.index(KindChallenge, dirName, file, doc, func() { c.challenges[dirName] = &ch })
}

// checkName enforces that an item's declared name matches its directory — the
// convention every engine relies on to resolve a reference to a directory.
func (c *Catalog) checkName(kind Kind, dirName, declared, file string, doc *yaml.Node) {
	if declared != "" && declared != dirName {
		c.problems = append(c.problems, Problem{
			Kind: kind, Name: dirName, File: file, Line: lineOfValue(doc, declared),
			Message: fmt.Sprintf("name %q must match its directory name %q", declared, dirName),
		})
	}
}

// index records the item's source file and YAML document, then stores it. An
// across-root name collision overrides silently (that is the external-content
// override contract); the source and node are updated to the winning file.
func (c *Catalog) index(kind Kind, name, file string, doc *yaml.Node, store func()) {
	key := sourceKey(kind, name)
	c.sources[key] = file
	c.nodes[key] = doc
	store()
}

func nameOr(name, fallback string) string {
	if name != "" {
		return name
	}
	return fallback
}

// lineOfValue returns the 1-based line of the first scalar node in doc whose
// value equals want, or 0 if none is found. It lets a cross-reference or naming
// problem point the author at the exact line.
func lineOfValue(doc *yaml.Node, want string) int {
	var walk func(n *yaml.Node) int
	walk = func(n *yaml.Node) int {
		if n == nil {
			return 0
		}
		if n.Kind == yaml.ScalarNode && n.Value == want {
			return n.Line
		}
		for _, ch := range n.Content {
			if line := walk(ch); line != 0 {
				return line
			}
		}
		return 0
	}
	return walk(doc)
}
