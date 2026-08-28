// SPDX-License-Identifier: Apache-2.0
package catalog

import (
	"os"
	"path/filepath"
	"testing"
)

// The fuzz targets feed arbitrary bytes through each content parser and assert
// only that loading never panics — malformed, truncated or adversarial YAML must
// always surface as a Problem, never a crash (W2 exit criterion). Running
// `go test` executes the seed corpus below as ordinary tests; `go test -fuzz`
// explores further.

// fuzzKind writes data as the sole item of one kind and loads it, asserting no
// panic. It returns nothing: a panic fails the test, and problems are expected.
func fuzzKind(t *testing.T, kind Kind, data []byte) {
	t.Helper()
	layout := kindLayout[kind]
	root := t.TempDir()
	dir := filepath.Join(root, layout.dir, "item")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Skip() // filesystem hiccup, not a parser defect
	}
	if err := os.WriteFile(filepath.Join(dir, layout.file), data, 0o644); err != nil {
		t.Skip()
	}
	// Must not panic. The result (valid or problem-laden) is irrelevant here.
	if _, err := Load(root); err != nil {
		t.Fatalf("Load returned a hard error (should collect problems instead): %v", err)
	}
}

var fuzzSeeds = []string{
	"",
	"name: item",
	"name: item\ncomponents: not-a-list",
	"\t- oops\n  : :",
	"name: {{.DomainSuffix}}",
	"name: item\ndetection:\n  type: http\n  url: \"{{.Nope}}\"",
	"modules:\n  - action: {type: scenario, ref: x}",
	"name: item\nsetup:\n  type: incident\n  ref: {{",
	string([]byte{0x00, 0x01, 0xff, 0xfe}),
	"a: &a [*a]", // self-referential alias
	// M2 references/snippets: malformed shapes must surface as problems, never panic.
	"name: item\nreferences: not-a-list",
	"name: item\nreferences:\n  - {}",
	"name: item\nsnippets:\n  - {label: x, yaml: \"a\", path: \"b\"}",
	"name: item\nsnippets:\n  - {label: x, path: \"../escape.yaml\"}",
	"name: item\nsnippets:\n  - {label: x, yaml: \"{{.Nope}}\"}",
}

func FuzzLoadScenario(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) { fuzzKind(t, KindScenario, data) })
}

func FuzzLoadIncident(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) { fuzzKind(t, KindIncident, data) })
}

func FuzzLoadPath(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) { fuzzKind(t, KindPath, data) })
}

func FuzzLoadChallenge(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) { fuzzKind(t, KindChallenge, data) })
}
