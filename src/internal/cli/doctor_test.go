// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sagar2395/snowopslabs/internal/capacity"
	"github.com/sagar2395/snowopslabs/internal/config"
	"github.com/sagar2395/snowopslabs/internal/toolchain"
)

// fakeEnv builds a Fake where every required tool resolves and reports a
// current version. dockerInfo is the scripted "{{.NCPU}} {{.MemTotal}}" output
// of `docker info` (e.g. "2 2147483648" for 2 CPU / 2 GiB); its rule is added
// first so `docker info` matches it, not the generic `docker` version rule.
func fakeEnv(dockerInfo string) *toolchain.Fake {
	f := toolchain.NewFake()
	f.Available = map[string]string{
		"bash": "/bin/bash", "kubectl": "/usr/bin/kubectl", "helm": "/usr/bin/helm",
		"docker": "/usr/bin/docker", "k3d": "/usr/bin/k3d", "kind": "/usr/bin/kind",
	}
	f.WhenArgsContain("info", dockerInfo+"\n", 0)
	f.WhenArgsContain("/bin/bash", "GNU bash, version 5.2.21(1)-release\n", 0)
	f.WhenArgsContain("kubectl", `{"clientVersion":{"gitVersion":"v1.31.0"}}`+"\n", 0)
	f.WhenArgsContain("helm", "v3.16.0\n", 0)
	f.WhenArgsContain("docker", "27.0.0\n", 0)
	f.WhenArgsContain("k3d", "k3d version v5.8.3\n", 0)
	f.WhenArgsContain("kind", "kind v0.27.0\n", 0)
	return f
}

// These tests check doctor's output text as well as its exit status.

func TestRunDoctor(t *testing.T) {
	ctx := t.Context()

	// A fake where every tool resolves and reports a current version, and the
	// Docker VM is comfortably provisioned. The `docker info` rule is registered
	// first so `docker info` matches it rather than the generic `docker` version
	// rule (first match wins).
	healthy := func() *toolchain.Fake {
		return fakeEnv("16 17179869184") // 16 CPU, 16 GiB - well above the minimum
	}

	t.Run("a healthy environment succeeds", func(t *testing.T) {
		var out bytes.Buffer
		if err := runDoctor(ctx, &out, healthy()); err != nil {
			t.Fatalf("runDoctor: %v\n%s", err, out.String())
		}
		if !strings.Contains(out.String(), "Ready to run SnowOps Labs") {
			t.Errorf("output should confirm success:\n%s", out.String())
		}
		if strings.Contains(out.String(), "Problems to fix") {
			t.Errorf("a healthy environment should report no problems:\n%s", out.String())
		}
	})

	t.Run("lists every tool with its version and requirement", func(t *testing.T) {
		var out bytes.Buffer
		if err := runDoctor(ctx, &out, healthy()); err != nil {
			t.Fatalf("runDoctor: %v", err)
		}
		for _, want := range []string{"TOOL", "STATUS", "VERSION", "REQUIRED", "kubectl", "helm", "k3d"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("output should contain %q:\n%s", want, out.String())
			}
		}
	})

	t.Run("a missing required tool fails with an actionable message", func(t *testing.T) {
		f := healthy()
		delete(f.Available, "helm")

		var out bytes.Buffer
		err := runDoctor(ctx, &out, f)
		if err == nil {
			t.Fatalf("expected a non-zero result:\n%s", out.String())
		}
		text := out.String()
		if !strings.Contains(text, "Problems to fix") {
			t.Errorf("output should have a problems section:\n%s", text)
		}
		// Name the tool, why it matters, and how to fix it. The install hint is
		// platform-specific (Homebrew on macOS, the helm.sh docs on Linux), so
		// pick the expectation to match the host the test is running on.
		installHint := "helm.sh"
		if runtime.GOOS == "darwin" {
			installHint = "brew install helm"
		}
		for _, want := range []string{"helm", "PATH", "installing platform components", installHint} {
			if !strings.Contains(text, want) {
				t.Errorf("output should mention %q:\n%s", want, text)
			}
		}
	})

	t.Run("an outdated tool fails and shows both versions", func(t *testing.T) {
		f := healthy()
		f2 := toolchain.NewFake()
		f2.Available = f.Available
		f2.WhenArgsContain("helm", "v3.9.0\n", 0)
		f2.WhenArgsContain("/bin/bash", "GNU bash, version 5.2.21(1)-release\n", 0)
		f2.WhenArgsContain("kubectl", `{"clientVersion":{"gitVersion":"v1.31.0"}}`+"\n", 0)
		f2.WhenArgsContain("docker", "27.0.0\n", 0)
		f2.WhenArgsContain("k3d", "k3d version v5.8.3\n", 0)
		f2.WhenArgsContain("kind", "kind v0.27.0\n", 0)

		var out bytes.Buffer
		if err := runDoctor(ctx, &out, f2); err == nil {
			t.Fatalf("expected a non-zero result:\n%s", out.String())
		}
		text := out.String()
		if !strings.Contains(text, "OUTDATED") {
			t.Errorf("the table should mark helm outdated:\n%s", text)
		}
		for _, want := range []string{"3.9.0", "3.12.0"} {
			if !strings.Contains(text, want) {
				t.Errorf("output should show version %q:\n%s", want, text)
			}
		}
	})

	t.Run("a missing optional tool warns but succeeds", func(t *testing.T) {
		f := healthy()
		delete(f.Available, "k3d")
		delete(f.Available, "kind")

		var out bytes.Buffer
		if err := runDoctor(ctx, &out, f); err != nil {
			t.Fatalf("optional tools must not fail the check: %v\n%s", err, out.String())
		}
		text := out.String()
		if !strings.Contains(text, "Notes:") {
			t.Errorf("a missing optional tool should appear as a note:\n%s", text)
		}
		if !strings.Contains(text, "optional") {
			t.Errorf("the table should mark it optional:\n%s", text)
		}
	})

	t.Run("reports the count of blocking problems", func(t *testing.T) {
		f := healthy()
		delete(f.Available, "helm")
		delete(f.Available, "kubectl")

		var out bytes.Buffer
		err := runDoctor(ctx, &out, f)
		if err == nil {
			t.Fatal("expected an error")
		}
		if !strings.Contains(err.Error(), "2 problem(s)") {
			t.Errorf("error = %q, want it to count the problems", err)
		}
	})

	t.Run("honours a cancelled context", func(t *testing.T) {
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		var out bytes.Buffer
		if err := runDoctor(cancelled, &out, healthy()); err == nil {
			t.Fatal("expected an error on a cancelled context")
		}
	})

	t.Run("an under-provisioned Docker VM is a blocking problem", func(t *testing.T) {
		var out bytes.Buffer
		// 2 CPU / 2 GiB - the default colima VM, which cannot hold the lab.
		err := runDoctor(ctx, &out, fakeEnv("2 2147483648"))
		if err == nil {
			t.Fatalf("an undersized VM must fail doctor:\n%s", out.String())
		}
		text := out.String()
		for _, want := range []string{"Problems to fix", "Docker has 2 CPU / 2 GB", "needs at least 2 CPU / 4 GB"} {
			if !strings.Contains(text, want) {
				t.Errorf("output should mention %q:\n%s", want, text)
			}
		}
		if strings.Contains(text, "Ready to run") {
			t.Errorf("doctor must not claim readiness after a problem:\n%s", text)
		}
	})

	t.Run("a VM at the minimum reports its size and passes", func(t *testing.T) {
		var out bytes.Buffer
		if err := runDoctor(ctx, &out, fakeEnv("2 4294967296")); err != nil {
			t.Fatalf("runDoctor: %v\n%s", err, out.String())
		}
		if !strings.Contains(out.String(), "2 CPU / 4 GB - meets the minimum") {
			t.Errorf("the Docker line should show the size:\n%s", out.String())
		}
	})
}

func TestDockerCapacity(t *testing.T) {
	fake := func(info string, exit int, dockerContext string) *toolchain.Fake {
		f := toolchain.NewFake()
		f.Available = map[string]string{"docker": "/usr/bin/docker", "colima": "/opt/homebrew/bin/colima"}
		f.WhenArgsContain("context show", dockerContext+"\n", 0)
		f.WhenArgsContain("info", info, exit)
		return f
	}

	tests := []struct {
		name        string
		fake        *toolchain.Fake
		goos        string
		wsl         bool
		wantLine    string
		wantProblem string
		wantNote    string
	}{
		{
			name: "enough resources", fake: fake("4 8307675136", 0, "colima"), goos: "darwin",
			wantLine: "4 CPU / 7.7 GB - meets the minimum",
		},
		{
			name: "undersized colima prints the colima resize", fake: fake("2 2054160384", 0, "colima"), goos: "darwin",
			wantLine: "2 CPU / 1.9 GB", wantProblem: "colima stop && colima start --cpu 2 --memory 4",
		},
		{
			name: "undersized WSL points at .wslconfig", fake: fake("2 2054160384", 0, "default"), goos: "linux", wsl: true,
			wantProblem: "wsl --shutdown",
		},
		{
			name: "stopped colima is a note: init starts it", fake: fake("", 1, "colima"), goos: "darwin",
			wantLine: "not running", wantNote: "labctl init' starts it",
		},
		{
			name: "stopped native daemon is a problem", fake: fake("", 1, "default"), goos: "linux",
			wantLine: "not running", wantProblem: "sudo systemctl start docker",
		},
		{
			name: "unparseable output is a note", fake: fake("lots of ram", 0, "default"), goos: "linux",
			wantLine: "unknown", wantNote: "Could not read",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dockerCapacity(t.Context(), tt.fake, capacity.Platform{GOOS: tt.goos, WSL: tt.wsl})
			line, problem, note := got.line, got.problem, got.note
			check := func(field, got, want string) {
				if want == "" && got != "" {
					t.Errorf("%s = %q, want empty", field, got)
				}
				if want != "" && !strings.Contains(got, want) {
					t.Errorf("%s = %q, want it to contain %q", field, got, want)
				}
			}
			if tt.wantLine != "" {
				check("line", line, tt.wantLine)
			}
			check("problem", problem, tt.wantProblem)
			check("note", note, tt.wantNote)
		})
	}

	t.Run("missing docker is left to the tool checks", func(t *testing.T) {
		f := toolchain.NewFake()
		f.Available = map[string]string{}
		f.LookPathErr = errors.New("not found")
		if got := dockerCapacity(t.Context(), f, capacity.Platform{GOOS: "linux"}); got != (dockerReport{}) {
			t.Errorf("want nothing, got %+v", got)
		}
	})
}

func TestStatusLabel(t *testing.T) {
	tests := []struct {
		name   string
		result toolchain.CheckResult
		want   string
	}{
		{"ok", toolchain.CheckResult{Status: toolchain.CheckOK}, "ok"},
		{"missing required", toolchain.CheckResult{Status: toolchain.CheckMissing}, "MISSING"},
		{"missing optional", toolchain.CheckResult{Status: toolchain.CheckMissing, Optional: true}, "missing (optional)"},
		{"outdated required", toolchain.CheckResult{Status: toolchain.CheckOutdated}, "OUTDATED"},
		{"outdated optional", toolchain.CheckResult{Status: toolchain.CheckOutdated, Optional: true}, "outdated (optional)"},
		{"unknown", toolchain.CheckResult{Status: toolchain.CheckUnknown}, "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := statusLabel(tt.result); got != tt.want {
				t.Errorf("statusLabel() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMemoryRequestsNote(t *testing.T) {
	lab := t.TempDir()
	for _, d := range []string{"scenarios", "runtimes/k3d", "runtimes/incluster"} {
		if err := os.MkdirAll(filepath.Join(lab, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("SNOWOPS_HOME", t.TempDir())
	t.Setenv("CLUSTER_NAME", "")
	engine := capacity.Resources{CPUs: 4, MemBytes: 8 << 30} // 8 GiB

	fake := func(requests string, exit int) *toolchain.Fake {
		f := toolchain.NewFake()
		f.Available = map[string]string{"kubectl": "/usr/bin/kubectl"}
		f.WhenArgsContain("get pods", requests, exit)
		return f
	}

	tests := []struct {
		name     string
		profile  string
		fake     *toolchain.Fake
		wantNote string
	}{
		{
			name: "requests past 85% of Docker's memory warn", profile: "k3d",
			fake:     fake("4Gi\n2560Mi\n512Mi\n", 0),
			wantNote: "ask for 7.0 GB of memory, 87% of Docker's 8.0 GB",
		},
		{name: "requests well within Docker's memory say nothing", profile: "k3d", fake: fake("4Gi\n", 0)},
		{name: "a cluster that does not answer says nothing", profile: "k3d", fake: fake("", 1)},
		{name: "a lab that runs elsewhere says nothing", profile: "incluster", fake: fake("8Gi\n", 0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("PROFILE", tt.profile)
			note := memoryRequestsNote(t.Context(), tt.fake, lab, engine)
			if tt.wantNote == "" && note != "" {
				t.Errorf("note = %q, want none", note)
			}
			if tt.wantNote != "" && !strings.Contains(note, tt.wantNote) {
				t.Errorf("note = %q, want it to contain %q", note, tt.wantNote)
			}
		})
	}

	t.Run("asks the lab's own cluster", func(t *testing.T) {
		t.Setenv("PROFILE", "k3d")
		f := fake("", 0)
		memoryRequestsNote(t.Context(), f, lab, engine)
		if !f.Called("--context k3d-snowops") {
			t.Errorf("kubectl should target the lab's context, calls: %v", f.CallStrings())
		}
	})
}

func TestLabDomain(t *testing.T) {
	lab := t.TempDir()
	for _, d := range []string{"scenarios", "runtimes/k3d"} {
		if err := os.MkdirAll(filepath.Join(lab, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("SNOWOPS_HOME", t.TempDir())
	t.Setenv("DOMAIN_SUFFIX", "")
	t.Setenv("CLUSTER_NAME", "")
	tests := []struct {
		name       string
		env        string
		labErr     error
		wantSuffix string
		wantLocal  bool
	}{
		{name: "the default suffix resolves locally", wantSuffix: "snowops.localhost", wantLocal: true},
		{name: "a .local suffix needs hosts entries", env: "k3d.local", wantSuffix: "k3d.local"},
		{name: "no lab falls back to the default", labErr: config.ErrNoLab, wantSuffix: "snowops.localhost", wantLocal: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DOMAIN_SUFFIX", tt.env)
			suffix, local := labDomain(lab, tt.labErr)
			if suffix != tt.wantSuffix || local != tt.wantLocal {
				t.Errorf("labDomain = %q, %v; want %q, %v", suffix, local, tt.wantSuffix, tt.wantLocal)
			}
		})
	}
}
