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

// renderSnippets writes each reference snippet with its resolved body. Path
// snippets are read from dir; every body is template-resolved so it prints as it
// is used. Each snippet carries its own "apply with" hint (defaulting to
// `kubectl apply -f -`) because not every snippet is a kubectl manifest — a Helm
// values file, for instance, is applied differently, and labelling it as
// kubectl-appliable would mislead the learner. A snippet whose file cannot be
// read is reported inline rather than silently dropped (the loader already fails
// validation on a missing path, so this is belt-and-braces).
func renderSnippets(w io.Writer, snips []scenario.Snippet, dir string, resolve resolveFunc) {
	if len(snips) == 0 {
		return
	}
	fmt.Fprintf(w, "\nSnippets:\n")
	for _, s := range snips {
		apply := resolve(s.Apply)
		if apply == "" {
			apply = "kubectl apply -f -"
		}
		// The label and description name the workload too, so they are resolved
		// alongside the body — a snippet titled "{{.WorkloadName}}" reads as an
		// authoring bug to the learner.
		fmt.Fprintf(w, "\n  # %s", resolve(s.Label))
		if s.Description != "" {
			fmt.Fprintf(w, " — %s", resolve(s.Description))
		}
		fmt.Fprintln(w)
		fmt.Fprintf(w, "  # apply with: %s\n", apply)
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
