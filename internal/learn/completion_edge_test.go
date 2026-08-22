// SPDX-License-Identifier: Apache-2.0
package learn

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeMinimalPathYAML writes a minimal valid path.yaml to learnDir/name/path.yaml.
func writeMinimalPathYAML(t *testing.T, learnDir, name, yaml string) {
	t.Helper()
	dir := filepath.Join(learnDir, name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "path.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
}

const minimalValidPathYAML = `name: minimal-path
displayName: Minimal Path
description: A minimal test learning path
modules:
  - name: step-one
    displayName: Step One
    action:
      type: command
      ref: labctl scenario up test
    check:
      name: check-step-one
      type: script
      script: check.sh
`

// TestPathValidate covers all required-field validations.
func TestPathValidate(t *testing.T) {
	tests := []struct {
		name    string
		path    *Path
		wantErr string
	}{
		{
			name:    "missing name",
			path:    &Path{DisplayName: "X", Modules: []Module{{Name: "m", Action: Action{Type: "command", Ref: "x"}, Check: Check{Name: "c", Type: "script"}}}},
			wantErr: "name is required",
		},
		{
			name:    "missing displayName",
			path:    &Path{Name: "x", Modules: []Module{{Name: "m", Action: Action{Type: "command", Ref: "x"}, Check: Check{Name: "c", Type: "script"}}}},
			wantErr: "displayName is required",
		},
		{
			name:    "empty modules",
			path:    &Path{Name: "x", DisplayName: "X", Modules: []Module{}},
			wantErr: "at least one module is required",
		},
		{
			name: "missing action.type",
			path: &Path{
				Name: "x", DisplayName: "X",
				Modules: []Module{{Name: "m", Action: Action{Type: "", Ref: "x"}, Check: Check{Name: "c", Type: "script"}}},
			},
			wantErr: "action.type is required",
		},
		{
			name: "unsafe intro path with dotdot",
			path: &Path{
				Name: "x", DisplayName: "X",
				Modules: []Module{{
					Name:   "m",
					Intro:  "../outside/intro.md",
					Action: Action{Type: "command", Ref: "x"},
					Check:  Check{Name: "c", Type: "script"},
				}},
			},
			wantErr: "intro path must not use ..",
		},
		{
			name: "unsafe intro path absolute",
			path: &Path{
				Name: "x", DisplayName: "X",
				Modules: []Module{{
					Name:   "m",
					Intro:  "/etc/passwd",
					Action: Action{Type: "command", Ref: "x"},
					Check:  Check{Name: "c", Type: "script"},
				}},
			},
			wantErr: "intro path must not use ..",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := tt.path.Validate()
			if len(errs) == 0 {
				t.Fatalf("expected validation error containing %q, got none", tt.wantErr)
			}
			found := false
			for _, e := range errs {
				if strings.Contains(e, tt.wantErr) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected error containing %q, got: %v", tt.wantErr, errs)
			}
		})
	}
}

// TestEngineLoadPath_UnknownPath verifies that LoadPath returns an error for a
// path that does not exist in the learn directory.
func TestEngineLoadPath_UnknownPath(t *testing.T) {
	e, _ := makeEngine(t)
	_, err := e.LoadPath("non-existent-path")
	if err == nil {
		t.Fatal("expected error for unknown path, got nil")
	}
}

// TestEngineProgress_NoneStarted verifies that Progress returns nil (not an
// error) when a path has not been started yet.
func TestEngineProgress_NoneStarted(t *testing.T) {
	e, _ := makeEngine(t)
	prog, err := e.Progress("some-path")
	if err != nil {
		t.Fatalf("Progress() error: %v", err)
	}
	if prog != nil {
		t.Errorf("expected nil progress when not started, got %+v", prog)
	}
}

// TestEnginePaths_CorruptPathSkipped verifies that a directory with a broken
// path.yaml does not cause Paths() to error — the broken entry is silently
// skipped and valid paths still load.
func TestEnginePaths_CorruptPathSkipped(t *testing.T) {
	e, learnDir := makeEngine(t)

	// Write one valid path.
	writeMinimalPathYAML(t, learnDir, "good-path", minimalValidPathYAML)

	// Write one corrupt path (invalid YAML).
	badDir := filepath.Join(learnDir, "bad-path")
	os.MkdirAll(badDir, 0755)
	os.WriteFile(filepath.Join(badDir, "path.yaml"), []byte("modules: [unclosed"), 0644)

	paths, err := e.Paths()
	if err != nil {
		t.Fatalf("Paths() returned error: %v", err)
	}

	// Only the valid path should be returned.
	if len(paths) != 1 {
		t.Errorf("expected 1 valid path, got %d", len(paths))
	}
	if len(paths) > 0 && paths[0].Name != "minimal-path" {
		t.Errorf("expected path name 'minimal-path', got %q", paths[0].Name)
	}
}

// TestEngineLoadPath_Valid verifies that a valid path loads without error and
// has expected fields.
func TestEngineLoadPath_Valid(t *testing.T) {
	e, learnDir := makeEngine(t)
	writeMinimalPathYAML(t, learnDir, "minimal-path", minimalValidPathYAML)

	p, err := e.LoadPath("minimal-path")
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	if p.Name != "minimal-path" {
		t.Errorf("Name: got %q, want %q", p.Name, "minimal-path")
	}
	if len(p.Modules) != 1 {
		t.Errorf("expected 1 module, got %d", len(p.Modules))
	}
}
