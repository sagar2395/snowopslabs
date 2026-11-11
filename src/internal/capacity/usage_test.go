// SPDX-License-Identifier: Apache-2.0

package capacity

import (
	"errors"
	"testing"

	"github.com/snowops-oss/snowopslabs/internal/toolchain"
)

const meminfo4GB = `MemTotal:        3998720 kB
MemFree:          137216 kB
MemAvailable:    2032640 kB
Buffers:           10240 kB
Cached:          2086912 kB
`

func TestNodeLabel(t *testing.T) {
	tests := []struct {
		name     string
		profile  string
		expected string
		ok       bool
	}{
		{name: "k3d", profile: "k3d", expected: "k3d.cluster=lab", ok: true},
		{name: "kind", profile: "kind", expected: "io.x-k8s.kind.cluster=lab", ok: true},
		{name: "incluster has no node containers", profile: "incluster", expected: "", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := NodeLabel(tt.profile, "lab")
			if got != tt.expected || ok != tt.ok {
				t.Errorf("NodeLabel(%q) = %q, %v; want %q, %v", tt.profile, got, ok, tt.expected, tt.ok)
			}
		})
	}
}

func TestUsage(t *testing.T) {
	tests := []struct {
		name     string
		fake     func() *toolchain.Fake
		expected int
		wantErr  bool
	}{
		{
			name: "total minus available on the docker machine",
			fake: func() *toolchain.Fake {
				return toolchain.NewFake().
					WhenArgsContain("ps -q", "server\nagent\n", 0).
					WhenArgsContain("exec server cat /proc/meminfo", meminfo4GB, 0)
			},
			expected: 1920,
		},
		{
			name: "no running lab container uses nothing",
			fake: func() *toolchain.Fake {
				return toolchain.NewFake().WhenArgsContain("ps -q", "", 0)
			},
			expected: 0,
		},
		{
			name: "docker failing is an error",
			fake: func() *toolchain.Fake {
				return toolchain.NewFake().WhenArgsContain("ps -q", "", 1)
			},
			wantErr: true,
		},
		{
			name: "an unreadable meminfo is an error",
			fake: func() *toolchain.Fake {
				return toolchain.NewFake().
					WhenArgsContain("ps -q", "server\n", 0).
					WhenArgsContain("exec", "", 1)
			},
			wantErr: true,
		},
		{
			name: "docker not installed",
			fake: func() *toolchain.Fake {
				f := toolchain.NewFake()
				f.Available = map[string]string{}
				f.LookPathErr = errors.New("not found")
				return f
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Usage(t.Context(), tt.fake(), "k3d.cluster=snowops")
			if (err != nil) != tt.wantErr {
				t.Fatalf("Usage error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.expected {
				t.Errorf("Usage = %d, want %d", got, tt.expected)
			}
		})
	}
}

func TestUsedMiB(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int
		wantErr  bool
	}{
		{name: "meminfo", input: meminfo4GB, expected: 1920},
		{name: "no memavailable", input: "MemTotal: 3998720 kB\n", wantErr: true},
		{name: "garbage lines are skipped", input: "junk\nMemTotal: 2048 kB\nMemAvailable: x kB\nMemAvailable: 1024 kB\n", expected: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := usedMiB(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.expected {
				t.Errorf("usedMiB = %d, want %d", got, tt.expected)
			}
		})
	}
}

func TestRequested(t *testing.T) {
	tests := []struct {
		name     string
		fake     *toolchain.Fake
		expected int
		wantErr  bool
	}{
		{
			name:     "sums every container's memory request",
			fake:     toolchain.NewFake().WhenArgsContain("get pods", "512Mi\n1Gi\n\n256Mi\n", 0),
			expected: 1792,
		},
		{
			name: "no pods ask for nothing",
			fake: toolchain.NewFake().WhenArgsContain("get pods", "", 0),
		},
		{
			name:    "a cluster that does not answer is an error",
			fake:    toolchain.NewFake().WhenArgsContain("get pods", "", 1),
			wantErr: true,
		},
		{
			name:    "an unreadable request is an error",
			fake:    toolchain.NewFake().WhenArgsContain("get pods", "lots\n", 0),
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Requested(t.Context(), tt.fake, "k3d-lab")
			if (err != nil) != tt.wantErr {
				t.Fatalf("Requested error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.expected {
				t.Errorf("Requested = %d, want %d", got, tt.expected)
			}
		})
	}

	t.Run("asks the given context for pods that have not finished", func(t *testing.T) {
		f := toolchain.NewFake()
		if _, err := Requested(t.Context(), f, "k3d-lab"); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"--context k3d-lab", "status.phase!=Succeeded,status.phase!=Failed"} {
			if !f.Called(want) {
				t.Errorf("kubectl should be called with %q, calls: %v", want, f.CallStrings())
			}
		}
	})

	t.Run("missing kubectl is an error", func(t *testing.T) {
		f := toolchain.NewFake()
		f.LookPathErr = errors.New("not found")
		f.Available = map[string]string{}
		if _, err := Requested(t.Context(), f, "k3d-lab"); err == nil {
			t.Error("want an error")
		}
	})
}

func TestMemoryBytes(t *testing.T) {
	tests := []struct {
		quantity string
		expected float64
		wantErr  bool
	}{
		{quantity: "128Mi", expected: 128 << 20},
		{quantity: "1.5Gi", expected: 1.5 * (1 << 30)},
		{quantity: "64Ki", expected: 64 << 10},
		{quantity: "1G", expected: 1e9},
		{quantity: "500M", expected: 5e8},
		{quantity: "1e9", expected: 1e9},
		{quantity: "1048576", expected: 1 << 20},
		{quantity: "2000m", expected: 2},
		{quantity: "Mi", wantErr: true},
		{quantity: "ten", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.quantity, func(t *testing.T) {
			got, err := memoryBytes(tt.quantity)
			if (err != nil) != tt.wantErr {
				t.Fatalf("memoryBytes(%q) error = %v, wantErr %v", tt.quantity, err, tt.wantErr)
			}
			if got != tt.expected {
				t.Errorf("memoryBytes(%q) = %v, want %v", tt.quantity, got, tt.expected)
			}
		})
	}
}
