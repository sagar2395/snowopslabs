// SPDX-License-Identifier: Apache-2.0
package platform

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// InterfaceMeta is the subset of a provider's _interface.yaml the details page
// surfaces: what the tool is, what it offers, and what it needs.
type InterfaceMeta struct {
	Description  string   `json:"description"`
	Provides     []string `json:"provides"`
	Ports        []string `json:"ports"`
	Dependencies []string `json:"dependencies"`
	Resources    []string `json:"resources"`
	Chart        string   `json:"chart"`
}

// rawInterface mirrors the _interface.yaml fields we read. Providers declare it
// either in their own directory or one level up at the category root, so this is
// tried against both.
type rawInterface struct {
	Description string   `yaml:"description"`
	Provides    []string `yaml:"provides"`
	Requires    struct {
		KubernetesResources []string `yaml:"kubernetes_resources"`
		Ports               []string `yaml:"ports"`
		Dependencies        []string `yaml:"dependencies"`
	} `yaml:"requires"`
	Implementations map[string]struct {
		Chart string `yaml:"chart"`
	} `yaml:"implementations"`
}

// Meta reads and parses the provider's _interface.yaml. It looks in the
// provider's own directory first, then the category directory above it (some
// categories describe all their providers in one shared file). A missing or
// unparseable file yields a zero InterfaceMeta, never an error — the details
// page degrades gracefully.
func (p *Provider) Meta() InterfaceMeta {
	for _, path := range []string{
		filepath.Join(p.Path, "_interface.yaml"),
		filepath.Join(filepath.Dir(p.Path), "_interface.yaml"),
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var raw rawInterface
		if yaml.Unmarshal(data, &raw) != nil {
			continue
		}
		meta := InterfaceMeta{
			Description:  strings.TrimSpace(raw.Description),
			Provides:     raw.Provides,
			Ports:        raw.Requires.Ports,
			Dependencies: raw.Requires.Dependencies,
			Resources:    raw.Requires.KubernetesResources,
		}
		if impl, ok := raw.Implementations[p.Name]; ok {
			meta.Chart = impl.Chart
		}
		return meta
	}
	return InterfaceMeta{}
}

// InstallCommands extracts the helm/kubectl commands from the provider's
// install.sh so the details page shows how the tool actually reaches the
// cluster. Backslash line-continuations are joined into one command, and shell
// plumbing (repo adds, waits, variables) is left out — only the install verbs a
// learner would recognise are returned.
func (p *Provider) InstallCommands() []string {
	f, err := os.Open(filepath.Join(p.Path, "install.sh"))
	if err != nil {
		return nil
	}
	defer f.Close()

	var cmds []string
	var cont strings.Builder
	joining := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if joining {
			cont.WriteString(" " + trimmed)
		} else if isInstallVerb(trimmed) {
			cont.Reset()
			cont.WriteString(trimmed)
		} else {
			continue
		}
		if strings.HasSuffix(trimmed, "\\") {
			// Drop the trailing backslash and keep accumulating.
			s := strings.TrimSpace(strings.TrimSuffix(cont.String(), "\\"))
			cont.Reset()
			cont.WriteString(s)
			joining = true
			continue
		}
		joining = false
		cmds = append(cmds, normalizeSpaces(cont.String()))
	}
	return cmds
}

// isInstallVerb reports whether a line begins a helm/kubectl install command
// (the ones worth showing), as opposed to repo setup, waits, or status reads.
func isInstallVerb(line string) bool {
	switch {
	case strings.HasPrefix(line, "helm upgrade"), strings.HasPrefix(line, "helm install"):
		return true
	case strings.HasPrefix(line, "kubectl apply"), strings.HasPrefix(line, "kubectl create"):
		return true
	}
	return false
}

// normalizeSpaces collapses runs of whitespace (left by joining continuations)
// into single spaces.
func normalizeSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
