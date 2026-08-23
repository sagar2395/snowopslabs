// SPDX-License-Identifier: Apache-2.0
package scenario

import (
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
