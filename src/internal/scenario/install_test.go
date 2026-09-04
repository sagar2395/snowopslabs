// SPDX-License-Identifier: Apache-2.0
package scenario

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// recordingExec is a CommandExecutor that records every command instead of
// running it, so install/uninstall can be tested without a cluster.
type recordingExec struct {
	calls [][]string        // each entry: {name, arg, arg, ...}
	files map[string]string // snapshot of any "-f <path>" file, read at call time
}

func (r *recordingExec) RunCommandStreamed(_, name string, args ...string) (string, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	// Temp files passed via -f are deleted by the caller's defer once this
	// returns, so snapshot their contents now while they still exist.
	for i, a := range args {
		if a == "-f" && i+1 < len(args) {
			if b, err := os.ReadFile(args[i+1]); err == nil {
				if r.files == nil {
					r.files = map[string]string{}
				}
				r.files[args[i+1]] = string(b)
			}
		}
	}
	return "", nil
}

func (r *recordingExec) RunScriptStreamed(_, scriptPath string, args ...string) (string, error) {
	r.calls = append(r.calls, append([]string{scriptPath}, args...))
	return "", nil
}

// find returns the first recorded call to binary that contains needle among its
// arguments, or nil.
func (r *recordingExec) find(binary, needle string) []string {
	for _, c := range r.calls {
		if c[0] != binary {
			continue
		}
		for _, a := range c[1:] {
			if a == needle {
				return c
			}
		}
	}
	return nil
}

// flagValue returns the argument following flag in a recorded call.
func flagValue(call []string, flag string) string {
	for i, a := range call {
		if a == flag && i+1 < len(call) {
			return call[i+1]
		}
	}
	return ""
}

func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "scenarios"), 0o755); err != nil {
		t.Fatal(err)
	}
	return NewEngine(root, "k3d.local", "k3d", "observability")
}

// installHelm and uninstallHelm must resolve the component namespace the SAME
// way. A regression where uninstall used the raw "{{.MonitoringNamespace}}"
// meant `helm uninstall` ran against the wrong namespace and the release leaked
// while `scenario down` reported success.
func TestHelm_InstallAndUninstallUseSameResolvedNamespace(t *testing.T) {
	eng := newTestEngine(t)
	comp := &Component{
		Name:      "loki",
		Type:      "helm",
		Chart:     "grafana/loki",
		Namespace: "{{.MonitoringNamespace}}",
	}
	s := &Scenario{Dir: t.TempDir()}

	install := &recordingExec{}
	if err := eng.installHelm(s, comp, install); err != nil {
		t.Fatalf("installHelm: %v", err)
	}
	got := eng.namespaceOf(install.find("helm", "upgrade"))
	if got != "observability" {
		t.Errorf("install namespace = %q, want %q", got, "observability")
	}

	uninstall := &recordingExec{}
	if err := eng.uninstallHelm(comp, uninstall); err != nil {
		t.Fatalf("uninstallHelm: %v", err)
	}
	got = eng.namespaceOf(uninstall.find("helm", "uninstall"))
	if got != "observability" {
		t.Errorf("uninstall namespace = %q, want %q (raw template must not leak)", got, "observability")
	}
}

// namespaceOf reads the --namespace value from a recorded helm call.
func (e *Engine) namespaceOf(call []string) string { return flagValue(call, "--namespace") }

// failingExec returns an error from every RunCommandStreamed call, simulating a
// kubectl apply that the cluster rejects.
type failingExec struct{ recordingExec }

func (f *failingExec) RunCommandStreamed(label, name string, args ...string) (string, error) {
	_, _ = f.recordingExec.RunCommandStreamed(label, name, args...)
	return "", fmt.Errorf("kubectl apply rejected the manifest")
}

// installGrafanaDashboard must resolve the namespace template like the other
// installers (a raw "{{.MonitoringNamespace}}" produced an invalid ConfigMap
// that kubectl rejected), and uninstall must target the same resolved namespace
// so `scenario down` deletes what `up` created.
func TestGrafanaDashboard_InstallAndUninstallUseSameResolvedNamespace(t *testing.T) {
	eng := newTestEngine(t)
	dir := t.TempDir()
	dashDir := filepath.Join(dir, "dashboards")
	if err := os.MkdirAll(dashDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dashDir, "autoscaling.json"), []byte(`{"title":"Autoscaling"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Scenario{Name: "autoscaling-under-load", Dir: dir}
	comp := &Component{
		Name:      "autoscaling-dashboard",
		Type:      "grafana-dashboard",
		Path:      "dashboards",
		Namespace: "{{.MonitoringNamespace}}",
	}

	install := &recordingExec{}
	if err := eng.installGrafanaDashboard(s, comp, install); err != nil {
		t.Fatalf("installGrafanaDashboard: %v", err)
	}
	apply := install.find("kubectl", "apply")
	if apply == nil {
		t.Fatalf("expected a kubectl apply call, got %v", install.calls)
	}
	out := install.files[flagValue(apply, "-f")]
	if strings.Contains(out, "{{.MonitoringNamespace}}") {
		t.Errorf("raw template leaked into ConfigMap:\n%s", out)
	}
	if !strings.Contains(out, "namespace: observability") {
		t.Errorf("namespace not resolved to observability:\n%s", out)
	}

	uninstall := &recordingExec{}
	if err := eng.uninstallGrafanaDashboard(s, comp, uninstall); err != nil {
		t.Fatalf("uninstallGrafanaDashboard: %v", err)
	}
	del := uninstall.find("kubectl", "delete")
	if del == nil {
		t.Fatalf("expected a kubectl delete call, got %v", uninstall.calls)
	}
	if got := flagValue(del, "--namespace"); got != "observability" {
		t.Errorf("uninstall namespace = %q, want %q (raw template must not leak)", got, "observability")
	}
}

// A dashboard apply that the cluster rejects must fail the activation, not be
// silently swallowed — the false-success bug the audit flagged as P0.
func TestGrafanaDashboard_ApplyFailurePropagates(t *testing.T) {
	eng := newTestEngine(t)
	dir := t.TempDir()
	dashDir := filepath.Join(dir, "dashboards")
	if err := os.MkdirAll(dashDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dashDir, "autoscaling.json"), []byte(`{"title":"Autoscaling"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Scenario{Name: "autoscaling-under-load", Dir: dir}
	comp := &Component{Name: "autoscaling-dashboard", Type: "grafana-dashboard", Path: "dashboards"}

	if err := eng.installGrafanaDashboard(s, comp, &failingExec{}); err == nil {
		t.Fatal("expected installGrafanaDashboard to return the apply error, got nil")
	}
}

// installManifest must render labctl's own template vars before applying, while
// leaving foreign templating (Prometheus/Grafana) untouched.
func TestInstallManifest_RendersLabctlVarsOnly(t *testing.T) {
	eng := newTestEngine(t)
	dir := t.TempDir()
	manifest := "kind: PrometheusRule\nmetadata:\n  namespace: \"{{.MonitoringNamespace}}\"\n" +
		"annotations:\n  desc: \"{{ $value }}\"\n"
	if err := os.WriteFile(filepath.Join(dir, "rules.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Scenario{Dir: dir}
	comp := &Component{Name: "alerting-rules", Type: "manifest", Path: "rules.yaml"}

	rec := &recordingExec{}
	if err := eng.installManifest(s, comp, rec); err != nil {
		t.Fatalf("installManifest: %v", err)
	}
	apply := rec.find("kubectl", "apply")
	if apply == nil {
		t.Fatalf("expected a kubectl apply call, got %v", rec.calls)
	}
	// The applied file is a temp file the caller deletes on return; the recorder
	// snapshotted its contents at call time.
	out := rec.files[flagValue(apply, "-f")]
	if out == "" {
		t.Fatalf("recorder did not capture the applied manifest; calls=%v", rec.calls)
	}
	if !strings.Contains(out, `namespace: "observability"`) {
		t.Errorf("labctl var not rendered:\n%s", out)
	}
	if !strings.Contains(out, `{{ $value }}`) {
		t.Errorf("foreign template was mangled (should be left intact):\n%s", out)
	}
}

// scriptedExec fails a configured number of times with a fixed output before
// succeeding, so the retry paths can be exercised without a cluster.
type scriptedExec struct {
	recordingExec
	failures map[string]int    // command signature -> remaining failures
	output   map[string]string // command signature -> combined output to return
}

func (s *scriptedExec) RunCommandStreamed(label, name string, args ...string) (string, error) {
	out, _ := s.recordingExec.RunCommandStreamed(label, name, args...)
	sig := name
	if len(args) > 0 {
		sig = name + " " + args[0]
	}
	if n := s.failures[sig]; n > 0 {
		s.failures[sig] = n - 1
		return s.output[sig], fmt.Errorf("exit status 1")
	}
	return out, nil
}

// A scenario must never rewrite a release the platform owns: helm upgrade over
// an existing StatefulSet release fails on immutable spec fields.
func TestHelm_AdoptSkipsInstallWhenReleaseExists(t *testing.T) {
	tests := []struct {
		name         string
		adopt        bool
		releaseFound bool
		wantInstall  bool
	}{
		{"adopt with existing release is skipped", true, true, false},
		{"adopt with no release installs", true, false, true},
		{"without adopt always installs", false, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eng := newTestEngine(t)
			eng.SetOutput(io.Discard)
			rec := &scriptedExec{failures: map[string]int{}, output: map[string]string{}}
			if !tt.releaseFound {
				rec.failures["helm status"] = 1
			}
			comp := &Component{Name: "loki", Type: "helm", Chart: "grafana/loki", Adopt: tt.adopt}

			if err := eng.installHelm(&Scenario{Dir: t.TempDir()}, comp, rec); err != nil {
				t.Fatalf("installHelm: %v", err)
			}
			gotInstall := rec.find("helm", "upgrade") != nil
			if gotInstall != tt.wantInstall {
				t.Errorf("helm upgrade ran = %v, want %v (calls: %v)", gotInstall, tt.wantInstall, rec.calls)
			}
		})
	}
}

// The platform's values are the base; a scenario overlay is layered on top.
// Helm applies -f files left to right, so order decides which one wins.
func TestHelm_PlatformValuesAreLayeredUnderScenarioOverlay(t *testing.T) {
	eng := newTestEngine(t)
	base := filepath.Join(eng.ProjectRoot, "platform", "logging", "loki")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "values.yaml"), []byte("platform: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scenarioDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(scenarioDir, "overlay.yaml"), []byte("overlay: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := &recordingExec{}
	comp := &Component{
		Name: "loki", Type: "helm", Chart: "grafana/loki",
		PlatformValues: "logging/loki", ValuesFile: "overlay.yaml",
	}
	if err := eng.installHelm(&Scenario{Dir: scenarioDir}, comp, rec); err != nil {
		t.Fatalf("installHelm: %v", err)
	}

	call := rec.find("helm", "upgrade")
	var seen []string
	for i, a := range call {
		if a == "-f" && i+1 < len(call) {
			seen = append(seen, rec.files[call[i+1]])
		}
	}
	if len(seen) != 2 {
		t.Fatalf("expected 2 values files, got %d (%v)", len(seen), call)
	}
	if !strings.Contains(seen[0], "platform: true") {
		t.Errorf("first -f should be the platform base, got %q", seen[0])
	}
	if !strings.Contains(seen[1], "overlay: true") {
		t.Errorf("second -f should be the scenario overlay, got %q", seen[1])
	}
}

func TestHelm_MissingPlatformValuesIsAnError(t *testing.T) {
	eng := newTestEngine(t)
	rec := &recordingExec{}
	comp := &Component{Name: "loki", Type: "helm", Chart: "grafana/loki", PlatformValues: "logging/nope"}

	err := eng.installHelm(&Scenario{Dir: t.TempDir()}, comp, rec)
	if err == nil {
		t.Fatal("expected an error naming the missing platform values file")
	}
	if !strings.Contains(err.Error(), "logging/nope") {
		t.Errorf("error should name the component path, got: %v", err)
	}
}

func TestImmutableStatefulSet(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want string
	}{
		{
			name: "server-side apply failure",
			out: `Error: UPGRADE FAILED: server-side apply failed for object monitoring/loki apps/v1, ` +
				`Kind=StatefulSet: StatefulSet.apps "loki" is invalid: spec: Forbidden: ` +
				`updates to statefulset spec for fields other than 'replicas' are forbidden`,
			want: "loki",
		},
		{
			name: "three-way merge patch failure",
			out: `Error: UPGRADE FAILED: cannot patch "tempo" with kind StatefulSet: ` +
				`StatefulSet.apps "tempo" is invalid: spec: Forbidden: ` +
				`updates to statefulset spec for fields other than 'replicas' are forbidden`,
			want: "tempo",
		},
		{"an unrelated failure is left alone", "Error: UPGRADE FAILED: timed out waiting for the condition", ""},
		{"empty output", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := immutableStatefulSet(tt.out); got != tt.want {
				t.Errorf("immutableStatefulSet() = %q, want %q", got, tt.want)
			}
		})
	}
}

// Re-running a scenario over an existing Loki/Tempo release used to dead-end on
// the immutable-StatefulSet error. Orphaning the StatefulSet and retrying keeps
// the scenario idempotent.
func TestHelm_RecoversFromImmutableStatefulSet(t *testing.T) {
	eng := newTestEngine(t)
	eng.SetOutput(io.Discard)
	rec := &scriptedExec{
		failures: map[string]int{"helm upgrade": 1},
		output: map[string]string{"helm upgrade": `Error: UPGRADE FAILED: server-side apply failed for ` +
			`object monitoring/loki apps/v1, Kind=StatefulSet: StatefulSet.apps "loki" is invalid: ` +
			`spec: Forbidden: updates to statefulset spec for fields other than 'replicas' are forbidden`},
	}
	comp := &Component{Name: "loki", Type: "helm", Chart: "grafana/loki", Namespace: "monitoring"}

	if err := eng.installHelm(&Scenario{Dir: t.TempDir()}, comp, rec); err != nil {
		t.Fatalf("installHelm should recover from the immutable-field error: %v", err)
	}

	del := rec.find("kubectl", "statefulset")
	if del == nil {
		t.Fatal("expected the StatefulSet to be deleted before the retry")
	}
	if !strings.Contains(strings.Join(del, " "), "--cascade=orphan") {
		t.Errorf("delete must orphan the pods, got: %v", del)
	}
	var upgrades int
	for _, c := range rec.calls {
		if c[0] == "helm" && len(c) > 1 && c[1] == "upgrade" {
			upgrades++
		}
	}
	if upgrades != 2 {
		t.Errorf("expected the upgrade to be retried once, got %d attempts", upgrades)
	}
}

// A failure that is not the immutable-StatefulSet case must surface, not retry.
func TestHelm_UnrelatedFailureIsNotRetried(t *testing.T) {
	eng := newTestEngine(t)
	eng.SetOutput(io.Discard)
	rec := &scriptedExec{
		failures: map[string]int{"helm upgrade": 1},
		output:   map[string]string{"helm upgrade": "Error: UPGRADE FAILED: timed out waiting for the condition"},
	}
	comp := &Component{Name: "loki", Type: "helm", Chart: "grafana/loki", Namespace: "monitoring"}

	if err := eng.installHelm(&Scenario{Dir: t.TempDir()}, comp, rec); err == nil {
		t.Fatal("expected the original failure to surface")
	}
	if rec.find("kubectl", "statefulset") != nil {
		t.Error("must not delete a StatefulSet for an unrelated failure")
	}
}

// A script component's side effects must be reversible: `scenario down` runs
// its uninstallScript, so wiring an app to a collector is undone when the
// collector is removed.
func TestScript_UninstallScriptRunsOnTeardown(t *testing.T) {
	tests := []struct {
		name            string
		uninstallScript string
		wantScript      string
	}{
		{"reverses via the uninstall script", "scripts/disable-tracing.sh", "scripts/disable-tracing.sh"},
		{"no uninstall script is a no-op", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eng := newTestEngine(t)
			s := &Scenario{Dir: filepath.Join(eng.ProjectRoot, "scenarios", "obs")}
			comp := &Component{
				Name: "wire", Type: "script",
				Script: "scripts/enable-tracing.sh", UninstallScript: tt.uninstallScript,
			}

			rec := &recordingExec{}
			if err := eng.uninstallComponent(s, comp, rec); err != nil {
				t.Fatalf("uninstallComponent: %v", err)
			}
			if tt.wantScript == "" {
				if len(rec.calls) != 0 {
					t.Errorf("expected no commands, got %v", rec.calls)
				}
				return
			}
			if len(rec.calls) != 1 || !strings.HasSuffix(rec.calls[0][0], tt.wantScript) {
				t.Errorf("expected %q to run, got %v", tt.wantScript, rec.calls)
			}
		})
	}
}

// adopt is "borrow, not own": a scenario that reused a platform release must
// leave it alone on teardown, or `scenario down` takes Loki and Tempo with it.
func TestHelm_AdoptedReleaseIsNotUninstalled(t *testing.T) {
	tests := []struct {
		name          string
		adopt         bool
		wantUninstall bool
	}{
		{"adopted release survives teardown", true, false},
		{"scenario-owned release is removed", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eng := newTestEngine(t)
			eng.SetOutput(io.Discard)
			rec := &recordingExec{}
			comp := &Component{Name: "loki", Type: "helm", Chart: "grafana/loki", Adopt: tt.adopt}

			if err := eng.uninstallHelm(comp, rec); err != nil {
				t.Fatalf("uninstallHelm: %v", err)
			}
			got := rec.find("helm", "uninstall") != nil
			if got != tt.wantUninstall {
				t.Errorf("helm uninstall ran = %v, want %v", got, tt.wantUninstall)
			}
		})
	}
}
