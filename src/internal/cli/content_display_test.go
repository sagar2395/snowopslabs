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

func TestRenderReferences(t *testing.T) {
	var buf bytes.Buffer
	renderReferences(&buf, []scenario.Reference{
		{Label: "KEDA — spec", URL: "https://keda.sh/docs/", Note: "the fields"},
		{Label: "no note", URL: "https://example.test"},
	})
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
	renderReferences(&buf, nil)
	if buf.Len() != 0 {
		t.Fatalf("expected no output for empty refs, got %q", buf.String())
	}
}

func TestRenderSnippets_InlineAndPathWithTemplate(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "so.yaml"), []byte("ns: {{.MonitoringNamespace}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	upper := func(s string) string { return strings.ReplaceAll(s, "{{.MonitoringNamespace}}", "monitoring") }

	var buf bytes.Buffer
	renderSnippets(&buf, []scenario.Snippet{
		{Label: "inline", Description: "a note", YAML: "kind: ConfigMap"},
		{Label: "from file", Path: "so.yaml"},
	}, dir, upper)
	got := buf.String()

	if !strings.Contains(got, "Snippets (apply with: kubectl apply -f -):") {
		t.Errorf("missing snippets header: %q", got)
	}
	if !strings.Contains(got, "# inline — a note") {
		t.Errorf("inline label/description missing: %q", got)
	}
	if !strings.Contains(got, "    kind: ConfigMap") {
		t.Errorf("inline body not indented: %q", got)
	}
	// The path snippet's template must be resolved before display.
	if !strings.Contains(got, "    ns: monitoring") {
		t.Errorf("path snippet not template-resolved: %q", got)
	}
	if strings.Contains(got, "{{.MonitoringNamespace}}") {
		t.Errorf("unresolved template leaked into output: %q", got)
	}
}

func TestRenderSnippets_MissingFileReportedInline(t *testing.T) {
	var buf bytes.Buffer
	renderSnippets(&buf, []scenario.Snippet{{Label: "gone", Path: "nope.yaml"}}, t.TempDir(), nil)
	if !strings.Contains(buf.String(), "unavailable:") {
		t.Fatalf("expected an inline unavailable notice, got %q", buf.String())
	}
}
