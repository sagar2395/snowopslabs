// SPDX-License-Identifier: Apache-2.0
package scenario

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sagar2395/snowopslabs/pkg/checks"
	"github.com/sagar2395/snowopslabs/pkg/extension"
)

const noCheckScenarioYAML = `name: no-checks-scenario
displayName: No Checks Scenario
description: A scenario with no checks for testing ErrNoChecks
category: testing
components: []
`

func TestGet_UnknownNameReturnsError(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "scenarios"), 0755)

	engine := NewEngine(root, "k3d.local", "k3d")
	_, err := engine.Get("this-does-not-exist")
	if err == nil {
		t.Fatal("expected error for unknown scenario, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention 'not found': %v", err)
	}
}

func TestUp_AlreadyActiveReturnsErrAlreadyActive(t *testing.T) {
	root := t.TempDir()
	createTestScenario(t, root, "minimal-scenario", minimalScenarioYAML)

	engine := NewEngine(root, "k3d.local", "k3d")

	// First Up should succeed (no components, no executor needed).
	if err := engine.Up("minimal-scenario", nil, false); err != nil {
		t.Fatalf("first Up: %v", err)
	}

	// Second Up should return ErrAlreadyActive.
	err := engine.Up("minimal-scenario", nil, false)
	if err == nil {
		t.Fatal("expected ErrAlreadyActive on second Up, got nil")
	}
	if !errors.Is(err, ErrAlreadyActive) {
		t.Errorf("expected errors.Is(err, ErrAlreadyActive), got: %v", err)
	}
}

func TestVerify_NoChecksReturnsErrNoChecks(t *testing.T) {
	root := t.TempDir()
	createTestScenario(t, root, "no-checks-scenario", noCheckScenarioYAML)

	engine := NewEngine(root, "k3d.local", "k3d")
	runner := checks.NewRunner()

	_, err := engine.Verify(context.Background(), "no-checks-scenario", runner)
	if err == nil {
		t.Fatal("expected ErrNoChecks, got nil")
	}
	if !errors.Is(err, ErrNoChecks) {
		t.Errorf("expected errors.Is(err, ErrNoChecks), got: %v", err)
	}
}

// errHookDenied is a Hooks implementation whose PreStage returns an error,
// so we can test that the engine propagates hook errors.
type errHookDenied struct {
	extension.NoopHooks
}

func (errHookDenied) PreStage(_ context.Context, _ extension.Event) error {
	return errors.New("hook: stage denied by policy")
}

func TestUp_HookDenied_ReturnsError(t *testing.T) {
	root := t.TempDir()
	// A no-component scenario will still run the stage loop and call hooks.
	// We inject an error-returning PreStage hook to simulate a policy denial.
	createTestScenario(t, root, "minimal-scenario", minimalScenarioYAML)

	engine := NewEngine(root, "k3d.local", "k3d")
	engine.Hooks = errHookDenied{}

	err := engine.Up("minimal-scenario", nil, false)
	if err == nil {
		t.Fatal("expected error from hook denial, got nil")
	}
	if !strings.Contains(err.Error(), "hook") {
		t.Errorf("error should mention hook: %v", err)
	}
}

func TestNewEngine_CorruptedYAML_PopulatesLoadErrors(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "scenarios", "corrupt-scenario")
	os.MkdirAll(dir, 0755)
	// Write deliberately broken YAML.
	os.WriteFile(filepath.Join(dir, "scenario.yaml"), []byte("name: [unclosed bracket"), 0644)

	engine := NewEngine(root, "k3d.local", "k3d")

	errs := engine.LoadErrors()
	if len(errs) == 0 {
		t.Fatal("expected loadErrors to be populated for corrupt YAML, got none")
	}
	if _, ok := errs["corrupt-scenario"]; !ok {
		t.Errorf("expected error keyed by 'corrupt-scenario', got keys: %v", errs)
	}
}

func TestVerify_UnknownScenarioReturnsError(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "scenarios"), 0755)

	engine := NewEngine(root, "k3d.local", "k3d")
	runner := checks.NewRunner()

	_, err := engine.Verify(context.Background(), "ghost-scenario", runner)
	if err == nil {
		t.Fatal("expected error for unknown scenario in Verify, got nil")
	}
}
