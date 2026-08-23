// SPDX-License-Identifier: Apache-2.0
package scenario

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeScenario(t *testing.T, root, name, yaml string) {
	t.Helper()
	dir := filepath.Join(root, "scenarios", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scenario.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Up must refuse an already-active scenario unless force is set. This guards the
// `--force` flag whose "use --force to reinstall" hint previously referenced a
// flag that did not exist.
func TestUp_ForceReinstallsActiveScenario(t *testing.T) {
	root := t.TempDir()
	writeScenario(t, root, "force-test", `name: force-test
displayName: Force Test
description: no components, no prerequisites
category: testing
`)
	eng := NewEngine(root, "k3d.local", "k3d", "monitoring")
	if err := eng.markActive("force-test"); err != nil {
		t.Fatalf("markActive: %v", err)
	}

	rec := &recordingExec{}
	if err := eng.Up("force-test", rec, false); !errors.Is(err, ErrAlreadyActive) {
		t.Fatalf("Up(force=false) on active scenario: got %v, want ErrAlreadyActive", err)
	}
	if err := eng.Up("force-test", rec, true); err != nil {
		t.Fatalf("Up(force=true) on active scenario: %v", err)
	}
}

// A missing app/platform prerequisite must tell the user the exact command to
// fix it, not just a filesystem path.
func TestPreflight_MissingPrereqs_SuggestFixCommands(t *testing.T) {
	root := t.TempDir()
	writeScenario(t, root, "needs-prereqs", `name: needs-prereqs
displayName: Needs Prereqs
description: x
category: testing
prerequisites:
  apps:
    - go-api
  platform:
    - monitoring/grafana
`)
	eng := NewEngine(root, "k3d.local", "k3d", "monitoring")
	s, err := eng.Get("needs-prereqs")
	if err != nil {
		t.Fatal(err)
	}
	err = eng.Preflight(s)
	if err == nil {
		t.Fatal("expected preflight to fail on missing prerequisites")
	}
	msg := err.Error()
	if !strings.Contains(msg, "labctl app deploy go-api") {
		t.Errorf("missing app prereq should suggest 'labctl app deploy go-api'; got:\n%s", msg)
	}
	if !strings.Contains(msg, "labctl platform up") {
		t.Errorf("missing platform prereq should suggest 'labctl platform up'; got:\n%s", msg)
	}
}
