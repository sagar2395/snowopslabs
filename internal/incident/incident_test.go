// SPDX-License-Identifier: Apache-2.0
package incident

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sagar2395/snowopslabs/internal/executor"
	"github.com/sagar2395/snowopslabs/pkg/checks"
)

const testFaultYAML = `name: %s
displayName: "Test Fault"
description: "breaks things for tests"
category: workload
severity: medium
target:
  namespace: go-api
  workload: go-api
prerequisites:
  apps:
    - go-api
detection:
  name: resolved-when-marker-gone
  type: script
  script: checks/resolved.sh
  timeoutSeconds: 5
`

// writeFault creates a complete fault dir whose inject.sh drops a marker
// file and whose detection check passes once the marker is gone — a fully
// working incident without a cluster.
func writeFault(t *testing.T, root, name string) {
	t.Helper()
	dir := filepath.Join(root, "incidents", name)
	if err := os.MkdirAll(filepath.Join(dir, "checks"), 0755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"fault.yaml":         strings.Replace(testFaultYAML, "%s", name, 1),
		"inject.sh":          "#!/usr/bin/env bash\ntouch \"$(dirname \"$0\")/BROKEN\"\necho injected \"$TARGET_NAMESPACE/$TARGET_WORKLOAD\"\n",
		"resolve.sh":         "#!/usr/bin/env bash\nrm -f \"$(dirname \"$0\")/BROKEN\"\necho resolved\n",
		"checks/resolved.sh": "#!/usr/bin/env bash\n[ ! -f \"$(dirname \"$0\")/../BROKEN\" ]\n",
		"hints.md":           "# Hints\n\n## Hint 1\nLook closer.\n\n## Hint 2\nCloser still.\n\n## Hint 3\nIt's the marker file.\n",
		"solution.md":        "# Solution\nRemove the marker.\n",
	}
	for f, content := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte(content), 0755); err != nil {
			t.Fatal(err)
		}
	}
}

func createApp(t *testing.T, root, name string) {
	t.Helper()
	dir := filepath.Join(root, "apps", name)
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "app.env"), []byte("APP_NAME="+name+"\n"), 0644)
}

func testEngine(t *testing.T, faults ...string) (*Engine, string) {
	t.Helper()
	root := t.TempDir()
	createApp(t, root, "go-api")
	for _, f := range faults {
		writeFault(t, root, f)
	}
	return NewEngine(root, "k3d.local"), root
}

func testRunner() *checks.Runner {
	r := checks.NewRunner()
	r.DefaultTimeout = 5 * time.Second
	return r
}

func TestEngine_DiscoveryAndContract(t *testing.T) {
	e, root := testEngine(t, "fault-a", "fault-b")
	if len(e.List()) != 2 {
		t.Fatalf("expected 2 faults, got %d", len(e.List()))
	}

	// A dir violating the contract (missing resolve.sh) must not load.
	dir := filepath.Join(root, "incidents", "broken")
	writeFault(t, root, "broken")
	os.Remove(filepath.Join(dir, "resolve.sh"))
	e = NewEngine(root, "k3d.local")
	if len(e.List()) != 2 {
		t.Fatalf("contract-violating fault must not load, got %d faults", len(e.List()))
	}
	if err := e.LoadErrors()["broken"]; err == nil || !strings.Contains(err.Error(), "resolve.sh") {
		t.Fatalf("load error should name the missing file: %v", err)
	}
}

func TestEngine_NameMustMatchDir(t *testing.T) {
	root := t.TempDir()
	writeFault(t, root, "dir-name")
	yaml := strings.Replace(testFaultYAML, "%s", "other-name", 1)
	os.WriteFile(filepath.Join(root, "incidents", "dir-name", "fault.yaml"), []byte(yaml), 0644)

	e := NewEngine(root, "k3d.local")
	if len(e.List()) != 0 {
		t.Fatal("name/dir mismatch must not load")
	}
	if err := e.LoadErrors()["dir-name"]; err == nil || !strings.Contains(err.Error(), "must match") {
		t.Fatalf("got %v", err)
	}
}

func TestFaultValidate(t *testing.T) {
	base := func() *Fault {
		return &Fault{
			Name: "x", Category: "network", Severity: "high",
			Target:    Target{Namespace: "go-api", Workload: "go-api"},
			Detection: checks.Check{Name: "d", Type: "http", URL: "http://x"},
		}
	}
	if err := base().Validate(); err != nil {
		t.Fatalf("base fault should be valid: %v", err)
	}

	tests := []struct {
		name    string
		mutate  func(*Fault)
		wantErr string
	}{
		{"bad category", func(f *Fault) { f.Category = "cosmic" }, "unknown category"},
		{"bad severity", func(f *Fault) { f.Severity = "apocalyptic" }, "unknown severity"},
		{"missing target", func(f *Fault) { f.Target = Target{} }, "target.namespace"},
		{"invalid detection", func(f *Fault) { f.Detection.URL = "" }, "detection:"},
		{"detection script traversal", func(f *Fault) {
			f.Detection = checks.Check{Name: "d", Type: "script", Script: "../../etc/evil.sh"}
		}, "relative path inside"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := base()
			tt.mutate(f)
			err := f.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestInject_StatusResolutionLoop(t *testing.T) {
	e, root := testEngine(t, "fault-a")
	ex := executor.New(root)
	ctx := context.Background()

	// Nothing active initially.
	if _, err := e.Status(ctx, testRunner(), ""); err != ErrNoActive {
		t.Fatalf("expected ErrNoActive, got %v", err)
	}

	f, err := e.Inject("fault-a", ex, false, false)
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	if f.Name != "fault-a" {
		t.Fatalf("injected %q", f.Name)
	}

	// Status while broken: not resolved, state stays.
	res, err := e.Status(ctx, testRunner(), "")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if res.Resolved || res.Check.Pass {
		t.Fatalf("should not be resolved yet: %+v", res.Check)
	}
	if active, _ := e.Active(); active == nil {
		t.Fatal("active state must persist while unresolved")
	}

	// "Manually fix" by removing the marker, then Status detects resolution.
	os.Remove(filepath.Join(root, "incidents", "fault-a", "BROKEN"))
	res, err = e.Status(ctx, testRunner(), "")
	if err != nil {
		t.Fatalf("Status after fix: %v", err)
	}
	if !res.Resolved {
		t.Fatalf("expected resolved, got %+v", res.Check)
	}
	if active, _ := e.Active(); active != nil {
		t.Fatal("active state must clear on resolution")
	}
}

func TestInject_SecondIncidentNeedsForce(t *testing.T) {
	e, root := testEngine(t, "fault-a", "fault-b")
	ex := executor.New(root)

	if _, err := e.Inject("fault-a", ex, false, false); err != nil {
		t.Fatal(err)
	}
	_, err := e.Inject("fault-b", ex, false, false)
	if err == nil || !strings.Contains(err.Error(), "fault-a") {
		t.Fatalf("expected active-incident error naming fault-a, got %v", err)
	}
	if _, err := e.Inject("fault-b", ex, true, false); err != nil {
		t.Fatalf("force inject: %v", err)
	}
	if active, _ := e.Active(); active == nil || active.Fault != "fault-b" {
		t.Fatalf("active = %+v", active)
	}
}

func TestInject_PrereqMissing(t *testing.T) {
	root := t.TempDir() // no apps/go-api
	writeFault(t, root, "fault-a")
	e := NewEngine(root, "k3d.local")

	_, err := e.Inject("fault-a", executor.New(root), false, false)
	if err == nil || !strings.Contains(err.Error(), "go-api") {
		t.Fatalf("expected prereq failure, got %v", err)
	}
	if active, _ := e.Active(); active != nil {
		t.Fatal("failed inject must not record active state")
	}
}

func TestResolve_EscapeHatch(t *testing.T) {
	e, root := testEngine(t, "fault-a")
	ex := executor.New(root)

	if _, err := e.Inject("fault-a", ex, false, false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Resolve("", ex, ""); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if active, _ := e.Active(); active != nil {
		t.Fatal("resolve must clear active state")
	}
	if _, err := os.Stat(filepath.Join(root, "incidents", "fault-a", "BROKEN")); !os.IsNotExist(err) {
		t.Fatal("resolve.sh must have run")
	}

	// No active incident: bare resolve errors, explicit name still works.
	if _, err := e.Resolve("", ex, ""); err == nil {
		t.Fatal("expected ErrNoActive")
	}
	if _, err := e.Resolve("fault-a", ex, ""); err != nil {
		t.Fatalf("explicit resolve must work without active state: %v", err)
	}
}

func TestPickRandom(t *testing.T) {
	e, _ := testEngine(t, "fault-a", "fault-b", "fault-c")

	first, err := e.PickRandom(42, "")
	if err != nil {
		t.Fatalf("PickRandom: %v", err)
	}
	for i := 0; i < 5; i++ {
		again, _ := e.PickRandom(42, "")
		if again.Name != first.Name {
			t.Fatalf("seeded pick must be deterministic: %q vs %q", again.Name, first.Name)
		}
	}

	if _, err := e.PickRandom(1, "network"); err == nil {
		t.Fatal("no eligible faults in category network — expected error")
	}
	if f, err := e.PickRandom(1, "workload"); err != nil || f == nil {
		t.Fatalf("category filter: %v", err)
	}
}

func TestPickRandom_NoEligible(t *testing.T) {
	root := t.TempDir() // prereq app missing
	writeFault(t, root, "fault-a")
	e := NewEngine(root, "k3d.local")
	if _, err := e.PickRandom(1, ""); err == nil {
		t.Fatal("expected no-eligible error")
	}
}

func TestResolveCheckTemplates(t *testing.T) {
	e := NewEngine(t.TempDir(), "lab.example")
	c := e.resolveCheck(checks.Check{Name: "x", Type: "http", URL: "http://go-api.{{.DomainSuffix}}/health"})
	if c.URL != "http://go-api.lab.example/health" {
		t.Fatalf("url not resolved: %q", c.URL)
	}
}
