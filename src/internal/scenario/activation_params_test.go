// SPDX-License-Identifier: Apache-2.0
package scenario

import (
	"github.com/sagar2395/snowopslabs/internal/workload"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestActiveParamsRoundTrip(t *testing.T) {
	e := &Engine{stateDir: t.TempDir()}
	want := map[string]string{"MinReplicas": "2", "MaxTailAmplification": "6"}
	if err := e.markActive("demo", want); err != nil {
		t.Fatalf("markActive: %v", err)
	}
	got := e.activeParams("demo")
	if len(got) != len(want) {
		t.Fatalf("activeParams() = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("activeParams()[%q] = %q, want %q", k, got[k], v)
		}
	}
	if !e.isActive("demo") {
		t.Error("isActive() = false after markActive")
	}
}

// An activation from an older build wrote the literal text "active". It must
// still read as active, with no parameters, rather than as a corrupt file.
func TestActiveParamsReadsLegacyMarker(t *testing.T) {
	dir := t.TempDir()
	e := &Engine{stateDir: dir}
	if err := os.WriteFile(filepath.Join(dir, "legacy.active"), []byte("active"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !e.isActive("legacy") {
		t.Error("isActive() = false for a legacy marker")
	}
	if got := e.activeParams("legacy"); got != nil {
		t.Errorf("activeParams() = %v, want nil", got)
	}
}

// A windowed check grades this run: the window must open at activation, reset
// on re-activation, and exist (as the smallest range) when nothing is active.
func TestSinceActivationWindowsTheRun(t *testing.T) {
	dir := t.TempDir()
	e := &Engine{stateDir: dir}
	s := &Scenario{Name: "demo"}
	query := "max_over_time(x[{{.SinceActivation}}:1m])"

	restore := e.withActivationParams(s)
	if got, want := e.resolveTemplate(query), "max_over_time(x[1m:1m])"; got != want {
		t.Errorf("inactive: resolveTemplate() = %q, want %q", got, want)
	}
	restore()

	if err := e.markActive("demo", nil); err != nil {
		t.Fatal(err)
	}
	activated := time.Now().Add(-(94*time.Minute + 30*time.Second))
	if err := os.Chtimes(filepath.Join(dir, "demo.active"), activated, activated); err != nil {
		t.Fatal(err)
	}
	restore = e.withActivationParams(s)
	if got, want := e.resolveTemplate(query), "max_over_time(x[95m:1m])"; got != want {
		t.Errorf("active 94.5m: resolveTemplate() = %q, want %q", got, want)
	}
	restore()
	if !e.activatedAt.IsZero() {
		t.Errorf("activatedAt = %v after restore, want zero", e.activatedAt)
	}

	if err := e.markActive("demo", nil); err != nil {
		t.Fatal(err)
	}
	defer e.withActivationParams(s)()
	if got, want := e.resolveTemplate(query), "max_over_time(x[1m:1m])"; got != want {
		t.Errorf("re-activated: resolveTemplate() = %q, want %q", got, want)
	}
}

func TestActiveParamsMissingScenario(t *testing.T) {
	e := &Engine{stateDir: t.TempDir()}
	if got := e.activeParams("never-activated"); got != nil {
		t.Errorf("activeParams() = %v, want nil", got)
	}
}

// A scenario with no parameters keeps the plain marker, so nothing changes for
// the scenarios that never had any.
func TestMarkActiveWithoutParamsStaysPlain(t *testing.T) {
	dir := t.TempDir()
	e := &Engine{stateDir: dir}
	if err := e.markActive("plain", nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "plain.active"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "active" {
		t.Errorf("marker = %q, want %q", data, "active")
	}
}

// Down re-renders the same manifests the install rendered, so it must resolve
// the same parameters. It did not, and a manifest carrying a {{.Param}} was
// handed to `kubectl delete` unresolved: the delete failed, teardown reported
// success, and the object was left behind.
func TestWithActivationParamsScopesAndRestores(t *testing.T) {
	e := &Engine{stateDir: t.TempDir()}
	s := &Scenario{
		Name:       "demo",
		Parameters: []Parameter{{Name: "MinReplicas", Default: "1", Type: "int"}},
	}

	// No activation recorded: the declared defaults are used.
	restore := e.withActivationParams(s)
	if got := e.resolveTemplate("{{.MinReplicas}}"); got != "1" {
		t.Errorf("with defaults: got %q, want %q", got, "1")
	}
	restore()
	if e.resolvedParams != nil {
		t.Error("restore() left resolvedParams set")
	}

	// Activated with an override: that value is used, not the default.
	if err := e.markActive("demo", map[string]string{"MinReplicas": "4"}); err != nil {
		t.Fatal(err)
	}
	restore = e.withActivationParams(s)
	if got := e.resolveTemplate("{{.MinReplicas}}"); got != "4" {
		t.Errorf("with activation override: got %q, want %q", got, "4")
	}
	restore()
}

// The manifest that exposed this: unquoted so the CRD sees an integer, which
// means it is only valid YAML once the parameter is substituted.
func TestParamsResolveInsideAManifestBody(t *testing.T) {
	e := &Engine{stateDir: t.TempDir()}
	s := &Scenario{Name: "demo", Parameters: []Parameter{
		{Name: "MinReplicas", Default: "1", Type: "int"},
		{Name: "MaxReplicas", Default: "6", Type: "int"},
	}}
	defer e.withActivationParams(s)()

	const body = "spec:\n  minReplicaCount: {{.MinReplicas}}\n  maxReplicaCount: {{.MaxReplicas}}\n"
	got := e.resolveTemplate(body)
	want := "spec:\n  minReplicaCount: 1\n  maxReplicaCount: 6\n"
	if got != want {
		t.Errorf("resolveTemplate(manifest) =\n%q\nwant\n%q", got, want)
	}
}

// Verify and Down run in a later process than Up, so the binding cannot come
// from ambient config: a scenario brought up against java-api was otherwise
// graded and torn down against whatever APP_NAME happened to say.
func TestActivationRecordsTheAppItRanAgainst(t *testing.T) {
	root := t.TempDir()
	e := NewEngine(root, "k3d.local", "k3d")
	e.Workload = workload.Default("java-api")
	if err := e.markActive("some-scenario", map[string]string{"Threshold": "15"}); err != nil {
		t.Fatalf("markActive: %v", err)
	}
	if got := e.ActiveApp("some-scenario"); got != "java-api" {
		t.Errorf("ActiveApp = %q, want java-api", got)
	}
	if got := e.activeParams("some-scenario")["Threshold"]; got != "15" {
		t.Errorf("params survived alongside the app? Threshold = %q, want 15", got)
	}
	if got := e.ActiveApp("never-activated"); got != "" {
		t.Errorf("ActiveApp for an inactive scenario = %q, want empty", got)
	}
}
