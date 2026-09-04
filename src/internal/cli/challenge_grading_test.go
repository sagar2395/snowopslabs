package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sagar2395/snowopslabs/internal/challenge"
	"github.com/sagar2395/snowopslabs/internal/incident"
)

// A detection check's script is relative to the fault's own directory and its
// URL carries template variables. Grading from the project root with the raw
// check scores every submission zero, which is how three shipped challenges
// became uncompletable.
func TestResolveGradingChecks(t *testing.T) {
	root := t.TempDir()
	faultDir := filepath.Join(root, "incidents", "fault-a")
	if err := os.MkdirAll(filepath.Join(faultDir, "checks"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"fault.yaml": `name: fault-a
displayName: "Fault A"
description: "d"
category: config
severity: medium
target:
  namespace: go-api
  workload: go-api
detection:
  name: reachable
  type: http
  url: "http://go-api.{{.DomainSuffix}}/health"
  expectStatus: 200
`,
		"inject.sh":          "#!/usr/bin/env bash\n",
		"resolve.sh":         "#!/usr/bin/env bash\n",
		"checks/resolved.sh": "#!/usr/bin/env bash\n",
		"hints.md":           "# Hints\n\n## Hint 1\na\n",
		"solution.md":        "# Solution\nb\n",
	}
	for f, content := range files {
		if err := os.WriteFile(filepath.Join(faultDir, f), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	saved := incEng
	t.Cleanup(func() { incEng = saved })
	incEng = incident.NewEngine(root, "lab.example")

	tests := []struct {
		name        string
		ch          *challenge.Challenge
		wantURL     string
		wantDir     string
		wantCheckNm string
	}{
		{
			name: "detection check resolves templates and uses the fault dir",
			ch: &challenge.Challenge{
				Setup:   challenge.SetupSpec{Type: "incident", Ref: "fault-a"},
				Grading: challenge.GradingSpec{UseDetectionCheck: true},
			},
			wantURL:     "http://go-api.lab.example/health",
			wantDir:     faultDir,
			wantCheckNm: "reachable",
		},
		{
			name: "explicit checks resolve templates and keep the project root",
			ch: &challenge.Challenge{
				Setup: challenge.SetupSpec{Type: "incident", Ref: "fault-a"},
				Grading: challenge.GradingSpec{Checks: []challenge.GCheck{{
					Name: "custom", Type: "http",
					URL: "http://go-api.{{.DomainSuffix}}/ready", ExpectStatus: 200,
				}}},
			},
			wantURL:     "http://go-api.lab.example/ready",
			wantDir:     root,
			wantCheckNm: "custom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs, dir, err := resolveGradingChecks(context.Background(), tt.ch, root)
			if err != nil {
				t.Fatalf("resolveGradingChecks() error = %v", err)
			}
			if len(cs) != 1 {
				t.Fatalf("got %d checks, want 1", len(cs))
			}
			if cs[0].Name != tt.wantCheckNm {
				t.Errorf("check name = %q, want %q", cs[0].Name, tt.wantCheckNm)
			}
			if cs[0].URL != tt.wantURL {
				t.Errorf("url = %q, want %q", cs[0].URL, tt.wantURL)
			}
			if dir != tt.wantDir {
				t.Errorf("scriptDir = %q, want %q", dir, tt.wantDir)
			}
		})
	}
}

func TestResolveGradingChecks_UnknownFault(t *testing.T) {
	root := t.TempDir()
	saved := incEng
	t.Cleanup(func() { incEng = saved })
	incEng = incident.NewEngine(root, "lab.example")

	ch := &challenge.Challenge{
		Setup:   challenge.SetupSpec{Type: "incident", Ref: "nope"},
		Grading: challenge.GradingSpec{UseDetectionCheck: true},
	}
	if _, _, err := resolveGradingChecks(context.Background(), ch, root); err == nil {
		t.Fatal("expected an error for an unknown fault")
	}
}
