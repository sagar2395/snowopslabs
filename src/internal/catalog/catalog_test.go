// SPDX-License-Identifier: Apache-2.0
package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- fixtures ---------------------------------------------------------------

const validScenario = `name: s1
displayName: Scenario One
components:
  - name: c1
    type: script
    script: run.sh
`

const validIncident = `name: i1
displayName: Incident One
category: workload
severity: low
target:
  namespace: ns
  workload: w
detection:
  name: resolved
  type: script
  script: resolved.sh
`

const validPath = `name: p1
displayName: Path One
modules:
  - name: m1
    action:
      type: scenario
      ref: s1
    check:
      name: chk
      type: script
      script: x.sh
`

const validChallenge = `name: ch1
displayName: Challenge One
setup:
  type: incident
  ref: i1
grading:
  useDetectionCheck: true
`

// write creates a content item file at <root>/<dir>/<name>/<file>.
func write(t *testing.T, root, dir, name, file, body string) {
	t.Helper()
	itemDir := filepath.Join(root, dir, name)
	if err := os.MkdirAll(itemDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(itemDir, file), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// validRoot builds a temp content root with one valid item of each kind that
// all cross-reference correctly.
func validRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, root, "scenarios", "s1", "scenario.yaml", validScenario)
	write(t, root, "incidents", "i1", "fault.yaml", validIncident)
	write(t, root, "learn", "p1", "path.yaml", validPath)
	write(t, root, "challenges", "ch1", "challenge.yaml", validChallenge)
	return root
}

// findProblem returns the first problem whose message contains sub, or fails.
func findProblem(t *testing.T, ps []Problem, sub string) Problem {
	t.Helper()
	for _, p := range ps {
		if strings.Contains(p.Message, sub) {
			return p
		}
	}
	t.Fatalf("no problem containing %q; got %d problems: %v", sub, len(ps), ps)
	return Problem{}
}

// --- happy path -------------------------------------------------------------

func TestLoad_ValidContent(t *testing.T) {
	c, err := Load(validRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if !c.IsValid() {
		t.Fatalf("expected zero problems, got: %v", c.Problems())
	}
	want := map[Kind]int{KindScenario: 1, KindIncident: 1, KindPath: 1, KindChallenge: 1}
	for k, n := range want {
		if got := c.Counts()[k]; got != n {
			t.Errorf("count[%s] = %d, want %d", k, got, n)
		}
	}
	if _, ok := c.Scenario("s1"); !ok {
		t.Error("scenario s1 not indexed")
	}
	if _, ok := c.Incident("i1"); !ok {
		t.Error("incident i1 not indexed")
	}
	if src := c.Source(KindScenario, "s1"); !strings.HasSuffix(src, filepath.Join("scenarios", "s1", "scenario.yaml")) {
		t.Errorf("Source = %q, want it to point at the scenario file", src)
	}
}

func TestLoad_EmptyRoot(t *testing.T) {
	c, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !c.IsValid() {
		t.Errorf("empty root should be valid, got: %v", c.Problems())
	}
	if len(c.Scenarios()) != 0 {
		t.Error("expected no scenarios")
	}
}

// --- cross-reference --------------------------------------------------

func TestLoad_PathReferencesMissingScenario(t *testing.T) {
	root := t.TempDir()
	// A path that points at a scenario that does not exist.
	badPath := strings.Replace(validPath, "ref: s1", "ref: does-not-exist", 1)
	write(t, root, "learn", "p1", "path.yaml", badPath)

	c, _ := Load(root)
	p := findProblem(t, c.Problems(), "unknown scenario \"does-not-exist\"")
	if p.Kind != KindPath {
		t.Errorf("Kind = %s, want path", p.Kind)
	}
	if !strings.HasSuffix(p.File, filepath.Join("learn", "p1", "path.yaml")) {
		t.Errorf("File = %q, want the path.yaml", p.File)
	}
	if p.Line == 0 {
		t.Error("expected a non-zero line number naming the reference")
	}
}

func TestLoad_ChallengeReferencesMissingIncident(t *testing.T) {
	root := t.TempDir()
	bad := strings.Replace(validChallenge, "ref: i1", "ref: ghost", 1)
	write(t, root, "challenges", "ch1", "challenge.yaml", bad)

	c, _ := Load(root)
	p := findProblem(t, c.Problems(), "unknown incident \"ghost\"")
	if p.Line == 0 {
		t.Error("expected a line number for the bad ref")
	}
}

func TestLoad_UseDetectionCheckRequiresIncident(t *testing.T) {
	root := t.TempDir()
	write(t, root, "scenarios", "s1", "scenario.yaml", validScenario)
	// useDetectionCheck but setup is a scenario, which has no detection check.
	bad := "name: ch1\ndisplayName: C\nsetup:\n  type: scenario\n  ref: s1\ngrading:\n  useDetectionCheck: true\n"
	write(t, root, "challenges", "ch1", "challenge.yaml", bad)

	c, _ := Load(root)
	findProblem(t, c.Problems(), "requires setup.type: incident")
}

// --- naming + decode --------------------------------------------------------

func TestLoad_NameMustMatchDirectory(t *testing.T) {
	root := t.TempDir()
	// Directory is "s1" but the file declares name: other.
	write(t, root, "scenarios", "s1", "scenario.yaml", strings.Replace(validScenario, "name: s1", "name: other", 1))

	c, _ := Load(root)
	p := findProblem(t, c.Problems(), "must match its directory name")
	if p.Line == 0 {
		t.Error("expected the line of the mismatched name")
	}
}

func TestLoad_MalformedYAML(t *testing.T) {
	root := t.TempDir()
	write(t, root, "scenarios", "s1", "scenario.yaml", "name: s1\n  : : bad\n\t- oops")

	c, _ := Load(root)
	findProblem(t, c.Problems(), "YAML")
}

func TestLoad_InvalidScenarioComponent(t *testing.T) {
	root := t.TempDir()
	// helm component with no chart is invalid per scenario.Validate.
	bad := "name: s1\ndisplayName: S\ncomponents:\n  - name: c1\n    type: helm\n"
	write(t, root, "scenarios", "s1", "scenario.yaml", bad)

	c, _ := Load(root)
	findProblem(t, c.Problems(), "helm component requires chart")
}

// --- templates --------------------------------------------------------

func TestLoad_UnknownTemplateKeyIsAProblem(t *testing.T) {
	root := t.TempDir()
	// A check URL referencing a key the typed context does not define.
	bad := `name: s1
displayName: S
components:
  - name: c1
    type: script
    script: run.sh
checks:
  - name: reachable
    type: http
    url: "http://go-api.{{.Nope}}/health"
`
	write(t, root, "scenarios", "s1", "scenario.yaml", bad)

	c, _ := Load(root)
	p := findProblem(t, c.Problems(), "Nope")
	if p.Line == 0 {
		t.Error("expected the line of the bad template")
	}
}

func TestLoad_ValidTemplateResolves(t *testing.T) {
	root := t.TempDir()
	good := `name: s1
displayName: S
components:
  - name: c1
    type: script
    script: run.sh
checks:
  - name: reachable
    type: http
    url: "http://go-api.{{.DomainSuffix}}/health"
`
	write(t, root, "scenarios", "s1", "scenario.yaml", good)

	c, _ := Load(root)
	for _, p := range c.Problems() {
		if strings.Contains(p.Message, "template") {
			t.Errorf("unexpected template problem: %s", p)
		}
	}
}

// --- external roots / override ----------------------------------------

func TestLoad_LaterRootOverrides(t *testing.T) {
	base := validRoot(t)
	ext := t.TempDir()
	// External root redefines s1 with a different display name.
	write(t, ext, "scenarios", "s1", "scenario.yaml", strings.Replace(validScenario, "Scenario One", "Overridden", 1))

	c, _ := Load(base, ext)
	s, ok := c.Scenario("s1")
	if !ok {
		t.Fatal("s1 missing after override")
	}
	if s.DisplayName != "Overridden" {
		t.Errorf("DisplayName = %q, want Overridden (later root should win)", s.DisplayName)
	}
	if src := c.Source(KindScenario, "s1"); !strings.HasPrefix(src, ext) {
		t.Errorf("Source = %q, want it under the external root", src)
	}
}

func TestResolveRoots(t *testing.T) {
	proj := t.TempDir()
	ext := t.TempDir()
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	sep := string(os.PathListSeparator)
	got := resolveRoots(proj, strings.Join([]string{ext, missing, "", ext}, sep))

	if len(got) != 2 {
		t.Fatalf("roots = %v, want [proj ext] (missing + blank + dup skipped)", got)
	}
	if got[0] != proj || got[1] != ext {
		t.Errorf("roots = %v, want proj first then ext", got)
	}
}

func TestResolveRoots_ProjectDedup(t *testing.T) {
	proj := t.TempDir()
	// Listing the project root again in the env must not duplicate it.
	got := resolveRoots(proj, proj)
	if len(got) != 1 {
		t.Errorf("roots = %v, want just the project root", got)
	}
}
