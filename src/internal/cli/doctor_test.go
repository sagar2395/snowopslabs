// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bytes"
	"context"
	"runtime"
	"strings"
	"testing"

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

// doctor's whole value is the quality of its output, so these assert on what
// the user reads, not just the exit status.

func TestRunDoctor(t *testing.T) {
	ctx := context.Background()

	// A fake where every tool resolves and reports a current version, and the
	// Docker VM is comfortably provisioned. The `docker info` rule is registered
	// first so `docker info` matches it rather than the generic `docker` version
	// rule (first match wins).
	healthy := func() *toolchain.Fake {
		return fakeEnv("16 17179869184") // 16 CPU, 16 GiB — well above the minimum
	}

	t.Run("a healthy environment succeeds", func(t *testing.T) {
		var out bytes.Buffer
		if err := runDoctor(ctx, &out, healthy()); err != nil {
			t.Fatalf("runDoctor: %v\n%s", err, out.String())
		}
		if !strings.Contains(out.String(), "Everything SnowOps Labs needs") {
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
		if !strings.Contains(err.Error(), "2 required") {
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

	t.Run("a nil context does not panic", func(t *testing.T) {
		var out bytes.Buffer
		//nolint:staticcheck // deliberately passing nil to prove it is handled
		if err := runDoctor(nil, &out, healthy()); err != nil {
			t.Fatalf("runDoctor: %v", err)
		}
	})

	t.Run("warns (but does not fail) when the Docker VM is under-provisioned", func(t *testing.T) {
		var out bytes.Buffer
		// 2 CPU / 2 GiB — the classic default Docker Desktop / Colima VM.
		if err := runDoctor(ctx, &out, fakeEnv("2 2147483648")); err != nil {
			t.Fatalf("an under-provisioned VM is a warning, not a failure: %v\n%s", err, out.String())
		}
		text := out.String()
		if strings.Contains(text, "Problems to fix") {
			t.Errorf("resources are a note, not a blocking problem:\n%s", text)
		}
		// Name the shortfall and both fixes (README guidance).
		for _, want := range []string{
			"Notes:", "Docker has 2 CPU / 2 GiB", "needs at least 4 CPU / 8 GiB",
			"colima start --cpu 4 --memory 8", "Docker Desktop",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("output should mention %q:\n%s", want, text)
			}
		}
	})

	t.Run("no resource warning when the Docker VM is sufficient", func(t *testing.T) {
		var out bytes.Buffer
		if err := runDoctor(ctx, &out, fakeEnv("4 8589934592")); err != nil { // exactly 4 CPU / 8 GiB
			t.Fatalf("runDoctor: %v", err)
		}
		if strings.Contains(out.String(), "needs at least") {
			t.Errorf("a VM meeting the minimum should not warn:\n%s", out.String())
		}
	})

	t.Run("no resource warning when the Docker daemon is unreachable", func(t *testing.T) {
		f := healthy()
		// `docker info` fails (daemon down) — a different problem, silently skipped.
		f2 := toolchain.NewFake()
		f2.Available = f.Available
		f2.WhenArgsContain("info", "", 1)
		f2.WhenArgsContain("/bin/bash", "GNU bash, version 5.2.21(1)-release\n", 0)
		f2.WhenArgsContain("kubectl", `{"clientVersion":{"gitVersion":"v1.31.0"}}`+"\n", 0)
		f2.WhenArgsContain("helm", "v3.16.0\n", 0)
		f2.WhenArgsContain("docker", "27.0.0\n", 0)
		f2.WhenArgsContain("k3d", "k3d version v5.8.3\n", 0)
		f2.WhenArgsContain("kind", "kind v0.27.0\n", 0)

		var out bytes.Buffer
		if err := runDoctor(ctx, &out, f2); err != nil {
			t.Fatalf("runDoctor: %v", err)
		}
		if strings.Contains(out.String(), "needs at least") {
			t.Errorf("a stopped daemon should not produce a resource warning:\n%s", out.String())
		}
	})
}

func TestDockerResourceWarning(t *testing.T) {
	ctx := context.Background()

	newFake := func(dockerInfo string, exit int) *toolchain.Fake {
		f := toolchain.NewFake()
		f.Available = map[string]string{"docker": "/usr/bin/docker"}
		f.WhenArgsContain("info", dockerInfo, exit)
		return f
	}

	t.Run("warns below the CPU minimum", func(t *testing.T) {
		if got := dockerResourceWarning(ctx, newFake("2 17179869184", 0)); !strings.Contains(got, "2 CPU") {
			t.Errorf("expected a CPU warning, got %q", got)
		}
	})

	t.Run("warns below the memory minimum", func(t *testing.T) {
		got := dockerResourceWarning(ctx, newFake("8 2147483648", 0))
		if !strings.Contains(got, "8 CPU / 2 GiB") {
			t.Errorf("expected a memory warning naming the detected values, got %q", got)
		}
	})

	t.Run("silent exactly at the minimum", func(t *testing.T) {
		if got := dockerResourceWarning(ctx, newFake("4 8589934592", 0)); got != "" {
			t.Errorf("4 CPU / 8 GiB meets the minimum; want no warning, got %q", got)
		}
	})

	t.Run("silent when docker is not installed", func(t *testing.T) {
		f := toolchain.NewFake()
		f.Available = map[string]string{} // docker absent -> LookPath fails
		if got := dockerResourceWarning(ctx, f); got != "" {
			t.Errorf("missing docker is reported elsewhere; want no warning, got %q", got)
		}
	})

	t.Run("silent when the daemon is unreachable", func(t *testing.T) {
		if got := dockerResourceWarning(ctx, newFake("", 1)); got != "" {
			t.Errorf("a stopped daemon should be skipped, got %q", got)
		}
	})

	t.Run("silent on unparseable output", func(t *testing.T) {
		for _, bad := range []string{"lots of ram", "8", "0 0", "-1 8589934592"} {
			if got := dockerResourceWarning(ctx, newFake(bad, 0)); got != "" {
				t.Errorf("output %q is unparseable; want no warning, got %q", bad, got)
			}
		}
	})

	t.Run("nil context does not panic", func(t *testing.T) {
		//nolint:staticcheck // deliberately passing nil to prove it is handled
		_ = dockerResourceWarning(nil, newFake("16 17179869184", 0))
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
