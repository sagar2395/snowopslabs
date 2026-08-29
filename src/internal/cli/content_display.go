// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/sagar2395/snowopslabs/pkg/scenario"
)

// resolveFunc expands the template variables an author may use in a content
// string (e.g. {{.DomainSuffix}}). The scenario and incident engines each expose
// one; passing it in keeps this display code independent of either engine.
type resolveFunc func(string) string

// renderReferences writes the upstream doc/tool links for a scenario or
// incident. It writes nothing when there are none, so callers need no guard.
func renderReferences(w io.Writer, refs []scenario.Reference) {
	if len(refs) == 0 {
		return
	}
	fmt.Fprintf(w, "\nReferences:\n")
	for _, r := range refs {
		fmt.Fprintf(w, "  - %s\n    %s\n", r.Label, r.URL)
		if r.Note != "" {
			fmt.Fprintf(w, "    %s\n", r.Note)
		}
	}
}

// renderSnippets writes each applyable manifest snippet with its resolved body,
// ready to copy into `kubectl apply -f -`. Path snippets are read from dir;
// every body is template-resolved so it is applyable as printed. A snippet whose
// file cannot be read is reported inline rather than silently dropped (the
// loader already fails validation on a missing path, so this is belt-and-braces).
func renderSnippets(w io.Writer, snips []scenario.Snippet, dir string, resolve resolveFunc) {
	if len(snips) == 0 {
		return
	}
	fmt.Fprintf(w, "\nSnippets (apply with: kubectl apply -f -):\n")
	for _, s := range snips {
		fmt.Fprintf(w, "\n  # %s", s.Label)
		if s.Description != "" {
			fmt.Fprintf(w, " — %s", s.Description)
		}
		fmt.Fprintln(w)
		body, err := snippetBody(s, dir, resolve)
		if err != nil {
			fmt.Fprintf(w, "    (unavailable: %v)\n", err)
			continue
		}
		for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
			fmt.Fprintf(w, "    %s\n", line)
		}
	}
}

// snippetBody returns a snippet's template-resolved manifest text, reading it
// from disk when the snippet references a path.
func snippetBody(s scenario.Snippet, dir string, resolve resolveFunc) (string, error) {
	raw := s.YAML
	if s.Path != "" {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(s.Path)))
		if err != nil {
			return "", err
		}
		raw = string(data)
	}
	if resolve != nil {
		raw = resolve(raw)
	}
	return raw, nil
}
