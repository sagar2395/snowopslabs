// SPDX-License-Identifier: Apache-2.0

package snippet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sagar2395/snowopslabs/pkg/scenario"
)

func TestResolve(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("deploy.yaml", "# banner\n# more banner\n---\nname: {{.WorkloadName}}  # trailing stays\n")
	write("tool.sh", "#!/usr/bin/env bash\n# why one\n# why two\necho {{.WorkloadName}}\n")
	resolve := func(s string) string { return strings.ReplaceAll(s, "{{.WorkloadName}}", "echo-server") }

	tests := []struct {
		name        string
		in          scenario.Snippet
		wantYAML    string
		wantLabel   string
		wantCommand string
		wantErr     bool
	}{
		{
			name:      "path manifest is trimmed then resolved",
			in:        scenario.Snippet{Label: "{{.WorkloadName}} deploy", Path: "deploy.yaml"},
			wantYAML:  "---\nname: echo-server  # trailing stays\n",
			wantLabel: "echo-server deploy",
		},
		{
			name:     "script keeps its shebang",
			in:       scenario.Snippet{Label: "tool", Path: "tool.sh"},
			wantYAML: "#!/usr/bin/env bash\necho echo-server\n",
		},
		{
			name:        "exercise manifest gets an inlined kubectl apply",
			in:          scenario.Snippet{Label: "x", YAML: "kind: ConfigMap", Exercise: true},
			wantYAML:    "kind: ConfigMap",
			wantCommand: "kubectl apply -f - <<'EOF'\nkind: ConfigMap\nEOF",
		},
		{
			name:        "exercise with an authored command uses it, resolved",
			in:          scenario.Snippet{Label: "x", Path: "tool.sh", Apply: "bash tool.sh --app {{.WorkloadName}}", Exercise: true},
			wantYAML:    "#!/usr/bin/env bash\necho echo-server\n",
			wantCommand: "bash tool.sh --app echo-server",
		},
		{
			name:     "exercise script without a command has none",
			in:       scenario.Snippet{Label: "x", Path: "tool.sh", Exercise: true},
			wantYAML: "#!/usr/bin/env bash\necho echo-server\n",
		},
		{
			name:     "reference snippet has no apply command",
			in:       scenario.Snippet{Label: "x", YAML: "kind: ConfigMap"},
			wantYAML: "kind: ConfigMap",
		},
		{
			name:        "heredoc delimiter avoids a line in the body",
			in:          scenario.Snippet{Label: "x", YAML: "a: 1\nEOF\n", Exercise: true},
			wantYAML:    "a: 1\nEOF\n",
			wantCommand: "kubectl apply -f - <<'SNIPPET_EOF'\na: 1\nEOF\nSNIPPET_EOF",
		},
		{
			name:      "missing file is an error but keeps the label",
			in:        scenario.Snippet{Label: "{{.WorkloadName}}", Path: "nope.yaml"},
			wantLabel: "echo-server",
			wantErr:   true,
		},
		{
			name:    "path escaping the directory is refused",
			in:      scenario.Snippet{Label: "x", Path: "../../etc/passwd"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(dir, tt.in, resolve)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got.YAML != tt.wantYAML {
				t.Errorf("YAML = %q, want %q", got.YAML, tt.wantYAML)
			}
			if tt.wantLabel != "" && got.Label != tt.wantLabel {
				t.Errorf("Label = %q, want %q", got.Label, tt.wantLabel)
			}
			if got.ApplyCommand != tt.wantCommand {
				t.Errorf("ApplyCommand = %q, want %q", got.ApplyCommand, tt.wantCommand)
			}
		})
	}
}

func TestTrimComments(t *testing.T) {
	tests := []struct {
		name  string
		shell bool
		in    string
		want  string
	}{
		{
			name: "banner and multi-line blocks go, single lines stay",
			in:   "# banner\n\napiVersion: v1\n# one line stays\nkind: A\n\n# block one\n# block two\n\nspec: {}\n",
			want: "apiVersion: v1\n# one line stays\nkind: A\n\nspec: {}\n",
		},
		{
			name: "a single-line banner is still a banner",
			in:   "# the file\nkey: value\n",
			want: "key: value\n",
		},
		{
			name: "yaml block scalar keeps its hash lines",
			in:   "data:\n  script: |\n    # a comment inside the script\n    # and another\n    echo hi\nnext: 1\n# dropped\n# block\n",
			want: "data:\n  script: |\n    # a comment inside the script\n    # and another\n    echo hi\nnext: 1\n",
		},
		{
			name: "list item block scalar",
			in:   "args:\n  - |\n    # kept\n    # kept too\n  - two\n",
			want: "args:\n  - |\n    # kept\n    # kept too\n  - two\n",
		},
		{
			name:  "shell heredoc keeps its hash lines",
			shell: true,
			in:    "#!/bin/sh\ncat <<'EOF' > f\n# content\n# more content\nEOF\n# why\n# because\necho done\n",
			want:  "#!/bin/sh\ncat <<'EOF' > f\n# content\n# more content\nEOF\necho done\n",
		},
		{
			name:  "a here-string is not a heredoc",
			shell: true,
			in:    "set -e\nread x <<< \"$y\"\n# a\n# b\necho $x\n",
			want:  "set -e\nread x <<< \"$y\"\necho $x\n",
		},
		{
			name: "blank lines left behind collapse",
			in:   "a: 1\n\n# x\n# y\n\n\nb: 2",
			want: "a: 1\n\nb: 2",
		},
		{
			name: "only comments leaves nothing",
			in:   "# a\n# b\n",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := trimComments(tt.in, tt.shell); got != tt.want {
				t.Errorf("trimComments() =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}
