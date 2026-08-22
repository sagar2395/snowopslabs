// SPDX-License-Identifier: Apache-2.0
package lab

// End-to-end plan execution against a fake project: stub platform
// providers, a stub engine/deploy.sh, a no-component scenario, and a stub
// traffic stop script. Everything runs through the real engines and the
// real executor — only the cluster is absent.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sagar2395/snowopslabs/internal/executor"
	"github.com/sagar2395/snowopslabs/internal/platform"
	"github.com/sagar2395/snowopslabs/internal/scenario"
)

// fakeLabProject builds a project root with everything Execute touches.
func fakeLabProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	// Platform providers (script exit 0): ingress/traefik, monitoring/metrics
	for _, p := range []struct{ cat, name string }{
		{"ingress", "traefik"}, {"monitoring", "metrics"},
	} {
		dir := filepath.Join(root, "platform", p.cat, p.name)
		os.MkdirAll(dir, 0755)
		for _, f := range []string{"install.sh", "uninstall.sh", "status.sh"} {
			os.WriteFile(filepath.Join(dir, f), []byte("#!/usr/bin/env bash\nexit 0\n"), 0755)
		}
	}

	// engine/deploy.sh records its calls
	os.MkdirAll(filepath.Join(root, "engine"), 0755)
	os.WriteFile(filepath.Join(root, "engine", "deploy.sh"),
		[]byte("#!/usr/bin/env bash\necho \"$1 $2\" >> deploy.log\n"), 0755)

	// services/traffic/stop.sh records its call
	os.MkdirAll(filepath.Join(root, "services", "traffic"), 0755)
	os.WriteFile(filepath.Join(root, "services", "traffic", "stop.sh"),
		[]byte("#!/usr/bin/env bash\necho stopped >> traffic.log\n"), 0755)

	// A component-less scenario (activation needs no cluster)
	scDir := filepath.Join(root, "scenarios", "empty-scenario")
	os.MkdirAll(scDir, 0755)
	os.WriteFile(filepath.Join(scDir, "scenario.yaml"),
		[]byte("name: empty-scenario\ndisplayName: Empty\ncategory: testing\ncomponents: []\n"), 0644)

	return root
}

func deps(t *testing.T, root string, continueOnError bool) Deps {
	t.Helper()
	return Deps{
		Exec:            executor.New(root),
		Registry:        platform.NewRegistry(root),
		Scenes:          scenario.NewEngine(root, "k3d.local", "k3d"),
		ContinueOnError: continueOnError,
	}
}

func TestExecute_RestoreThenResetRoundTrip(t *testing.T) {
	root := fakeLabProject(t)
	d := deps(t, root, false)

	snap := &Snapshot{
		Name:      "round-trip",
		Platform:  []string{"monitoring/metrics", "ingress/traefik"},
		Apps:      []string{"go-api"},
		Scenarios: []string{"empty-scenario"},
	}

	if err := Execute(RestorePlan(snap), d); err != nil {
		t.Fatalf("restore: %v", err)
	}

	if got := d.Registry.Installed(); len(got) != 2 {
		t.Fatalf("platform not recorded as installed: %v", got)
	}
	active := false
	for _, s := range d.Scenes.Status() {
		if s.Name == "empty-scenario" && s.Active {
			active = true
		}
	}
	if !active {
		t.Fatal("scenario should be active after restore")
	}
	if log, _ := os.ReadFile(filepath.Join(root, "deploy.log")); !strings.Contains(string(log), "deploy go-api") {
		t.Fatalf("app deploy not executed: %q", log)
	}

	// Restore again over the converged lab: must be a no-op success.
	if err := Execute(RestorePlan(snap), d); err != nil {
		t.Fatalf("restore must be idempotent: %v", err)
	}

	// Reset: tears down everything except ingress.
	reset := ResetPlan(d.Registry.Installed(), snap.Apps, []string{"empty-scenario"})
	d.ContinueOnError = true
	if err := Execute(reset, d); err != nil {
		t.Fatalf("reset: %v", err)
	}

	if got := d.Registry.Installed(); len(got) != 1 || got[0] != "ingress/traefik" {
		t.Fatalf("reset must keep only ingress, got %v", got)
	}
	for _, s := range d.Scenes.Status() {
		if s.Active {
			t.Fatalf("scenario %s still active after reset", s.Name)
		}
	}
	if log, _ := os.ReadFile(filepath.Join(root, "deploy.log")); !strings.Contains(string(log), "destroy go-api") {
		t.Fatalf("app destroy not executed: %q", log)
	}
	if log, _ := os.ReadFile(filepath.Join(root, "traffic.log")); !strings.Contains(string(log), "stopped") {
		t.Fatalf("traffic stop not executed: %q", log)
	}
}

func TestExecute_StopsOnFirstErrorByDefault(t *testing.T) {
	root := fakeLabProject(t)
	d := deps(t, root, false)

	plan := []Action{
		{Kind: "platform-install", Target: "ingress/missing-provider"}, // fails
		{Kind: "app-deploy", Target: "go-api"},                         // must not run
	}
	if err := Execute(plan, d); err == nil {
		t.Fatal("expected error")
	}
	if _, err := os.Stat(filepath.Join(root, "deploy.log")); !os.IsNotExist(err) {
		t.Fatal("execution must stop before the app deploy")
	}
}

func TestExecute_ContinueOnErrorCollectsFailures(t *testing.T) {
	root := fakeLabProject(t)
	d := deps(t, root, true)

	plan := []Action{
		{Kind: "platform-uninstall", Target: "ingress/missing-provider"}, // fails
		{Kind: "app-destroy", Target: "go-api"},                          // still runs
	}
	err := Execute(plan, d)
	if err == nil || !strings.Contains(err.Error(), "1 of 2 actions failed") {
		t.Fatalf("expected aggregated failure, got: %v", err)
	}
	if log, _ := os.ReadFile(filepath.Join(root, "deploy.log")); !strings.Contains(string(log), "destroy go-api") {
		t.Fatal("later actions must still run with ContinueOnError")
	}
}

func TestExecute_ProgressCallback(t *testing.T) {
	root := fakeLabProject(t)
	d := deps(t, root, false)

	var seen []string
	d.Progress = func(i, total int, a Action) {
		seen = append(seen, a.Kind)
	}
	plan := []Action{{Kind: "traffic-stop"}, {Kind: "app-deploy", Target: "go-api"}}
	if err := Execute(plan, d); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || seen[0] != "traffic-stop" {
		t.Fatalf("progress callbacks wrong: %v", seen)
	}
}

func TestSplitComponent(t *testing.T) {
	tests := []struct {
		in, cat, name string
		wantErr       bool
	}{
		{"ingress/traefik", "ingress", "traefik", false},
		{"monitoring/metrics/prometheus", "monitoring/metrics", "prometheus", false},
		{"no-slash", "", "", true},
		{"/leading", "", "", true},
		{"trailing/", "", "", true},
	}
	for _, tt := range tests {
		cat, name, err := splitComponent(tt.in)
		if tt.wantErr != (err != nil) {
			t.Errorf("splitComponent(%q): err = %v", tt.in, err)
			continue
		}
		if cat != tt.cat || name != tt.name {
			t.Errorf("splitComponent(%q) = (%q, %q), want (%q, %q)", tt.in, cat, name, tt.cat, tt.name)
		}
	}
}
