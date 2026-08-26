// SPDX-License-Identifier: Apache-2.0
package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTypedAccessors(t *testing.T) {
	c, err := Load(validRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := c.Path("p1"); !ok || p.Name != "p1" {
		t.Errorf("Path(p1) = %v, %v", p, ok)
	}
	if ch, ok := c.Challenge("ch1"); !ok || ch.Name != "ch1" {
		t.Errorf("Challenge(ch1) = %v, %v", ch, ok)
	}
	if _, ok := c.Scenario("missing"); ok {
		t.Error("Scenario(missing) should be false")
	}
	if len(c.Incidents()) != 1 || c.Incidents()[0].Name != "i1" {
		t.Errorf("Incidents() = %v", c.Incidents())
	}
	if len(c.Paths()) != 1 || len(c.Challenges()) != 1 {
		t.Errorf("Paths/Challenges wrong: %d, %d", len(c.Paths()), len(c.Challenges()))
	}
	if got := c.Source(KindIncident, "nope"); got != "" {
		t.Errorf("Source of unknown item = %q, want empty", got)
	}
}

func TestScenariosSortedByName(t *testing.T) {
	root := t.TempDir()
	write(t, root, "scenarios", "zeta", "scenario.yaml", strings.Replace(validScenario, "name: s1", "name: zeta", 1))
	write(t, root, "scenarios", "alpha", "scenario.yaml", strings.Replace(validScenario, "name: s1", "name: alpha", 1))
	c, _ := Load(root)
	got := c.Scenarios()
	if len(got) != 2 || got[0].Name != "alpha" || got[1].Name != "zeta" {
		t.Errorf("Scenarios() not sorted: %v", got)
	}
}

func TestResolveRoots_FromEnv(t *testing.T) {
	proj := t.TempDir()
	ext := t.TempDir()
	t.Setenv(ContentPathEnv, ext)
	got := ResolveRoots(proj)
	if len(got) != 2 || got[0] != proj || got[1] != ext {
		t.Errorf("ResolveRoots = %v, want [%s %s]", got, proj, ext)
	}
}

func TestLoad_DecodeError(t *testing.T) {
	root := t.TempDir()
	// components must be a list; a scalar makes Decode (not Unmarshal) fail.
	write(t, root, "scenarios", "s1", "scenario.yaml", "name: s1\ncomponents: not-a-list\n")
	c, _ := Load(root)
	findProblem(t, c.Problems(), "decoding")
}

func TestLoad_UnreadableItemFileSkipped(t *testing.T) {
	// A kind directory whose item file is absent is skipped quietly.
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "scenarios", "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	c, _ := Load(root)
	if len(c.Scenarios()) != 0 {
		t.Errorf("expected no scenarios from an empty dir, got %d", len(c.Scenarios()))
	}
	if !c.IsValid() {
		t.Errorf("a directory without the item file should not be a problem: %v", c.Problems())
	}
}
