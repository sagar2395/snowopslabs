// SPDX-License-Identifier: Apache-2.0

package scenario

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	pkgscenario "github.com/snowops-oss/snowopslabs/pkg/scenario"
)

// schemaTypes walks the type graph reachable from Scenario, following struct
// fields and the element types of slices, arrays, pointers and maps. A new
// nested struct added under Scenario is picked up automatically, so the
// field vocabulary can never silently miss it.
func schemaTypes() []reflect.Type {
	var (
		seen  = map[reflect.Type]bool{}
		order []reflect.Type
	)
	var walk func(t reflect.Type)
	walk = func(t reflect.Type) {
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		switch t.Kind() {
		case reflect.Struct:
			if seen[t] {
				return
			}
			seen[t] = true
			order = append(order, t)
			for i := range t.NumField() {
				walk(t.Field(i).Type)
			}
		case reflect.Slice, reflect.Array:
			walk(t.Elem())
		case reflect.Map:
			walk(t.Elem())
		}
	}
	walk(reflect.TypeFor[pkgscenario.Scenario]())
	return order
}

// schemaFields returns the yaml field names the schema defines, plus the
// subset whose values are free-form maps (their sub-keys are data, not
// fields - e.g. the --set entries under a component's `set:`).
func schemaFields() (fields, freeMaps map[string]bool) {
	fields = map[string]bool{}
	freeMaps = map[string]bool{}
	for _, t := range schemaTypes() {
		for i := range t.NumField() {
			f := t.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
			if name == "" || name == "-" {
				continue
			}
			fields[name] = true
			if f.Type.Kind() == reflect.Map {
				freeMaps[name] = true
			}
		}
	}
	return fields, freeMaps
}

// yamlFence matches a ```yaml fenced block and captures its body.
var yamlFence = regexp.MustCompile("(?s)```yaml\n(.*?)```")

// documentedFields collects every mapping key used in the ```yaml examples
// of the scenario schema reference. Each block is parsed as YAML, so literal
// text inside block scalars (such as the example ConfigMap in the snippets
// section) is not mistaken for fields. Keys under a free-form map field are
// data, not fields, and are skipped.
func documentedFields(t *testing.T, freeMaps map[string]bool) map[string]bool {
	t.Helper()
	doc := filepath.Join(repoRoot(t), "docs", "reference", "scenario-schema.md")
	data, err := os.ReadFile(doc) //nolint:gosec // test reads the repo's own doc
	if err != nil {
		t.Fatalf("reading the scenario schema reference: %v", err)
	}
	return documentedFieldsFromMarkdown(t, data, freeMaps)
}

// documentedFieldsFromMarkdown is documentedFields over inline markdown,
// used by the fixture tests below.
func documentedFieldsFromMarkdown(t *testing.T, data []byte, freeMaps map[string]bool) map[string]bool {
	t.Helper()
	blocks := yamlFence.FindAllSubmatch(data, -1)
	if len(blocks) == 0 {
		t.Fatal("no ```yaml blocks found in the markdown")
	}
	fields := map[string]bool{}
	for _, b := range blocks {
		var doc yaml.Node
		if err := yaml.Unmarshal(b[1], &doc); err != nil {
			t.Fatalf("a ```yaml block does not parse: %v", err)
		}
		// Walk the node tree rather than decoding into maps: the reference
		// intentionally repeats a key to show alternatives (platformValues),
		// which map decoding would reject as a duplicate.
		collectKeys(&doc, fields, freeMaps)
	}
	return fields
}

func collectKeys(n *yaml.Node, fields, freeMaps map[string]bool) {
	switch n.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, c := range n.Content {
			collectKeys(c, fields, freeMaps)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			name := ""
			if k.Kind == yaml.ScalarNode {
				name = k.Value
				fields[name] = true
			}
			// The sub-keys of a free-form map are data (e.g. --set
			// entries), not schema fields.
			if !freeMaps[name] {
				collectKeys(v, fields, freeMaps)
			}
		}
	case yaml.AliasNode:
		collectKeys(n.Alias, fields, freeMaps)
	}
	// Scalar nodes (including block-scalar text such as the example ConfigMap
	// in the snippets section) hold no fields.
}

// diffFields compares the schema's field vocabulary against the documented
// fields: which schema fields the docs are missing, and which documented
// fields the schema no longer defines.
func diffFields(fields, documented map[string]bool) (missing, stale []string) {
	for f := range fields {
		if !documented[f] {
			missing = append(missing, f)
		}
	}
	sort.Strings(missing)
	for f := range documented {
		if !fields[f] {
			stale = append(stale, f)
		}
	}
	sort.Strings(stale)
	return missing, stale
}

// TestScenarioSchemaDocMatchesSchema keeps docs/reference/scenario-schema.md
// in step with the fields the validator accepts: a field added to the schema
// must be documented, and a documented field removed from the schema must go
// from the reference too. It runs with the rest of `make test-go`.
func TestScenarioSchemaDocMatchesSchema(t *testing.T) {
	fields, freeMaps := schemaFields()
	if len(fields) == 0 {
		t.Fatal("no schema fields collected; the type-graph walk is broken")
	}
	documented := documentedFields(t, freeMaps)

	missing, stale := diffFields(fields, documented)
	for _, f := range missing {
		t.Errorf("schema field %q is not documented in docs/reference/scenario-schema.md", f)
	}
	for _, f := range stale {
		t.Errorf("docs/reference/scenario-schema.md documents %q, which the schema does not define", f)
	}
}

// TestDiffFields proves the checker can fail: inline markdown fixtures drive
// the same fence parsing and comparison the real test uses.
func TestDiffFields(t *testing.T) {
	tests := []struct {
		name        string
		schema      []string
		freeMaps    []string
		markdown    string
		wantMissing []string
		wantStale   []string
	}{
		{
			name:   "missing field",
			schema: []string{"name", "brandNewField"},
			markdown: "```yaml\n" +
				"name: demo\n" +
				"```\n",
			wantMissing: []string{"brandNewField"},
		},
		{
			name:   "stale field",
			schema: []string{"name"},
			markdown: "```yaml\n" +
				"name: demo\n" +
				"removedField: gone\n" +
				"```\n",
			wantStale: []string{"removedField"},
		},
		{
			name:   "in sync",
			schema: []string{"name", "type"},
			markdown: "```yaml\n" +
				"name: demo\n" +
				"type: helm\n" +
				"```\n",
		},
		{
			name:     "free-form map sub-keys are not fields",
			schema:   []string{"name", "set"},
			freeMaps: []string{"set"},
			markdown: "```yaml\n" +
				"name: demo\n" +
				"set:\n" +
				"  someChartValue: 3\n" +
				"```\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fields := map[string]bool{}
			for _, f := range tt.schema {
				fields[f] = true
			}
			freeMaps := map[string]bool{}
			for _, f := range tt.freeMaps {
				freeMaps[f] = true
			}
			documented := documentedFieldsFromMarkdown(t, []byte(tt.markdown), freeMaps)
			missing, stale := diffFields(fields, documented)
			if !reflect.DeepEqual(missing, tt.wantMissing) {
				t.Errorf("missing = %v, want %v", missing, tt.wantMissing)
			}
			if !reflect.DeepEqual(stale, tt.wantStale) {
				t.Errorf("stale = %v, want %v", stale, tt.wantStale)
			}
		})
	}
}
