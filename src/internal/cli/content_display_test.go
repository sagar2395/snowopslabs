// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sagar2395/snowopslabs/pkg/scenario"
)

// identityResolve stands in for an engine's resolver where the test's content
// carries no template variables.
func identityResolve(in string) string { return in }

// A reference's label, URL and note must all have templates expanded.
func TestRenderReferencesResolvesEveryField(t *testing.T) {
	var buf bytes.Buffer
	upper := func(in string) string { return strings.ReplaceAll(in, "{{.WorkloadName}}", "go-api") }
	renderReferences(&buf, []scenario.Reference{
		{Label: "{{.WorkloadName}} spec", URL: "https://x.test/{{.WorkloadName}}", Note: "about {{.WorkloadName}}"},
	}, upper)
	got := buf.String()
	if strings.Contains(got, "{{") {
		t.Errorf("unresolved template in output: %q", got)
	}
	for _, want := range []string{"go-api spec", "https://x.test/go-api", "about go-api"} {
		if !strings.Contains(got, want) {
			t.Errorf("output %q missing %q", got, want)
		}
	}
}

func TestRenderReferences(t *testing.T) {
	var buf bytes.Buffer
	renderReferences(&buf, []scenario.Reference{
		{Label: "KEDA — spec", URL: "https://keda.sh/docs/", Note: "the fields"},
		{Label: "no note", URL: "https://example.test"},
	}, identityResolve)
	got := buf.String()
	want := "\nReferences:\n" +
		"  - KEDA — spec\n    https://keda.sh/docs/\n    the fields\n" +
		"  - no note\n    https://example.test\n"
	if got != want {
		t.Fatalf("renderReferences mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestRenderReferences_EmptyWritesNothing(t *testing.T) {
	var buf bytes.Buffer
	renderReferences(&buf, nil, identityResolve)
	if buf.Len() != 0 {
		t.Fatalf("expected no output for empty refs, got %q", buf.String())
	}
}

func TestRenderSnippets_InlineAndPathWithTemplate(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "so.yaml"), []byte("# banner\n# more\nns: {{.MonitoringNamespace}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	resolve := func(s string) string { return strings.ReplaceAll(s, "{{.MonitoringNamespace}}", "monitoring") }

	var buf bytes.Buffer
	renderSnippets(&buf, []scenario.Snippet{
		{Label: "inline", Description: "a note", YAML: "kind: ConfigMap"},
		{Label: "from file", Path: "so.yaml"},
		{Label: "yours", YAML: "kind: Secret", Exercise: true},
		{Label: "yours too", YAML: "resources: {}", Apply: "helm upgrade -f -", Exercise: true},
	}, dir, resolve)
	got := buf.String()

	tests := []struct {
		name string
		want string
		gone bool
	}{
		{name: "header", want: "Snippets:"},
		{name: "label and description", want: "# inline — a note"},
		{name: "inline body indented", want: "    kind: ConfigMap"},
		{name: "path body resolved", want: "    ns: monitoring"},
		{name: "banner trimmed", want: "# banner", gone: true},
		{name: "exercise defaults to kubectl apply", want: "# you apply this: kubectl apply -f -"},
		{name: "exercise uses its own command", want: "# you apply this: helm upgrade -f -"},
		{name: "no unresolved template", want: "{{.MonitoringNamespace}}", gone: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if strings.Contains(got, tt.want) == tt.gone {
				t.Errorf("output contains %q = %v, want %v:\n%s", tt.want, !tt.gone, !tt.gone, got)
			}
		})
	}
	// A reference snippet is installed by the scenario, so it carries no apply hint.
	if strings.Count(got, "# you apply this:") != 2 {
		t.Errorf("apply hints should appear only on the two exercises:\n%s", got)
	}
}

func TestRenderSnippets_MissingFileReportedInline(t *testing.T) {
	var buf bytes.Buffer
	renderSnippets(&buf, []scenario.Snippet{{Label: "gone", Path: "nope.yaml"}}, t.TempDir(), identityResolve)
	if !strings.Contains(buf.String(), "unavailable:") {
		t.Fatalf("expected an inline unavailable notice, got %q", buf.String())
	}
}

// `scenario info` must print component namespaces with templates expanded.
func TestRenderComponent_ResolvesTemplates(t *testing.T) {
	resolve := func(s string) string {
		return strings.ReplaceAll(s, "{{.MonitoringNamespace}}", "monitoring")
	}

	tests := []struct {
		name string
		comp scenario.Component
		want string
	}{
		{
			"templated namespace resolves",
			scenario.Component{Name: "slo-dashboards", Type: "grafana-dashboard", Namespace: "{{.MonitoringNamespace}}"},
			"  - slo-dashboards [grafana-dashboard] ns=monitoring\n",
		},
		{
			"templated namespace resolves alongside a chart",
			scenario.Component{Name: "loki", Type: "helm", Chart: "grafana-community/loki", Namespace: "{{.MonitoringNamespace}}"},
			"  - loki [helm] chart=grafana-community/loki ns=monitoring\n",
		},
		{
			"a literal namespace is unchanged",
			scenario.Component{Name: "git-server", Type: "manifest", Namespace: "gitops"},
			"  - git-server [manifest] ns=gitops\n",
		},
		{
			"a component with no chart or namespace prints neither",
			scenario.Component{Name: "gitops-demo-app", Type: "script"},
			"  - gitops-demo-app [script]\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			renderComponent(&buf, tt.comp, "  ", resolve)
			if got := buf.String(); got != tt.want {
				t.Fatalf("renderComponent mismatch:\n got: %q\nwant: %q", got, tt.want)
			}
		})
	}
}
