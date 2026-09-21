// SPDX-License-Identifier: Apache-2.0
package catalog

import (
	"strings"
	"testing"
)

func TestLoad_ScriptCallsCarryTheWorkloadBinding(t *testing.T) {
	const bound = "#!/bin/sh\n. \"$(dirname \"$0\")/../../_lib/workload.sh\"\n"
	tests := []struct {
		name    string
		command string
		script  string
		wantErr bool
	}{
		{
			name:    "bound script called with --app",
			command: "bash scenarios/s1/scripts/tool.sh v1 --app {{.WorkloadName}} --namespace {{.WorkloadNamespace}}",
			script:  bound,
		},
		{
			name:    "bound script called through ProjectRoot with --app",
			command: "bash {{.ProjectRoot}}/scenarios/s1/scripts/tool.sh --app {{.WorkloadName}} && labctl scenario verify s1",
			script:  bound,
		},
		{
			name:    "bound script called without --app",
			command: "bash scenarios/s1/scripts/tool.sh v1.1.0",
			script:  bound,
			wantErr: true,
		},
		{
			name:    "--app on a later command does not count",
			command: "bash scenarios/s1/scripts/tool.sh && labctl traffic start --app {{.WorkloadName}}",
			script:  bound,
			wantErr: true,
		},
		{
			name:    "a script that is not bound needs no flags",
			command: "bash scenarios/s1/scripts/tool.sh {{.WorkloadNamespace}}",
			script:  "#!/bin/sh\necho \"$1\"\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			scn := validScenario + "explore:\n  commands:\n    - label: run it\n      command: \"" + tt.command + "\"\n"
			write(t, root, "scenarios", "s1", "scenario.yaml", scn)
			writeAsset(t, root, "scenarios", "s1", "scripts/tool.sh", tt.script)

			c, err := Load(root)
			if err != nil {
				t.Fatal(err)
			}
			var found bool
			for _, p := range c.Problems() {
				if strings.Contains(p.Message, "bound to a workload") {
					found = true
					if p.Line == 0 {
						t.Errorf("problem has no line: %v", p)
					}
				}
			}
			if found != tt.wantErr {
				t.Errorf("binding problem reported = %v, want %v (problems: %v)", found, tt.wantErr, c.Problems())
			}
		})
	}
}

func TestLoad_KubectlOnTemplatedFilesIsReported(t *testing.T) {
	tests := []struct {
		name    string
		command string
		body    string
		wantErr bool
	}{
		{name: "templated file applied directly", command: "kubectl apply -f scenarios/s1/manifests/pdb.yaml", body: "name: {{.WorkloadName}}\n", wantErr: true},
		{name: "through ProjectRoot", command: "kubectl delete -f {{.ProjectRoot}}/scenarios/s1/manifests/pdb.yaml", body: "name: {{.WorkloadName}}\n", wantErr: true},
		{name: "plain file is fine", command: "kubectl apply -f scenarios/s1/manifests/pdb.yaml", body: "name: fixed\n"},
		{name: "rendered first is fine", command: "labctl scenario render s1 manifests/pdb.yaml --app {{.WorkloadName}} | kubectl apply -f -", body: "name: {{.WorkloadName}}\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			scn := validScenario + "explore:\n  commands:\n    - label: apply\n      command: \"" + tt.command + "\"\n"
			write(t, root, "scenarios", "s1", "scenario.yaml", scn)
			writeAsset(t, root, "scenarios", "s1", "manifests/pdb.yaml", tt.body)

			c, err := Load(root)
			if err != nil {
				t.Fatal(err)
			}
			var found bool
			for _, p := range c.Problems() {
				found = found || strings.Contains(p.Message, "labctl scenario render s1 manifests/pdb.yaml")
			}
			if found != tt.wantErr {
				t.Errorf("render problem reported = %v, want %v (problems: %v)", found, tt.wantErr, c.Problems())
			}
		})
	}
}
