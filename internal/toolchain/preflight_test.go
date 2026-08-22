// SPDX-License-Identifier: Apache-2.0

package toolchain

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The point of preflight is that a broken environment produces a sentence the
// user can act on. These tests assert on the *content* of those messages, not
// just on status codes, because a correct status with a useless message is
// still a bad experience.

func TestPreflightCheck(t *testing.T) {
	ctx := context.Background()

	req := Requirement{
		Binary:      "helm",
		MinVersion:  "3.12.0",
		VersionArgs: []string{"version", "--short"},
		Why:         "installing platform components",
		InstallHint: func(goos string) string {
			if goos == "darwin" {
				return "brew install helm"
			}
			return "https://helm.sh/docs/intro/install/"
		},
	}

	t.Run("reports a satisfied requirement", func(t *testing.T) {
		f := NewFake().WhenArgsContain("version", "v3.14.2+g0e6a2a\n", 0)
		f.Available = map[string]string{"helm": "/usr/local/bin/helm"}

		p := &Preflight{Runner: f, GOOS: "linux", Requirements: []Requirement{req}}
		got, err := p.Check(ctx)
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("got %d results, want 1", len(got))
		}
		if got[0].Status != CheckOK {
			t.Errorf("status = %q, want ok (detail: %s)", got[0].Status, got[0].Detail)
		}
		if got[0].Version != "3.14.2" {
			t.Errorf("version = %q, want 3.14.2", got[0].Version)
		}
		if got[0].Path != "/usr/local/bin/helm" {
			t.Errorf("path = %q", got[0].Path)
		}
		if !got[0].OK() {
			t.Error("OK() = false for a satisfied requirement")
		}
	})

	t.Run("a missing binary names the tool, the reason and the fix", func(t *testing.T) {
		f := NewFake()
		f.Available = map[string]string{} // nothing resolves

		p := &Preflight{Runner: f, GOOS: "darwin", Requirements: []Requirement{req}}
		got, _ := p.Check(ctx)

		if got[0].Status != CheckMissing {
			t.Fatalf("status = %q, want missing", got[0].Status)
		}
		for _, want := range []string{"helm", "PATH", "installing platform components", "brew install helm"} {
			if !strings.Contains(got[0].Detail, want) {
				t.Errorf("detail %q should mention %q", got[0].Detail, want)
			}
		}
		if got[0].OK() {
			t.Error("OK() = true for a missing required tool")
		}
	})

	t.Run("an outdated binary reports both versions and the fix", func(t *testing.T) {
		f := NewFake().WhenArgsContain("version", "v3.9.0\n", 0)
		f.Available = map[string]string{"helm": "/usr/local/bin/helm"}

		p := &Preflight{Runner: f, GOOS: "linux", Requirements: []Requirement{req}}
		got, _ := p.Check(ctx)

		if got[0].Status != CheckOutdated {
			t.Fatalf("status = %q, want outdated", got[0].Status)
		}
		for _, want := range []string{"3.9.0", "3.12.0", "helm.sh"} {
			if !strings.Contains(got[0].Detail, want) {
				t.Errorf("detail %q should mention %q", got[0].Detail, want)
			}
		}
		if got[0].OK() {
			t.Error("OK() = true for an outdated required tool")
		}
	})

	t.Run("gives the platform-appropriate hint", func(t *testing.T) {
		for goos, want := range map[string]string{"darwin": "brew", "linux": "helm.sh"} {
			t.Run(goos, func(t *testing.T) {
				f := NewFake()
				f.Available = map[string]string{}
				p := &Preflight{Runner: f, GOOS: goos, Requirements: []Requirement{req}}
				got, _ := p.Check(ctx)
				if !strings.Contains(got[0].Detail, want) {
					t.Errorf("%s hint = %q, want it to mention %q", goos, got[0].Detail, want)
				}
			})
		}
	})

	t.Run("an unparseable version is unknown, not a failure", func(t *testing.T) {
		// Our inability to parse a version is our problem, not the user's;
		// blocking their work over it would be worse than proceeding.
		f := NewFake().WhenArgsContain("version", "some future format\n", 0)
		f.Available = map[string]string{"helm": "/usr/local/bin/helm"}

		p := &Preflight{Runner: f, GOOS: "linux", Requirements: []Requirement{req}}
		got, _ := p.Check(ctx)

		if got[0].Status != CheckUnknown {
			t.Fatalf("status = %q, want unknown", got[0].Status)
		}
		if !got[0].OK() {
			t.Error("OK() = false; an unknown version must not block work")
		}
		if !strings.Contains(got[0].Detail, "continuing") {
			t.Errorf("detail should say we are continuing anyway: %q", got[0].Detail)
		}
	})

	t.Run("parses a version even when the tool exits non-zero", func(t *testing.T) {
		// docker version exits non-zero when the daemon is down but still
		// prints the client version.
		f := NewFake().WhenArgsContainStderr("version", "v3.14.0\n", "daemon unreachable", 1)
		f.Available = map[string]string{"helm": "/usr/local/bin/helm"}

		p := &Preflight{Runner: f, GOOS: "linux", Requirements: []Requirement{req}}
		got, _ := p.Check(ctx)

		if got[0].Version != "3.14.0" {
			t.Errorf("version = %q, want 3.14.0 parsed despite the non-zero exit", got[0].Version)
		}
		if got[0].Status != CheckOK {
			t.Errorf("status = %q, want ok", got[0].Status)
		}
	})

	t.Run("a missing optional tool does not block", func(t *testing.T) {
		optional := req
		optional.Optional = true
		optional.Binary = "k3d"

		f := NewFake()
		f.Available = map[string]string{}
		p := &Preflight{Runner: f, GOOS: "linux", Requirements: []Requirement{optional}}
		got, _ := p.Check(ctx)

		if got[0].Status != CheckMissing {
			t.Fatalf("status = %q, want missing", got[0].Status)
		}
		if !got[0].OK() {
			t.Error("OK() = false; a missing optional tool must not block")
		}
	})

	t.Run("no MinVersion means any version passes", func(t *testing.T) {
		anyVersion := req
		anyVersion.MinVersion = ""

		f := NewFake().WhenArgsContain("version", "v0.0.1\n", 0)
		f.Available = map[string]string{"helm": "/usr/local/bin/helm"}
		p := &Preflight{Runner: f, GOOS: "linux", Requirements: []Requirement{anyVersion}}
		got, _ := p.Check(ctx)

		if got[0].Status != CheckOK {
			t.Errorf("status = %q, want ok", got[0].Status)
		}
	})

	t.Run("checks every requirement in order", func(t *testing.T) {
		f := NewFake()
		f.Available = map[string]string{}
		p := &Preflight{
			Runner: f, GOOS: "linux",
			Requirements: []Requirement{
				{Binary: "first", Why: "a"},
				{Binary: "second", Why: "b"},
				{Binary: "third", Why: "c"},
			},
		}
		got, err := p.Check(ctx)
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		want := []string{"first", "second", "third"}
		for i, w := range want {
			if got[i].Binary != w {
				t.Errorf("result %d = %q, want %q", i, got[i].Binary, w)
			}
		}
	})

	t.Run("honours a cancelled context", func(t *testing.T) {
		f := NewFake()
		p := &Preflight{Runner: f, GOOS: "linux", Requirements: Requirements()}
		cancelled, cancel := context.WithCancel(ctx)
		cancel()

		_, err := p.Check(cancelled)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	})

	t.Run("defaults GOOS and Requirements when unset", func(t *testing.T) {
		p := NewPreflight(NewFake())
		if p.GOOS == "" {
			t.Error("NewPreflight left GOOS empty")
		}
		if len(p.Requirements) == 0 {
			t.Error("NewPreflight left Requirements empty")
		}

		bare := &Preflight{Runner: NewFake()}
		got, err := bare.Check(ctx)
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if len(got) == 0 {
			t.Error("a bare Preflight should fall back to the default requirements")
		}
	})
}

func TestCheckResultOK(t *testing.T) {
	tests := []struct {
		name   string
		result CheckResult
		want   bool
	}{
		{"ok", CheckResult{Status: CheckOK}, true},
		{"unknown does not block", CheckResult{Status: CheckUnknown}, true},
		{"missing required blocks", CheckResult{Status: CheckMissing}, false},
		{"outdated required blocks", CheckResult{Status: CheckOutdated}, false},
		{"missing optional does not block", CheckResult{Status: CheckMissing, Optional: true}, true},
		{"outdated optional does not block", CheckResult{Status: CheckOutdated, Optional: true}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.result.OK(); got != tt.want {
				t.Errorf("OK() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestRequirements pins the shipped requirement table: every entry must be
// usable as an error message, or doctor's output degrades quietly over time.
func TestRequirements(t *testing.T) {
	reqs := Requirements()
	if len(reqs) == 0 {
		t.Fatal("no requirements defined")
	}

	seen := map[string]bool{}
	for _, r := range reqs {
		t.Run(r.Binary, func(t *testing.T) {
			if r.Binary == "" {
				t.Error("binary name is empty")
			}
			if seen[r.Binary] {
				t.Errorf("%q is listed twice", r.Binary)
			}
			seen[r.Binary] = true

			if r.Why == "" {
				t.Error("Why is empty — the user needs to know what breaks without it")
			}
			if r.InstallHint == nil {
				t.Error("InstallHint is nil — 'install it' is not actionable")
				return
			}
			for _, goos := range []string{"darwin", "linux"} {
				if hint := r.InstallHint(goos); hint == "" {
					t.Errorf("InstallHint(%s) is empty", goos)
				}
			}
			if r.MinVersion != "" {
				if _, err := splitVersion(r.MinVersion); err != nil {
					t.Errorf("MinVersion %q is not parseable: %v", r.MinVersion, err)
				}
			}
		})
	}

	t.Run("cloud tools are absent", func(t *testing.T) {
		// ADR-0001 cut cloud runtimes; requiring their CLIs would contradict it.
		for _, r := range reqs {
			switch r.Binary {
			case "az", "aws", "gcloud", "terraform":
				t.Errorf("%q should not be a requirement after ADR-0001", r.Binary)
			}
		}
	})

	t.Run("bash minimum accommodates macOS", func(t *testing.T) {
		// macOS ships bash 3.2; requiring 4+ would break the golden path.
		for _, r := range reqs {
			if r.Binary != "bash" {
				continue
			}
			cmp, err := compareVersions("3.2.57", r.MinVersion)
			if err != nil {
				t.Fatalf("compareVersions: %v", err)
			}
			if cmp < 0 {
				t.Errorf("bash MinVersion %q excludes the bash macOS ships", r.MinVersion)
			}
		}
	})
}
