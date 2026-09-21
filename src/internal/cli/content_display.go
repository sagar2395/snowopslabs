// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/sagar2395/snowopslabs/internal/snippet"
	"github.com/sagar2395/snowopslabs/pkg/scenario"
)

// resolveFunc expands template variables such as {{.DomainSuffix}} in a
// content string. The scenario and incident engines each provide one.
type resolveFunc func(string) string

// renderReferences writes the upstream doc/tool links for a scenario or
// incident. It writes nothing when there are none, so callers need no guard.
func renderReferences(w io.Writer, refs []scenario.Reference, resolve resolveFunc) {
	if len(refs) == 0 {
		return
	}
	fmt.Fprintf(w, "\nReferences:\n")
	for _, r := range refs {
		fmt.Fprintf(w, "  - %s\n    %s\n", resolve(r.Label), resolve(r.URL))
		if r.Note != "" {
			fmt.Fprintf(w, "    %s\n", resolve(r.Note))
		}
	}
}

// renderComponent writes one component line for `scenario info`, with the
// chart and namespace expanded.
func renderComponent(w io.Writer, c scenario.Component, indent string, resolve resolveFunc) {
	fmt.Fprintf(w, "%s- %s [%s]", indent, c.Name, c.Type)
	if c.Chart != "" {
		fmt.Fprintf(w, " chart=%s", resolve(c.Chart))
	}
	if c.Namespace != "" {
		fmt.Fprintf(w, " ns=%s", resolve(c.Namespace))
	}
	fmt.Fprintln(w)
}

// renderSnippets writes each snippet. An exercise snippet, which only the
// learner applies, is marked and shown with its apply command.
func renderSnippets(w io.Writer, snips []scenario.Snippet, dir string, resolve resolveFunc) {
	if len(snips) == 0 {
		return
	}
	fmt.Fprintf(w, "\nSnippets:\n")
	for _, raw := range snips {
		sn, err := snippet.Resolve(dir, raw, resolve)
		fmt.Fprintf(w, "\n  # %s", sn.Label)
		if sn.Description != "" {
			fmt.Fprintf(w, " — %s", sn.Description)
		}
		fmt.Fprintln(w)
		if sn.Exercise {
			apply := sn.Apply
			if apply == "" {
				apply = "kubectl apply -f -   (pipe the manifest below into it)"
			}
			fmt.Fprintf(w, "  # you apply this: %s\n", apply)
		}
		if err != nil {
			fmt.Fprintf(w, "    (unavailable: %v)\n", err)
			continue
		}
		for line := range strings.SplitSeq(strings.TrimRight(sn.YAML, "\n"), "\n") {
			fmt.Fprintf(w, "    %s\n", line)
		}
	}
}
