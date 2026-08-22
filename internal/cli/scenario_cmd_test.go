// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"bytes"
	"strings"
	"testing"
)

// executeHelp runs the cobra command tree with the given args and captures
// stdout/stderr output. Because --help causes cobra to exit 0 without running
// PersistentPreRunE, these tests are fully hermetic.
func executeHelp(t *testing.T, args ...string) string {
	t.Helper()
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs(args)
	// cobra's Execute returns nil for --help
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(%v): %v", args, err)
	}
	return buf.String()
}

func TestScenarioCmd_Help(t *testing.T) {
	out := executeHelp(t, "scenario", "--help")
	if out == "" {
		t.Error("expected non-empty --help output for 'scenario'")
	}
	if !strings.Contains(out, "scenario") {
		t.Errorf("help output should mention 'scenario': %q", out)
	}
}

func TestIncidentCmd_Help(t *testing.T) {
	out := executeHelp(t, "incident", "--help")
	if out == "" {
		t.Error("expected non-empty --help output for 'incident'")
	}
	if !strings.Contains(strings.ToLower(out), "incident") {
		t.Errorf("help output should mention 'incident': %q", out)
	}
}

func TestLearnCmd_Help(t *testing.T) {
	out := executeHelp(t, "learn", "--help")
	if out == "" {
		t.Error("expected non-empty --help output for 'learn'")
	}
	if !strings.Contains(strings.ToLower(out), "learn") {
		t.Errorf("help output should mention 'learn': %q", out)
	}
}

func TestPlatformCmd_Help(t *testing.T) {
	out := executeHelp(t, "platform", "--help")
	if out == "" {
		t.Error("expected non-empty --help output for 'platform'")
	}
	if !strings.Contains(strings.ToLower(out), "platform") {
		t.Errorf("help output should mention 'platform': %q", out)
	}
}

func TestEnvCmd_Help(t *testing.T) {
	out := executeHelp(t, "env", "--help")
	if out == "" {
		t.Error("expected non-empty --help output for 'env'")
	}
	if !strings.Contains(strings.ToLower(out), "env") {
		t.Errorf("help output should mention 'env': %q", out)
	}
}

func TestEnvPromoteCmd_Help(t *testing.T) {
	out := executeHelp(t, "env", "promote", "--help")
	if out == "" {
		t.Error("expected non-empty --help output for 'env promote'")
	}
	for _, want := range []string{"promote", "from-env", "to-env"} {
		if !strings.Contains(strings.ToLower(out), want) {
			t.Errorf("help output should mention %q: %q", want, out)
		}
	}
}

func TestEnvListCmd_Help(t *testing.T) {
	out := executeHelp(t, "env", "list", "--help")
	if out == "" {
		t.Error("expected non-empty --help output for 'env list'")
	}
}

func TestUsersCmd_Help(t *testing.T) {
	out := executeHelp(t, "users", "--help")
	if out == "" {
		t.Error("expected non-empty --help output for 'users'")
	}
	for _, want := range []string{"operator", "participant"} {
		if !strings.Contains(strings.ToLower(out), want) {
			t.Errorf("help output should mention %q: %q", want, out)
		}
	}
}

func TestUsersSubcommands_Help(t *testing.T) {
	for _, sub := range []string{"add", "list", "remove"} {
		out := executeHelp(t, "users", sub, "--help")
		if out == "" {
			t.Errorf("expected non-empty --help output for 'users %s'", sub)
		}
	}
}
