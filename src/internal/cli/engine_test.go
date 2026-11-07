// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sagar2395/snowopslabs/internal/config"
	"github.com/sagar2395/snowopslabs/internal/toolchain"
)

// slowSetup is a scenario setup that runs until it is stopped, and records
// that it was stopped rather than killed outright.
const slowSetup = `#!/usr/bin/env bash
dir=$(dirname "$0")
trap 'echo stopped >"$dir/STOPPED"; exit 143' TERM INT
echo started >"$dir/STARTED"
sleep 30 &
wait
`

// Ctrl-C stops a run in flight: labctl returns at once, the script gets a
// signal it can clean up on, and the run is recorded as cancelled rather than
// left running with no parent.
func TestInterrupt_StopsTheRunAndItsScript(t *testing.T) {
	l := newTerminal(t)
	writeDrill(t, l)
	l.Write(t, "scenarios/drill/setup.sh", slowSetup)

	ctx, interrupt := context.WithCancel(t.Context())
	go func() {
		deadline := time.Now().Add(20 * time.Second)
		for !l.exists("scenarios/drill/STARTED") && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
		interrupt()
	}()

	start := time.Now()
	r := l.runCtx(ctx, "scenario", "up", "drill")
	if elapsed := time.Since(start); elapsed > 15*time.Second {
		t.Fatalf("labctl took %s to stop after Ctrl-C", elapsed)
	}
	if r.exitCode() != 1 {
		t.Errorf("exit code = %d, want 1 for an interrupted command", r.exitCode())
	}
	deadline := time.Now().Add(10 * time.Second)
	for !l.exists("scenarios/drill/STOPPED") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !l.exists("scenarios/drill/STOPPED") {
		t.Error("the setup script was not signalled to stop")
	}
	if runs := l.mustRun("runs", "list"); !strings.Contains(runs.stdout, "cancelled") {
		t.Errorf("the interrupted run is not recorded as cancelled:\n%s", runs.stdout)
	}
	if strings.Contains(r.stdout, "the run keeps going") {
		t.Errorf("the start line promises the run outlives Ctrl-C:\n%s", r.stdout)
	}
}

// Ctrl-C ends `scenario verify --watch` between attempts instead of waiting
// out the interval.
func TestInterrupt_StopsVerifyWatch(t *testing.T) {
	l := newTerminal(t)
	writeDrill(t, l)
	l.mustRun("scenario", "up", "drill")

	ctx, interrupt := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer interrupt()
	start := time.Now()
	r := l.runCtx(ctx, "scenario", "verify", "drill", "--watch", "--interval", "1m", "--timeout", "10m")
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("verify --watch took %s to stop after Ctrl-C", elapsed)
	}
	if r.exitCode() != 1 {
		t.Errorf("exit code = %d, want 1", r.exitCode())
	}
}

// A command that runs scripts against the lab puts back an inotify limit that
// a Docker VM restart reset, before its scripts run.
func TestEngineCommands_RestoreTheInotifyLimit(t *testing.T) {
	l := newTerminal(t)
	writeDrill(t, l)
	l.Stub(t, "docker", vmRestartedDocker+"esac\nexit 0\n")

	r := l.mustRun("scenario", "up", "drill")
	if !strings.Contains(r.stdout, "Raised fs.inotify.max_user_instances from 128 to 512") {
		t.Errorf("scenario up does not say it raised the limit:\n%s", r.stdout)
	}
	if !slices.Contains(l.Calls(t), "docker exec k3d-clitest-server-0 sysctl -w fs.inotify.max_user_instances=512") {
		t.Errorf("the limit was not raised on the nodes, calls:\n%s", strings.Join(l.Calls(t), "\n"))
	}
}

func TestRestoreInotifyLimit(t *testing.T) {
	oldCfg := cfg
	t.Cleanup(func() { cfg = oldCfg })

	tests := []struct {
		name      string
		profile   string
		limit     string
		wantRaise bool
	}{
		{name: "a reset limit is raised", profile: "k3d", limit: "128", wantRaise: true},
		{name: "a limit already high enough is left alone", profile: "k3d", limit: "512"},
		{name: "kind is left alone", profile: "kind", limit: "128"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg = &config.Config{Profile: tt.profile, ClusterName: "lab"}
			f := toolchain.NewFake().
				WhenArgsContain("ps --filter label=k3d.cluster=lab", "k3d-lab-server-0\n", 0).
				WhenArgsContain("cat /proc/sys/fs/inotify/max_user_instances", tt.limit+"\n", 0)
			var out bytes.Buffer
			restoreInotifyLimit(t.Context(), &out, f)
			if f.Called("sysctl") != tt.wantRaise {
				t.Errorf("sysctl called = %v, want %v; calls: %v", f.Called("sysctl"), tt.wantRaise, f.CallStrings())
			}
			if said := strings.Contains(out.String(), "Raised"); said != tt.wantRaise {
				t.Errorf("output %q, want a Raised line: %v", out.String(), tt.wantRaise)
			}
		})
	}

	t.Run("a lab that cannot be read says nothing", func(t *testing.T) {
		cfg = &config.Config{Profile: "k3d", ClusterName: "lab"}
		f := toolchain.NewFake().WhenArgsContain("ps", "", 1)
		var out bytes.Buffer
		restoreInotifyLimit(t.Context(), &out, f)
		if out.Len() != 0 {
			t.Errorf("output = %q, want none", out.String())
		}
	})
}
