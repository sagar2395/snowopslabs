// SPDX-License-Identifier: Apache-2.0

// Package scaffold generates valid, lints-clean, verify-ready starter content so
// a first contribution is a few minutes' work. It is the single source of truth
// for `labctl scenario new`; the committed sdk/scenario-template/ dir mirrors
// this output for humans who prefer to copy by hand.
package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
)

// nameRe matches a scenario name.
var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

type vars struct {
	Name     string // scenario name
	BareName string // same as Name; kept distinct for template clarity
	Title    string // human display name
}

const scenarioYAML = `# yaml-language-server: $schema=https://labs.snowops.net/schemas/scenario.schema.json
apiVersion: scenario.snowops.net/v2
name: {{.BareName}}
displayName: {{.Title}}
description: TODO — describe what this scenario demonstrates.
category: custom
objectives:
  - TODO — what the learner should take away
stages:
  - name: setup
    description: Deploy the scenario's components here.
    components: []
checks:
  - name: ready
    type: script
    script: checks/ready.sh
`

const readyScript = `#!/usr/bin/env bash
# Scaffolded readiness check — replace with a real assertion (exit 0 = pass).
set -euo pipefail
echo "scenario {{.BareName}} is ready"
exit 0
`

// Scenario scaffolds scenarios/<name>/ under projectRoot with a valid v2
// scenario.yaml and a passing readiness check. It refuses to overwrite an
// existing scenario unless force is set. Returns the created directory.
func Scenario(projectRoot, name string, force bool) (string, error) {
	if !nameRe.MatchString(name) {
		return "", fmt.Errorf("invalid scenario name %q (lowercase letters, digits, hyphens)", name)
	}
	dir := filepath.Join(projectRoot, "scenarios", name)
	if err := ensureFresh(dir, force); err != nil {
		return "", err
	}
	v := vars{Name: name, BareName: name, Title: titleize(name)}
	if err := render(filepath.Join(dir, "scenario.yaml"), scenarioYAML, v, 0o644); err != nil {
		return "", err
	}
	if err := render(filepath.Join(dir, "checks", "ready.sh"), readyScript, v, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func ensureFresh(dir string, force bool) error {
	if _, err := os.Stat(dir); err == nil {
		if !force {
			return fmt.Errorf("%s already exists (use --force to overwrite)", dir)
		}
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
	}
	return nil
}

func render(path, tmpl string, v vars, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	t, err := template.New(filepath.Base(path)).Parse(tmpl)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return t.Execute(f, v)
}

// titleize turns "kafka-broker-outage" into "Kafka Broker Outage".
func titleize(name string) string {
	parts := strings.Split(name, "-")
	for i, p := range parts {
		if p != "" {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}
