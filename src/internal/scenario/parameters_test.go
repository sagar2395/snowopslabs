// SPDX-License-Identifier: Apache-2.0
package scenario

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func ip(n int) *int { return &n }

// paramScenario returns a scenario with the autoscaling knobs (min/max/threshold).
func paramScenario() *Scenario {
	return &Scenario{
		Name: "autoscaling-under-load",
		Parameters: []Parameter{
			{Name: "MinReplicas", Default: "1", Type: "int", Min: ip(1), Max: ip(10), NotGreaterThan: "MaxReplicas"},
			{Name: "MaxReplicas", Default: "6", Type: "int", Min: ip(1), Max: ip(10)},
			{Name: "Threshold", Default: "25", Type: "int", Min: ip(1), Max: ip(500)},
		},
	}
}

func TestEffectiveParams_DefaultsWhenNoOverrides(t *testing.T) {
	eng := newTestEngine(t)
	got, err := eng.effectiveParams(paramScenario())
	if err != nil {
		t.Fatalf("effectiveParams: %v", err)
	}
	for k, want := range map[string]string{"MinReplicas": "1", "MaxReplicas": "6", "Threshold": "25"} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
}

func TestEffectiveParams_AppliesValidOverrides(t *testing.T) {
	eng := newTestEngine(t)
	eng.SetActivationParams(map[string]string{"Threshold": "10", "MaxReplicas": "4"})
	got, err := eng.effectiveParams(paramScenario())
	if err != nil {
		t.Fatalf("effectiveParams: %v", err)
	}
	if got["Threshold"] != "10" || got["MaxReplicas"] != "4" || got["MinReplicas"] != "1" {
		t.Errorf("unexpected effective params: %v", got)
	}
}

func TestEffectiveParams_RejectsBadOverrides(t *testing.T) {
	cases := []struct {
		name     string
		override map[string]string
		want     string
	}{
		{"above max", map[string]string{"Threshold": "9999"}, "above the maximum"},
		{"below min", map[string]string{"MinReplicas": "0"}, "below the minimum"},
		{"not int", map[string]string{"Threshold": "abc"}, "not an integer"},
		{"min > max", map[string]string{"MinReplicas": "5", "MaxReplicas": "2"}, "must not be greater than"},
		{"unknown key", map[string]string{"Bogus": "1"}, "unknown parameter"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eng := newTestEngine(t)
			eng.SetActivationParams(tc.override)
			_, err := eng.effectiveParams(paramScenario())
			if err == nil {
				t.Fatalf("expected an error for %v", tc.override)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.want)
			}
		})
	}
}

// The resolved parameter values must substitute into a manifest's {{.Name}}
// placeholders, while built-in vars still win and are never shadowed.
func TestResolveTemplate_SubstitutesParams(t *testing.T) {
	eng := newTestEngine(t) // monitoring namespace "observability"
	eng.resolvedParams = map[string]string{"MinReplicas": "1", "MaxReplicas": "4", "Threshold": "10"}
	out := eng.resolveTemplate("min={{.MinReplicas}} max={{.MaxReplicas}} thr={{.Threshold}} ns={{.MonitoringNamespace}}")
	if out != "min=1 max=4 thr=10 ns=observability" {
		t.Errorf("unexpected render: %q", out)
	}

	// A parameter must never shadow a built-in.
	eng.resolvedParams = map[string]string{"MonitoringNamespace": "hijacked"}
	if out := eng.resolveTemplate("ns={{.MonitoringNamespace}}"); out != "ns=observability" {
		t.Errorf("param shadowed a built-in: %q", out)
	}
}

// SnippetContent reads a snippet's Path file and resolves both parameter
// defaults and built-in template vars, so the UI shows the real manifest.
func TestSnippetContent_ResolvesPathWithDefaults(t *testing.T) {
	eng := newTestEngine(t) // monitoring namespace "observability"
	dir := t.TempDir()
	manifest := "threshold: {{.Threshold}}\nns: {{.MonitoringNamespace}}\n"
	if err := os.WriteFile(filepath.Join(dir, "scaledobject.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Scenario{Name: "x", Dir: dir, Parameters: []Parameter{{Name: "Threshold", Default: "25", Type: "int"}}}

	got, err := eng.SnippetContent(s, Snippet{Path: "scaledobject.yaml"})
	if err != nil {
		t.Fatalf("SnippetContent: %v", err)
	}
	if !strings.Contains(got, "threshold: 25") {
		t.Errorf("parameter default not resolved:\n%s", got)
	}
	if !strings.Contains(got, "ns: observability") {
		t.Errorf("built-in not resolved:\n%s", got)
	}
}

func TestSnippetContent_InlineYAML(t *testing.T) {
	eng := newTestEngine(t)
	s := &Scenario{Name: "x", Parameters: []Parameter{{Name: "Threshold", Default: "25", Type: "int"}}}
	got, err := eng.SnippetContent(s, Snippet{YAML: "t: {{.Threshold}}"})
	if err != nil {
		t.Fatalf("SnippetContent: %v", err)
	}
	if got != "t: 25" {
		t.Errorf("inline yaml = %q, want %q", got, "t: 25")
	}
}

// A file's leading comment banner is stripped from the snippet (it belongs in
// the snippet description), but inline field comments are kept.
func TestSnippetContent_StripsLeadingCommentBanner(t *testing.T) {
	eng := newTestEngine(t)
	dir := t.TempDir()
	manifest := "# banner line one\n# banner line two\n\napiVersion: v1\nkind: ConfigMap  # inline stays\n"
	if err := os.WriteFile(filepath.Join(dir, "cm.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Scenario{Name: "x", Dir: dir}
	got, err := eng.SnippetContent(s, Snippet{Path: "cm.yaml"})
	if err != nil {
		t.Fatalf("SnippetContent: %v", err)
	}
	if strings.Contains(got, "banner line") {
		t.Errorf("leading comment banner not stripped:\n%s", got)
	}
	if !strings.HasPrefix(got, "apiVersion: v1") {
		t.Errorf("snippet should start at the first real line, got:\n%s", got)
	}
	if !strings.Contains(got, "# inline stays") {
		t.Errorf("inline comment was wrongly stripped:\n%s", got)
	}
}

func TestSnippetContent_RejectsPathEscape(t *testing.T) {
	eng := newTestEngine(t)
	s := &Scenario{Name: "x", Dir: t.TempDir()}
	if _, err := eng.SnippetContent(s, Snippet{Path: "../../../etc/passwd"}); err == nil {
		t.Fatal("expected an error for a path escaping the scenario dir")
	}
}
