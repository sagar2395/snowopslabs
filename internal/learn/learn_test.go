// SPDX-License-Identifier: Apache-2.0
package learn

import (
	"os"
	"path/filepath"
	"testing"
)

func makeEngine(t *testing.T) (*Engine, string) {
	t.Helper()
	dir := t.TempDir()
	learnDir := filepath.Join(dir, "learn")
	stateDir := filepath.Join(dir, ".labctl", "learn")
	if err := os.MkdirAll(learnDir, 0o755); err != nil {
		t.Fatal(err)
	}
	e := New(learnDir, stateDir, "")
	e.now = func() string { return "2026-01-01T00:00:00Z" }
	return e, learnDir
}

const validPathYAML = `
name: test-path
displayName: "Test Path"
description: "A test learning path"
modules:
  - name: step-one
    displayName: "Step One"
    intro: intros/01.md
    action:
      type: command
      ref: "labctl runtime up"
    check:
      name: cluster-up
      type: kubectl
      command: "cluster-info"
      timeoutSeconds: 30
  - name: step-two
    displayName: "Step Two"
    action:
      type: scenario
      ref: observability-sre
    check:
      name: prom-up
      type: http
      url: "http://prometheus.k3d.local/-/ready"
      expectedStatus: 200
      timeoutSeconds: 60
`

func writePathYAML(t *testing.T, learnDir, name, content string) {
	t.Helper()
	dir := filepath.Join(learnDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "path.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeIntro(t *testing.T, learnDir, pathName, relPath, content string) {
	t.Helper()
	full := filepath.Join(learnDir, pathName, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadPath_Valid(t *testing.T) {
	e, learnDir := makeEngine(t)
	writePathYAML(t, learnDir, "test-path", validPathYAML)
	writeIntro(t, learnDir, "test-path", "intros/01.md", "# Hello")

	p, err := e.LoadPath("test-path")
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	if p.Name != "test-path" {
		t.Errorf("name = %q, want test-path", p.Name)
	}
	if len(p.Modules) != 2 {
		t.Errorf("modules = %d, want 2", len(p.Modules))
	}
}

func TestLoadPath_MissingFile(t *testing.T) {
	e, _ := makeEngine(t)
	_, err := e.LoadPath("nonexistent")
	if err == nil {
		t.Fatal("expected error for missing path")
	}
}

func TestValidate_MissingName(t *testing.T) {
	p := &Path{
		DisplayName: "Test",
		Modules: []Module{
			{Name: "m", Action: Action{Type: "command", Ref: "x"}, Check: Check{Name: "c", Type: "http"}},
		},
	}
	errs := p.Validate()
	if len(errs) == 0 {
		t.Fatal("expected validation error for missing name")
	}
}

func TestValidate_PathTraversal(t *testing.T) {
	p := &Path{
		Name:        "bad",
		DisplayName: "Bad",
		Modules: []Module{
			{
				Name:   "m",
				Intro:  "../../../etc/passwd",
				Action: Action{Type: "command", Ref: "x"},
				Check:  Check{Name: "c", Type: "http"},
			},
		},
	}
	errs := p.Validate()
	found := false
	for _, e := range errs {
		if e == "modules[0]: intro path must not use .. or be absolute" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected path traversal error, got: %v", errs)
	}
}

func TestPaths_Empty(t *testing.T) {
	e, _ := makeEngine(t)
	paths, err := e.Paths()
	if err != nil {
		t.Fatalf("Paths: %v", err)
	}
	if len(paths) != 0 {
		t.Errorf("expected 0 paths, got %d", len(paths))
	}
}

func TestPaths_SkipsMalformed(t *testing.T) {
	e, learnDir := makeEngine(t)
	writePathYAML(t, learnDir, "good", validPathYAML)
	writeIntro(t, learnDir, "good", "intros/01.md", "hi")
	writePathYAML(t, learnDir, "bad", "not: valid: yaml: {{{{")

	paths, err := e.Paths()
	if err != nil {
		t.Fatalf("Paths: %v", err)
	}
	if len(paths) != 1 {
		t.Errorf("expected 1 valid path, got %d", len(paths))
	}
	if paths[0].Name != "test-path" {
		t.Errorf("expected test-path, got %q", paths[0].Name)
	}
}

func TestStartPath_And_Progress(t *testing.T) {
	e, learnDir := makeEngine(t)
	writePathYAML(t, learnDir, "test-path", validPathYAML)
	writeIntro(t, learnDir, "test-path", "intros/01.md", "hi")

	prog, err := e.StartPath("test-path")
	if err != nil {
		t.Fatalf("StartPath: %v", err)
	}
	if prog.PathName != "test-path" {
		t.Errorf("path name = %q", prog.PathName)
	}
	if len(prog.CompletedIdxs) != 0 {
		t.Errorf("expected no completed modules at start")
	}

	// Load back
	loaded, err := e.Progress("test-path")
	if err != nil {
		t.Fatalf("Progress: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected saved progress")
	}
	if loaded.StartedAt != "2026-01-01T00:00:00Z" {
		t.Errorf("startedAt = %q", loaded.StartedAt)
	}
}

func TestProgress_NotStarted(t *testing.T) {
	e, _ := makeEngine(t)
	prog, err := e.Progress("nonexistent")
	if err != nil {
		t.Fatalf("Progress: %v", err)
	}
	if prog != nil {
		t.Error("expected nil for not-started path")
	}
}

func TestMarkComplete(t *testing.T) {
	e, learnDir := makeEngine(t)
	writePathYAML(t, learnDir, "test-path", validPathYAML)
	writeIntro(t, learnDir, "test-path", "intros/01.md", "hi")

	prog, _ := e.StartPath("test-path")

	p, _ := e.LoadPath("test-path")
	if NextModuleIdx(p, prog) != 0 {
		t.Fatalf("expected next=0 before any completion")
	}

	if err := e.MarkComplete(prog, 0); err != nil {
		t.Fatalf("MarkComplete: %v", err)
	}
	if NextModuleIdx(p, prog) != 1 {
		t.Errorf("expected next=1 after completing idx 0")
	}

	if err := e.MarkComplete(prog, 1); err != nil {
		t.Fatalf("MarkComplete idx 1: %v", err)
	}
	if NextModuleIdx(p, prog) != -1 {
		t.Errorf("expected -1 when all done")
	}
}

func TestMarkComplete_Idempotent(t *testing.T) {
	e, learnDir := makeEngine(t)
	writePathYAML(t, learnDir, "test-path", validPathYAML)
	writeIntro(t, learnDir, "test-path", "intros/01.md", "hi")

	prog, _ := e.StartPath("test-path")
	_ = e.MarkComplete(prog, 0)
	_ = e.MarkComplete(prog, 0) // second call should be a no-op

	if len(prog.CompletedIdxs) != 1 {
		t.Errorf("expected 1 completed, got %d", len(prog.CompletedIdxs))
	}
}

func TestIntroText(t *testing.T) {
	e, learnDir := makeEngine(t)
	writePathYAML(t, learnDir, "test-path", validPathYAML)
	writeIntro(t, learnDir, "test-path", "intros/01.md", "# Hello Module")

	p, _ := e.LoadPath("test-path")
	text, err := e.IntroText(p, p.Modules[0])
	if err != nil {
		t.Fatalf("IntroText: %v", err)
	}
	if text != "# Hello Module" {
		t.Errorf("intro text = %q", text)
	}
}

func TestIntroText_NoIntro(t *testing.T) {
	e, learnDir := makeEngine(t)
	writePathYAML(t, learnDir, "test-path", validPathYAML)
	writeIntro(t, learnDir, "test-path", "intros/01.md", "hi")

	p, _ := e.LoadPath("test-path")
	// module[1] has no intro
	text, err := e.IntroText(p, p.Modules[1])
	if err != nil {
		t.Fatalf("IntroText: %v", err)
	}
	if text != "" {
		t.Errorf("expected empty intro, got %q", text)
	}
}

func TestCheckStatus_AliasFallback(t *testing.T) {
	// Canonical expectStatus wins when both are set.
	if got := (Check{ExpectStatus: 201, ExpectedStatus: 500}).Status(); got != 201 {
		t.Errorf("Status() = %d, want 201 (canonical wins)", got)
	}
	// Deprecated expectedStatus is honored when canonical is unset.
	if got := (Check{ExpectedStatus: 200}).Status(); got != 200 {
		t.Errorf("Status() = %d, want 200 (alias fallback)", got)
	}
	if got := (Check{}).Status(); got != 0 {
		t.Errorf("Status() = %d, want 0 when neither is set", got)
	}
}
