// SPDX-License-Identifier: Apache-2.0

package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// workloadLib is the helper a scenario script sources to learn which app it acts
// on. Run from a terminal, such a script has only the flags it is given.
const workloadLib = "_lib/workload.sh"

// scriptCall matches a shell call of a scenario script and captures the script's
// repo path and the arguments written after it.
var scriptCall = regexp.MustCompile(
	`\b(?:bash|sh) +(?:\{\{\.ProjectRoot\}\}/)?(scenarios/[A-Za-z0-9_-]+/[A-Za-z0-9_./-]+\.sh)([^"'` + "`" + `&|;\n]*)`)

// templatedApply matches kubectl reading a scenario file directly.
var templatedApply = regexp.MustCompile(
	`\bkubectl +(apply|create|replace|delete) +-f +(?:\{\{\.ProjectRoot\}\}/)?(scenarios/[A-Za-z0-9_-]+/[A-Za-z0-9_./-]+\.ya?ml)`)

// checkScriptBindings reports learner-facing commands that cannot run as
// written: a workload-bound script called without --app, which stops with a
// usage error, and kubectl reading a templated file, which sends literal
// {{.WorkloadName}} to the cluster.
func (c *Catalog) checkScriptBindings() {
	bound := map[string]bool{}
	sourcesLib := func(root, rel string) bool {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if v, ok := bound[full]; ok {
			return v
		}
		data, err := os.ReadFile(full) //nolint:gosec // a path inside the content root
		bound[full] = err == nil && strings.Contains(string(data), workloadLib)
		return bound[full]
	}

	for _, s := range c.scenarios {
		key := sourceKey(KindScenario, s.Name)
		root := filepath.Dir(filepath.Dir(s.Dir))
		commands := make([]string, 0, len(s.Explore.Commands)+len(s.Checks)+len(s.Snippets)+len(s.Explore.Tips))
		for _, cmd := range s.Explore.Commands {
			commands = append(commands, cmd.Command)
		}
		for _, chk := range s.Checks {
			commands = append(commands, chk.Remediation)
		}
		for _, sn := range s.Snippets {
			commands = append(commands, sn.Apply)
		}
		commands = append(commands, s.Explore.Tips...)

		for _, text := range commands {
			for _, m := range templatedApply.FindAllStringSubmatch(text, -1) {
				verb, rel := m[1], m[2]
				data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel))) //nolint:gosec // a path inside the content root
				if err != nil || !strings.Contains(string(data), "{{.") {
					continue
				}
				scn, file, _ := strings.Cut(strings.TrimPrefix(rel, "scenarios/"), "/")
				c.problems = append(c.problems, Problem{
					Kind: KindScenario, Name: s.Name, File: c.sources[key], Line: lineOfValue(c.nodes[key], text),
					Message: fmt.Sprintf("kubectl cannot fill in the template variables in %s: use labctl scenario render %s %s --app {{.WorkloadName}} | kubectl %s -f -", rel, scn, file, verb),
				})
			}
			for _, m := range scriptCall.FindAllStringSubmatch(text, -1) {
				if strings.Contains(m[2], "--app") || !sourcesLib(root, m[1]) {
					continue
				}
				c.problems = append(c.problems, Problem{
					Kind: KindScenario, Name: s.Name, File: c.sources[key], Line: lineOfValue(c.nodes[key], text),
					Message: fmt.Sprintf("%s is bound to a workload: call it with --app {{.WorkloadName}} --namespace {{.WorkloadNamespace}}", m[1]),
				})
			}
		}
	}
}
