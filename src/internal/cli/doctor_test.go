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

// doctor's whole value is the quality of its output, so these assert on what
// the user reads, not just the exit status.

func TestRunDoctor(t *testing.T) {
	ctx := context.Background()

	// A fake where every tool resolves and reports a current version.
	healthy := func() *toolchain.Fake {
		f := toolchain.NewFake()
		f.Available = map[string]string{
			"bash": "/bin/bash", "kubectl": "/usr/bin/kubectl", "helm": "/usr/bin/helm",
			"docker": "/usr/bin/docker", "k3d": "/usr/bin/k3d", "kind": "/usr/bin/kind",
		}
		f.WhenArgsContain("/bin/bash", "GNU bash, version 5.2.21(1)-release\n", 0)
		f.WhenArgsContain("kubectl", `{"clientVersion":{"gitVersion":"v1.31.0"}}`+"\n", 0)
		f.WhenArgsContain("helm", "v3.16.0\n", 0)
		f.WhenArgsContain("docker", "27.0.0\n", 0)
		f.WhenArgsContain("k3d", "k3d version v5.8.3\n", 0)
		f.WhenArgsContain("kind", "kind v0.27.0\n", 0)
		return f
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
