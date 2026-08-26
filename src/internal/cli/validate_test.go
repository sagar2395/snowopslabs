// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sagar2395/snowopslabs/internal/catalog"
)

const validScenarioYAML = `name: s1
displayName: Scenario One
components:
  - name: c1
    type: script
    script: run.sh
`

const validPathYAML = `name: p1
displayName: Path One
modules:
  - name: m1
    action:
      type: scenario
      ref: %s
    check:
      name: chk
      type: script
      script: x.sh
`

func writeContent(t *testing.T, root, dir, name, file, body string) {
	t.Helper()
	d := filepath.Join(root, dir, name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, file), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWriteValidationReport_Valid(t *testing.T) {
	root := t.TempDir()
	writeContent(t, root, "scenarios", "s1", "scenario.yaml", validScenarioYAML)
	writeContent(t, root, "learn", "p1", "path.yaml", strings.Replace(validPathYAML, "%s", "s1", 1))

	c, _ := catalog.Load(root)
	var out bytes.Buffer
	if err := writeValidationReport(&out, c, false); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "✓ all content valid") {
		t.Errorf("expected success banner, got:\n%s", got)
	}
	for _, want := range []string{"1 scenarios", "1 paths"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary missing %q:\n%s", want, got)
		}
	}
}

func TestWriteValidationReport_Invalid(t *testing.T) {
	root := t.TempDir()
	// Path references a scenario that does not exist.
	writeContent(t, root, "learn", "p1", "path.yaml", strings.Replace(validPathYAML, "%s", "ghost", 1))

	c, _ := catalog.Load(root)
	var out bytes.Buffer
	if err := writeValidationReport(&out, c, false); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "unknown scenario \"ghost\"") {
		t.Errorf("expected the broken reference in the report:\n%s", got)
	}
	if !strings.Contains(got, "✗ 1 problem found") {
		t.Errorf("expected a singular problem count:\n%s", got)
	}
	// A problem line must carry the file location.
	if !strings.Contains(got, filepath.Join("learn", "p1", "path.yaml")) {
		t.Errorf("expected the file path in the report:\n%s", got)
	}
}

func TestWriteValidationReport_JSON(t *testing.T) {
	root := t.TempDir()
	writeContent(t, root, "learn", "p1", "path.yaml", strings.Replace(validPathYAML, "%s", "ghost", 1))

	c, _ := catalog.Load(root)
	var out bytes.Buffer
	if err := writeValidationReport(&out, c, true); err != nil {
		t.Fatal(err)
	}
	var report struct {
		Valid    bool `json:"valid"`
		Problems []struct {
			Message string `json:"message"`
			File    string `json:"file"`
		} `json:"problems"`
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out.String())
	}
	if report.Valid {
		t.Error("Valid should be false")
	}
	if len(report.Problems) != 1 {
		t.Fatalf("expected 1 problem, got %d", len(report.Problems))
	}
}

func TestSetVersion(t *testing.T) {
	// Restore whatever the default was so other tests are unaffected.
	orig := rootCmd.Version
	t.Cleanup(func() { rootCmd.Version = orig })

	SetVersion("v1.2.3")
	if rootCmd.Version != "v1.2.3" {
		t.Errorf("Version = %q, want v1.2.3", rootCmd.Version)
	}
	// An empty version falls back to "dev" rather than blanking --version.
	SetVersion("")
	if rootCmd.Version != "dev" {
		t.Errorf("Version = %q, want dev fallback", rootCmd.Version)
	}
}
