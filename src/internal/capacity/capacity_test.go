// SPDX-License-Identifier: Apache-2.0

package capacity

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/snowops-oss/snowopslabs/internal/toolchain"
)

func TestResources_Meets(t *testing.T) {
	tests := []struct {
		name     string
		have     Resources
		need     Need
		expected bool
	}{
		{name: "exactly the floor", have: Resources{CPUs: 2, MemBytes: 4 * gib}, need: Floor, expected: true},
		{
			name:     "8 gb colima vm reports 7.7 gib",
			have:     Resources{CPUs: 4, MemBytes: 8307675136},
			need:     Need{CPUs: 4, MemGiB: 8},
			expected: true,
		},
		{name: "default 2 gb colima vm", have: Resources{CPUs: 2, MemBytes: 2054160384}, need: Floor, expected: false},
		{name: "too few cpus", have: Resources{CPUs: 1, MemBytes: 16 * gib}, need: Floor, expected: false},
		{name: "just under the tolerance", have: Resources{CPUs: 2, MemBytes: 3972844748}, need: Floor, expected: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.have.Meets(tt.need); got != tt.expected {
				t.Errorf("Meets = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestProbe(t *testing.T) {
	tests := []struct {
		name     string
		fake     func() *toolchain.Fake
		expected Resources
		wantErr  error
	}{
		{
			name: "reads cpu and memory",
			fake: func() *toolchain.Fake {
				return toolchain.NewFake().WhenArgsContain("info", "4 8307675136\n", 0)
			},
			expected: Resources{CPUs: 4, MemBytes: 8307675136},
		},
		{
			name: "docker not installed",
			fake: func() *toolchain.Fake {
				f := toolchain.NewFake()
				f.Available = map[string]string{"kubectl": "/usr/bin/kubectl"}
				f.LookPathErr = errors.New("not found")
				return f
			},
			wantErr: ErrDockerMissing,
		},
		{
			name: "user not in the docker group",
			fake: func() *toolchain.Fake {
				return toolchain.NewFake().WhenArgsContainStderr("info", "",
					"permission denied while trying to connect to the Docker daemon socket", 1)
			},
			wantErr: ErrNoPermission,
		},
		{
			name: "docker desktop shim without wsl integration",
			fake: func() *toolchain.Fake {
				return toolchain.NewFake().WhenArgsContainStderr("info", "",
					"The command 'docker' could not be found in this WSL 2 distro.", 1)
			},
			wantErr: ErrWSLIntegration,
		},
		{
			name: "daemon down",
			fake: func() *toolchain.Fake {
				return toolchain.NewFake().WhenArgsContain("info", "", 1)
			},
			wantErr: ErrDaemonDown,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Probe(t.Context(), tt.fake())
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.expected {
				t.Errorf("got %+v, want %+v", got, tt.expected)
			}
		})
	}

	t.Run("keeps the underlying cause in the chain", func(t *testing.T) {
		cause := errors.New("exec: docker: input/output error")
		f := toolchain.NewFake().WhenArgsContainError("info", cause)
		_, err := Probe(t.Context(), f)
		if !errors.Is(err, ErrDaemonDown) || !errors.Is(err, cause) {
			t.Fatalf("want both the sentinel and the cause in %v", err)
		}
	})

	t.Run("a cancelled context fails instead of hanging", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		f := toolchain.NewFake().WhenArgsContainBlock("info", time.Minute)
		if _, err := Probe(ctx, f); err == nil {
			t.Fatal("expected an error on a cancelled context")
		}
	})
}

func TestParseInfo(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "free text", input: "lots of text\n"},
		{name: "one field", input: "8"},
		{name: "non-numeric cpus", input: "x 8589934592"},
		{name: "non-numeric memory", input: "4 lots"},
		{name: "zero values", input: "0 0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseInfo(tt.input); err == nil {
				t.Errorf("parseInfo(%q) should fail", tt.input)
			}
		})
	}
}

func TestDetectHost(t *testing.T) {
	darwin := Platform{GOOS: "darwin"}
	withContext := func(name string) func() *toolchain.Fake {
		return func() *toolchain.Fake {
			return toolchain.NewFake().WhenArgsContain("context show", name+"\n", 0)
		}
	}
	defaultContext := func(available map[string]string) func() *toolchain.Fake {
		return func() *toolchain.Fake {
			f := toolchain.NewFake().WhenArgsContain("context show", "default\n", 0)
			f.Available = available
			f.LookPathErr = errors.New("not found")
			return f
		}
	}
	tests := []struct {
		name     string
		platform Platform
		fake     func() *toolchain.Fake
		expected Engine
	}{
		{name: "wsl wins", platform: Platform{GOOS: "linux", WSL: true}, fake: toolchain.NewFake, expected: EngineWSL},
		{name: "native linux", platform: Platform{GOOS: "linux"}, fake: toolchain.NewFake, expected: EngineNative},
		{name: "colima context", platform: darwin, fake: withContext("colima"), expected: EngineColima},
		{name: "named colima profile", platform: darwin, fake: withContext("colima-work"), expected: EngineColima},
		{name: "docker desktop context", platform: darwin, fake: withContext("desktop-linux"), expected: EngineDockerDesktop},
		{
			name:     "colima installed but never started",
			platform: darwin,
			fake: defaultContext(map[string]string{
				"docker": "/opt/homebrew/bin/docker",
				"colima": "/opt/homebrew/bin/colima",
			}),
			expected: EngineColima,
		},
		{
			name:     "default context and no colima",
			platform: darwin,
			fake:     defaultContext(map[string]string{"docker": "/usr/local/bin/docker"}),
			expected: EngineDockerDesktop,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DetectHost(t.Context(), tt.fake(), tt.platform).Engine; got != tt.expected {
				t.Errorf("engine = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestHost_StartHint(t *testing.T) {
	n := Need{CPUs: 4, MemGiB: 6}
	tests := []struct {
		name     string
		engine   Engine
		expected string
	}{
		{name: "colima", engine: EngineColima, expected: "colima start --cpu 4 --memory 6"},
		{name: "docker desktop", engine: EngineDockerDesktop, expected: "open -a Docker"},
		{name: "wsl", engine: EngineWSL, expected: "sudo service docker start"},
		{name: "native", engine: EngineNative, expected: "sudo systemctl start docker"},
		{name: "unknown falls back to native", engine: EngineUnknown, expected: "sudo systemctl start docker"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (Host{Engine: tt.engine}).StartHint(n); !strings.Contains(got, tt.expected) {
				t.Errorf("StartHint = %q, want it to contain %q", got, tt.expected)
			}
		})
	}
}

func TestHost_AccessHint(t *testing.T) {
	tests := []struct {
		name     string
		engine   Engine
		err      error
		expected string
	}{
		{name: "wsl integration off", engine: EngineWSL, err: ErrWSLIntegration, expected: "WSL Integration"},
		{name: "wsl permission", engine: EngineWSL, err: ErrNoPermission, expected: "wsl --shutdown"},
		{name: "linux permission", engine: EngineNative, err: ErrNoPermission, expected: "newgrp docker"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (Host{Engine: tt.engine}).AccessHint(tt.err); !strings.Contains(got, tt.expected) {
				t.Errorf("AccessHint = %q, want it to contain %q", got, tt.expected)
			}
		})
	}
}

func TestHost_ResizeHint(t *testing.T) {
	n := Need{CPUs: 4, MemGiB: 6}
	tests := []struct {
		name     string
		engine   Engine
		expected string
	}{
		{name: "colima", engine: EngineColima, expected: "colima stop && colima start --cpu 4 --memory 6"},
		{name: "docker desktop", engine: EngineDockerDesktop, expected: "Settings → Resources → CPUs 4, Memory 6 GB"},
		{name: "wsl", engine: EngineWSL, expected: "processors=4 and memory=6GB"},
		{name: "native", engine: EngineNative, expected: "needs 4 CPUs and 6 GB free"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (Host{Engine: tt.engine}).ResizeHint(n); !strings.Contains(got, tt.expected) {
				t.Errorf("ResizeHint = %q, want it to contain %q", got, tt.expected)
			}
		})
	}
}

func TestShortfall(t *testing.T) {
	tests := []struct {
		name     string
		have     Resources
		expected string
	}{
		{
			name:     "short on memory",
			have:     Resources{CPUs: 2, MemBytes: 2054160384},
			expected: "Docker has 2 CPU / 1.9 GB; this needs at least 2 CPU / 4 GB.",
		},
		{name: "enough", have: Resources{CPUs: 4, MemBytes: 16 * gib}, expected: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Shortfall(tt.have, Floor); got != tt.expected {
				t.Errorf("Shortfall = %q, want %q", got, tt.expected)
			}
		})
	}
}
