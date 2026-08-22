// SPDX-License-Identifier: Apache-2.0
package services

// Executes the services/pager shell scripts against a stub kubectl so
// syntax errors and unguarded commands are caught in CI without a cluster.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if dirInfo, err := os.Stat(filepath.Join(dir, "services")); err == nil && dirInfo.IsDir() {
			if mk, err := os.Stat(filepath.Join(dir, "Makefile")); err == nil && !mk.IsDir() {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skip("repository root not found (running outside the repo checkout)")
		}
		dir = parent
	}
}

const stubKubectl = `#!/usr/bin/env bash
echo "$@" >> "$KUBECTL_LOG"
if [ -n "${KUBECTL_FAIL_PATTERN:-}" ] && [[ "$*" == *"$KUBECTL_FAIL_PATTERN"* ]]; then
  exit 1
fi
case "$*" in
  *--dry-run=client*) echo "kind: Stub" ;;
esac
if [ "$1" = "apply" ]; then cat > /dev/null 2>&1 || true; fi
exit 0
`

func runPagerScript(t *testing.T, script string, extraEnv ...string) (string, string, error) {
	t.Helper()
	binDir := t.TempDir()
	logFile := filepath.Join(binDir, "kubectl.log")
	if err := os.WriteFile(filepath.Join(binDir, "kubectl"), []byte(stubKubectl), 0755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.Join(repoRoot(t), "services", "pager", script))
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"KUBECTL_LOG="+logFile,
	)
	cmd.Env = append(cmd.Env, extraEnv...)
	out, err := cmd.CombinedOutput()
	log, _ := os.ReadFile(logFile)
	return string(out), string(log), err
}

func TestPagerInstall(t *testing.T) {
	out, log, err := runPagerScript(t, "install.sh")
	if err != nil {
		t.Fatalf("install.sh failed: %v\n%s", err, out)
	}
	for _, want := range []string{"create namespace pager", "rollout status deploy/pager"} {
		if !strings.Contains(log, want) {
			t.Errorf("kubectl log missing %q:\n%s", want, log)
		}
	}
	if !strings.Contains(out, "pager.pager.svc.cluster.local:8080/alert") {
		t.Errorf("install must print the webhook URL:\n%s", out)
	}
}

func TestPagerUninstall(t *testing.T) {
	out, log, err := runPagerScript(t, "uninstall.sh")
	if err != nil {
		t.Fatalf("uninstall.sh failed: %v\n%s", err, out)
	}
	if !strings.Contains(log, "delete namespace pager") {
		t.Errorf("uninstall must delete the namespace:\n%s", log)
	}
}

func TestPagerStatus_NotInstalled(t *testing.T) {
	out, _, err := runPagerScript(t, "status.sh", "KUBECTL_FAIL_PATTERN=get deploy pager")
	if err != nil {
		t.Fatalf("status.sh must exit 0 when not installed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "not installed") {
		t.Errorf("expected not-installed message:\n%s", out)
	}
}
