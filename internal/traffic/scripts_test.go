// SPDX-License-Identifier: Apache-2.0
package traffic

// Integration tests for the services/traffic shell scripts. They run the
// real scripts against a stub kubectl on PATH, so the kubectl call sequence
// and the generated Job manifest are validated in CI without a cluster.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot walks up from the package directory to the project root
// (identified by the scenarios/ and services/ directories plus the Makefile,
// so Go package dirs like internal/services can't be mistaken for it).
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if dirExists(filepath.Join(dir, "scenarios")) && dirExists(filepath.Join(dir, "services")) && fileExists(filepath.Join(dir, "Makefile")) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skip("repository root not found (running outside the repo checkout)")
		}
		dir = parent
	}
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

const stubKubectl = `#!/usr/bin/env bash
# Test stub: log every invocation; capture manifests piped to apply/create;
# fail (exit 1) when the args match $KUBECTL_FAIL_PATTERN.
echo "$@" >> "$KUBECTL_LOG"
if [ -n "${KUBECTL_FAIL_PATTERN:-}" ] && [[ "$*" == *"$KUBECTL_FAIL_PATTERN"* ]]; then
  exit 1
fi
case "$*" in
  *--dry-run=client*) echo "kind: Stub" ;;
esac
if [ "$1" = "apply" ]; then
  cat >> "$KUBECTL_APPLY" 2>/dev/null || true
fi
exit 0
`

type scriptEnv struct {
	logFile   string
	applyFile string
	env       []string
}

// newScriptEnv installs the stub kubectl into a temp PATH dir.
func newScriptEnv(t *testing.T, extraEnv ...string) *scriptEnv {
	t.Helper()
	binDir := t.TempDir()
	se := &scriptEnv{
		logFile:   filepath.Join(binDir, "kubectl.log"),
		applyFile: filepath.Join(binDir, "kubectl.apply"),
	}
	if err := os.WriteFile(filepath.Join(binDir, "kubectl"), []byte(stubKubectl), 0755); err != nil {
		t.Fatal(err)
	}
	se.env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"KUBECTL_LOG="+se.logFile,
		"KUBECTL_APPLY="+se.applyFile,
	)
	se.env = append(se.env, extraEnv...)
	return se
}

func (se *scriptEnv) run(t *testing.T, script string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", filepath.Join(repoRoot(t), ScriptDir, script))
	cmd.Env = se.env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (se *scriptEnv) log() string {
	data, _ := os.ReadFile(se.logFile)
	return string(data)
}

func (se *scriptEnv) applied() string {
	data, _ := os.ReadFile(se.applyFile)
	return string(data)
}

func TestStartScript_AppliesJobWithSettings(t *testing.T) {
	se := newScriptEnv(t,
		"TRAFFIC_PROFILE=spike",
		"TRAFFIC_TARGET=http://echo-server.echo-server.svc.cluster.local:8080/",
		"TRAFFIC_RPS=25",
		"TRAFFIC_DURATION=15m",
	)
	out, err := se.run(t, "start.sh")
	if err != nil {
		t.Fatalf("start.sh failed: %v\n%s", err, out)
	}

	log := se.log()
	for _, want := range []string{
		"create namespace traffic",
		"create configmap traffic-profile",
		"delete job traffic-k6 --namespace traffic --ignore-not-found",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("kubectl log missing %q:\n%s", want, log)
		}
	}

	applied := se.applied()
	for _, want := range []string{
		"name: traffic-k6",
		"traffic-profile: spike",
		"image: grafana/k6:0.50.0",
		`value: "http://echo-server.echo-server.svc.cluster.local:8080/"`,
		`value: "25"`,
		`value: "15m"`,
	} {
		if !strings.Contains(applied, want) {
			t.Errorf("applied manifest missing %q:\n%s", want, applied)
		}
	}
	if strings.Contains(applied, "experimental-prometheus-rw") {
		t.Error("prometheus remote-write must be off by default")
	}
}

func TestStartScript_PrometheusRemoteWriteOptIn(t *testing.T) {
	se := newScriptEnv(t, "K6_PROMETHEUS_RW_SERVER_URL=http://prom.monitoring:9090/api/v1/write")
	out, err := se.run(t, "start.sh")
	if err != nil {
		t.Fatalf("start.sh failed: %v\n%s", err, out)
	}
	if !strings.Contains(se.applied(), "experimental-prometheus-rw") {
		t.Errorf("remote-write opt-in not reflected in k6 args:\n%s", se.applied())
	}
}

func TestStartScript_DefaultsTargetGoAPI(t *testing.T) {
	se := newScriptEnv(t)
	out, err := se.run(t, "start.sh")
	if err != nil {
		t.Fatalf("start.sh failed: %v\n%s", err, out)
	}
	if !strings.Contains(se.applied(), "go-api.go-api.svc.cluster.local:8080/health") {
		t.Errorf("default target should be go-api health endpoint:\n%s", se.applied())
	}
}

func TestStartScript_UnknownProfileFailsAndListsProfiles(t *testing.T) {
	se := newScriptEnv(t, "TRAFFIC_PROFILE=tsunami")
	out, err := se.run(t, "start.sh")
	if err == nil {
		t.Fatalf("expected failure for unknown profile, output:\n%s", out)
	}
	for _, want := range []string{"tsunami", "steady", "spike", "soak"} {
		if !strings.Contains(out, want) {
			t.Errorf("error output should mention %q:\n%s", want, out)
		}
	}
	if log := se.log(); strings.Contains(log, "apply") {
		t.Errorf("nothing must be applied for an unknown profile:\n%s", log)
	}
}

func TestStopScript_CleansUpEverything(t *testing.T) {
	se := newScriptEnv(t)
	out, err := se.run(t, "stop.sh")
	if err != nil {
		t.Fatalf("stop.sh failed: %v\n%s", err, out)
	}
	log := se.log()
	for _, want := range []string{
		"delete job traffic-k6",
		"delete configmap traffic-profile",
		"delete namespace traffic",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("stop.sh must run %q:\n%s", want, log)
		}
	}
}

func TestStopScript_NotRunningIsNoop(t *testing.T) {
	se := newScriptEnv(t, "KUBECTL_FAIL_PATTERN=get namespace")
	out, err := se.run(t, "stop.sh")
	if err != nil {
		t.Fatalf("stop.sh must exit 0 when nothing is running: %v\n%s", err, out)
	}
	if !strings.Contains(out, "not running") {
		t.Errorf("expected 'not running' message:\n%s", out)
	}
	if log := se.log(); strings.Contains(log, "delete") {
		t.Errorf("nothing must be deleted when not running:\n%s", log)
	}
}

func TestStatusScript_NotRunning(t *testing.T) {
	se := newScriptEnv(t, "KUBECTL_FAIL_PATTERN=get namespace")
	out, err := se.run(t, "status.sh")
	if err != nil {
		t.Fatalf("status.sh must exit 0 when not running: %v\n%s", err, out)
	}
	if !strings.Contains(out, "not running") {
		t.Errorf("expected 'not running' message:\n%s", out)
	}
}

func TestStatusScript_Running(t *testing.T) {
	se := newScriptEnv(t)
	out, err := se.run(t, "status.sh")
	if err != nil {
		t.Fatalf("status.sh failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "running") {
		t.Errorf("expected running status:\n%s", out)
	}
}
