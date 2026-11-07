// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sagar2395/snowopslabs/internal/config"
	"github.com/sagar2395/snowopslabs/internal/k8s"
	"github.com/sagar2395/snowopslabs/internal/labtest"
)

func TestPlatformState(t *testing.T) {
	tests := []struct {
		name   string
		ready  int
		total  int
		exists bool
		want   string
	}{
		{"namespace absent", 0, 0, false, "  [not installed]"},
		{"namespace but nothing in it", 0, 0, true, "  [no workloads]"},
		{"controller crashlooping", 0, 1, true, "  [degraded 0/1 ready]"},
		{"one of three down", 2, 3, true, "  [degraded 2/3 ready]"},
		{"all ready", 3, 3, true, "  [running]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := platformState(tt.ready, tt.total, tt.exists); got != tt.want {
				t.Errorf("platformState(%d, %d, %v) = %q, want %q",
					tt.ready, tt.total, tt.exists, got, tt.want)
			}
		})
	}
}

func TestPrintUnreachable(t *testing.T) {
	oldCfg := cfg
	t.Cleanup(func() { cfg = oldCfg })
	cfg = &config.Config{Profile: "k3d"}

	tests := []struct {
		name     string
		info     *k8s.ClusterInfo
		expected []string
	}{
		{name: "no context", info: &k8s.ClusterInfo{}, expected: []string{"NO CLUSTER", "labctl init"}},
		{name: "no info", info: nil, expected: []string{"NO CLUSTER"}},
		{
			name:     "configured but not answering",
			info:     &k8s.ClusterInfo{Context: "k3d-snowops", Error: "connection refused"},
			expected: []string{"UNREACHABLE (connection refused)", "k3d-snowops", "labctl doctor", "labctl init"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			printUnreachable(&out, tt.info)
			for _, want := range tt.expected {
				if !strings.Contains(out.String(), want) {
					t.Errorf("output should contain %q:\n%s", want, out.String())
				}
			}
			if strings.Contains(out.String(), "not installed") {
				t.Errorf("an unreachable cluster must not report components as not installed:\n%s", out.String())
			}
		})
	}
}

func TestStatus(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(l *terminal)
		golden  string
		want    []string
	}{
		{"a healthy lab", nil, "status_healthy",
			[]string{"Context:  k3d-contract", "Nodes:    2", "demo-app             replicas=2 ready=2"}},
		{"an app that is not deployed", func(l *terminal) { l.undeploy(labtest.App) }, "status_app_not_deployed",
			[]string{"demo-app             [not deployed]", "go-api               replicas=2 ready=2"}},
		{"no cluster was ever created", func(l *terminal) {
			l.Stub(l.t, "kubectl", "#!/bin/sh\necho 'error: current-context is not set' >&2\nexit 1\n")
		},
			"status_no_cluster", []string{"NO CLUSTER (no kubectl context is set)", "Create the lab with: labctl init"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newTerminal(t)
			if tt.prepare != nil {
				tt.prepare(l)
			}
			r := l.mustRun("status")
			for _, want := range tt.want {
				if !strings.Contains(r.stdout, want) {
					t.Errorf("status does not print %q:\n%s", want, r.stdout)
				}
			}
			l.golden(tt.golden, r)
		})
	}
}

// A Docker VM restart resets the inotify limit, and k3s on a node dies of it
// while Docker still shows the node's container up. status names the node, the
// cause and the recovery, and changes nothing itself.
func TestStatus_NodeStuckAfterVMRestart(t *testing.T) {
	l := newTerminal(t)
	if err := os.Rename(filepath.Join(l.Bin, "kubectl"), filepath.Join(l.Bin, "kubectl.healthy")); err != nil {
		t.Fatal(err)
	}
	l.Stub(t, "kubectl", `#!/bin/sh
case "$*" in
  "get nodes --no-headers") printf 'k3d-clitest-server-0 Ready\nk3d-clitest-agent-0 NotReady\n' ;;
  *) exec "$(dirname "$0")/kubectl.healthy" "$@" ;;
esac
`)
	l.Stub(t, "docker", vmRestartedDocker+`  "logs --tail 200 k3d-clitest-agent-0") echo 'E1107 kubelet: failed to create inotify fd: too many open files' >&2 ;;
esac
exit 0
`)

	r := l.mustRun("status")
	for _, want := range []string{
		"fs.inotify.max_user_instances is 128; the lab needs 512",
		"k3d-clitest-agent-0 is NotReady while its container is up.\n    k3s logged \"too many open files\".",
		"docker exec \"$n\" sysctl -w fs.inotify.max_user_instances=512",
		"docker restart k3d-clitest-agent-0",
	} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("status does not print %q:\n%s", want, r.stdout)
		}
	}
	if strings.Contains(r.stdout, "docker restart k3d-clitest-server-0") {
		t.Errorf("a Ready node needs no restart:\n%s", r.stdout)
	}
	for _, call := range l.Calls(t) {
		if strings.Contains(call, "sysctl") || strings.Contains(call, "docker restart") {
			t.Errorf("status must only report, but ran %q", call)
		}
	}
	l.golden("status_node_stuck", r)
}

// vmRestartedDocker is the start of a docker stand-in for a k3d lab whose
// Docker VM restarted: the nodes run, and the inotify limit is back at 128.
// A test closes its case statement.
const vmRestartedDocker = `#!/bin/sh
[ -n "${STUB_LOG:-}" ] && printf '%s\t%s\n' docker "$*" >>"$STUB_LOG"
case "$*" in
  info) echo "8 16000000000" ;;
  "ps --filter label=k3d.cluster=clitest --format {{.Names}}")
    printf 'k3d-clitest-serverlb\nk3d-clitest-agent-0\nk3d-clitest-server-0\n' ;;
  *"cat /proc/sys/fs/inotify/max_user_instances") echo 128 ;;
`
