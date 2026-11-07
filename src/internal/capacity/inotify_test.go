// SPDX-License-Identifier: Apache-2.0

package capacity

import (
	"slices"
	"testing"

	"github.com/sagar2395/snowopslabs/internal/toolchain"
)

const k3dLabel = "k3d.cluster=snowops"

// labNodes answers `docker ps` with a k3d lab's containers, the load balancer
// first as Docker lists the newest container first.
func labNodes() *toolchain.Fake {
	return toolchain.NewFake().
		WhenArgsContain("ps --filter label="+k3dLabel, "k3d-snowops-serverlb\nk3d-snowops-agent-0\nk3d-snowops-server-0\n", 0)
}

func TestInotifyLimit(t *testing.T) {
	tests := []struct {
		name     string
		fake     *toolchain.Fake
		expected int
		wantErr  bool
	}{
		{
			name:     "read through the first container",
			fake:     labNodes().WhenArgsContain("exec k3d-snowops-serverlb cat /proc/sys/fs/inotify/max_user_instances", "128\n", 0),
			expected: 128,
		},
		{name: "no running container reads nothing", fake: toolchain.NewFake().WhenArgsContain("ps", "", 0)},
		{name: "docker failing is an error", fake: toolchain.NewFake().WhenArgsContain("ps", "", 1), wantErr: true},
		{name: "an unreadable value is an error", fake: labNodes().WhenArgsContain("exec", "n/a\n", 0), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := InotifyLimit(t.Context(), tt.fake, k3dLabel)
			if (err != nil) != tt.wantErr {
				t.Fatalf("InotifyLimit error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.expected {
				t.Errorf("InotifyLimit = %d, want %d", got, tt.expected)
			}
		})
	}
}

func TestRaiseInotifyLimit(t *testing.T) {
	const sysctl = "sysctl -w fs.inotify.max_user_instances=512"

	t.Run("a reset limit is raised on every container", func(t *testing.T) {
		f := labNodes().
			WhenArgsContain("cat /proc/sys", "128\n", 0).
			WhenArgsContain("exec k3d-snowops-serverlb sysctl", "", 1)
		got, err := RaiseInotifyLimit(t.Context(), f, k3dLabel)
		if err != nil {
			t.Fatal(err)
		}
		if got != 128 {
			t.Errorf("RaiseInotifyLimit = %d, want the 128 it read", got)
		}
		for _, node := range []string{"k3d-snowops-agent-0", "k3d-snowops-server-0"} {
			if !f.Called("exec " + node + " " + sysctl) {
				t.Errorf("%s was not raised, calls: %v", node, f.CallStrings())
			}
		}
	})

	t.Run("a limit already high enough is left alone", func(t *testing.T) {
		f := labNodes().WhenArgsContain("cat /proc/sys", "8192\n", 0)
		got, err := RaiseInotifyLimit(t.Context(), f, k3dLabel)
		if err != nil || got != 8192 {
			t.Errorf("RaiseInotifyLimit = %d, %v; want 8192, nil", got, err)
		}
		if f.Called("sysctl") {
			t.Errorf("nothing should be written, calls: %v", f.CallStrings())
		}
	})

	t.Run("no container could raise it", func(t *testing.T) {
		f := labNodes().
			WhenArgsContain("cat /proc/sys", "128\n", 0).
			WhenArgsContain("sysctl", "", 1)
		if _, err := RaiseInotifyLimit(t.Context(), f, k3dLabel); err == nil {
			t.Error("expected an error when every sysctl fails")
		}
	})

	t.Run("a lab that is down is left alone", func(t *testing.T) {
		f := toolchain.NewFake().WhenArgsContain("ps", "", 0)
		got, err := RaiseInotifyLimit(t.Context(), f, k3dLabel)
		if err != nil || got != 0 {
			t.Errorf("RaiseInotifyLimit = %d, %v; want 0, nil", got, err)
		}
		if f.CallCount("exec") != 0 {
			t.Errorf("no container to exec into, calls: %v", f.CallStrings())
		}
	})
}

func TestStuckNodes(t *testing.T) {
	f := labNodes().
		WhenArgsContainStderr("logs --tail 200 k3d-snowops-agent-0", "",
			`E1107 kubelet.go:1600] "Failed to start ContainerManager" err="failed to create inotify fd: too many open files"`+"\n", 0)
	notReady := []string{"k3d-snowops-agent-0", "k3d-snowops-server-0", "k3d-snowops-agent-1-0"}

	got, err := StuckNodes(t.Context(), f, k3dLabel, notReady)
	if err != nil {
		t.Fatal(err)
	}
	// agent-1-0 has no running container: Docker already shows it stopped.
	want := []StuckNode{
		{Name: "k3d-snowops-agent-0", TooManyFiles: true},
		{Name: "k3d-snowops-server-0"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("StuckNodes = %+v, want %+v", got, want)
	}

	t.Run("docker failing is an error", func(t *testing.T) {
		f := toolchain.NewFake().WhenArgsContain("ps", "", 1)
		if _, err := StuckNodes(t.Context(), f, k3dLabel, notReady); err == nil {
			t.Error("expected an error")
		}
	})
}
