// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/sagar2395/snowopslabs/internal/snippet"
	"github.com/sagar2395/snowopslabs/pkg/scenario"
)

// resolveFunc expands the template variables an author may use in a content
// string (e.g. {{.DomainSuffix}}). The scenario and incident engines each expose
// one; passing it in keeps this display code independent of either engine.
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

// renderComponent writes one component line for `scenario info`. Chart and
// namespace are template-resolved because info is the learner's first contact:
// a raw "{{.MonitoringNamespace}}" sent them looking for a namespace that does
// not exist, while install had long since resolved it to the real one.
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

// renderSnippets writes each snippet as a reader sees it. An exercise snippet is
// marked, with the command that applies it, because nothing else installs it.
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
