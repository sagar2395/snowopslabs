// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sagar2395/snowopslabs/internal/labtest"
)

// TestMainStaysTrivial guards ADR-0005's entrypoint rule.
//
// The coverage gate does not measure cmd/labctl, so main must contain no
// logic: it only calls internal/cli, where code is tested.
func TestMainStaysTrivial(t *testing.T) {
	path := filepath.Join("..", "..", "cmd", "labctl", "main.go")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}

	var mainFn *ast.FuncDecl
	var otherDecls []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			// No declarations except `var version`, which the linker sets
			// with -X main.version and so must be in package main.
			if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok != token.IMPORT {
				if !isVersionVar(gen) {
					otherDecls = append(otherDecls, gen.Tok.String())
				}
			}
			continue
		}
		if fn.Name.Name == "main" {
			mainFn = fn
			continue
		}
		otherDecls = append(otherDecls, "func "+fn.Name.Name)
	}

	if mainFn == nil {
		t.Fatal("cmd/labctl/main.go has no main function")
	}

	if len(otherDecls) > 0 {
		t.Errorf("cmd/labctl/main.go declares %s - move it into internal/cli, "+
			"where the coverage gate applies (ADR-0005)", strings.Join(otherDecls, ", "))
	}

	// main must be a single call into internal/cli.
	if n := len(mainFn.Body.List); n > 1 {
		t.Errorf("main() has %d statements, want 1 - cmd/labctl is an entrypoint only (ADR-0005). "+
			"Move the logic into internal/cli so it can be tested.", n)
	}
}

// isVersionVar reports whether a declaration is exactly `var version = …`, the
// single build-stamped variable cmd/labctl is permitted to hold.
func isVersionVar(gen *ast.GenDecl) bool {
	if gen.Tok != token.VAR || len(gen.Specs) != 1 {
		return false
	}
	vs, ok := gen.Specs[0].(*ast.ValueSpec)
	if !ok || len(vs.Names) != 1 {
		return false
	}
	return vs.Names[0].Name == "version"
}

// TestPersistentPreRunSkipsEnvironmentDependentCommands checks that commands
// which must work in a broken environment, such as doctor, skip the setup in
// PersistentPreRunE.
func TestPersistentPreRunSkipsEnvironmentDependentCommands(t *testing.T) {
	path := "root.go"
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	for _, name := range []string{"doctor", "runs"} {
		if !strings.Contains(string(src), `"`+name+`"`) {
			t.Errorf("%q is not exempted from PersistentPreRunE; it must run without a loaded config", name)
		}
	}
}

func TestWithUserBin(t *testing.T) {
	tests := []struct{ path, want string }{
		{"/usr/bin:/bin", "/h/.local/bin:/usr/bin:/bin"},
		{"/h/.local/bin:/usr/bin", "/h/.local/bin:/usr/bin"},
		{"/usr/bin:/h/.local/bin", "/usr/bin:/h/.local/bin"},
		{"", "/h/.local/bin"},
	}
	for _, tt := range tests {
		if got := withUserBin(tt.path, "/h/.local/bin"); got != tt.want {
			t.Errorf("withUserBin(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestVersionSkew(t *testing.T) {
	tests := []struct {
		name    string
		binary  string
		content string
		warns   bool
	}{
		{name: "same release", binary: "1.5.0", content: "1.5.0", warns: false},
		{name: "v prefix on one side", binary: "v1.5.0", content: "1.5.0", warns: false},
		{name: "different releases", binary: "1.5.0", content: "1.4.0", warns: true},
		{name: "a lab without lab_version", binary: "1.5.0", content: "", warns: false},
		{name: "development content with a release binary", binary: "1.5.0", content: "1.6.0-dev", warns: true},
		{name: "a development build", binary: "dev", content: "1.4.0", warns: false},
		{name: "a build between releases", binary: "1.4.0-4-ge74f3d4-dirty", content: "1.4.0", warns: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := versionSkew(tt.binary, tt.content, "/lab"); (got != "") != tt.warns {
				t.Errorf("versionSkew(%q, %q) = %q, want warning %v", tt.binary, tt.content, got, tt.warns)
			}
		})
	}
}

// A mistake in how a command was typed shows that command's usage; a command
// that was typed correctly and then failed does not bury its error under it.
func TestRoot_UsageIsShownOnlyForUsageMistakes(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantUsage string // "" means no usage text at all
		wantErr   string
	}{
		{"a missing argument", []string{"scenario", "verify"}, "labctl scenario verify [scenario-name]", "accepts 1 arg(s), received 0"},
		{"too many arguments", []string{"incident", "inject", "a", "b"}, "labctl incident inject [fault-name]", "accepts at most 1 arg(s), received 2"},
		{"an unknown flag", []string{"scenario", "up", "demo-scenario", "--bogus"}, "labctl scenario up [scenario-name]", "unknown flag: --bogus"},
		{"an unknown subcommand argument", []string{"challenge", "info"}, "labctl challenge info <name>", "accepts 1 arg(s), received 0"},
		{"a correct command that fails", []string{"scenario", "up", "ghost"}, "", `scenario "ghost" not found`},
		{"a correct command that fails before its engines load", []string{"runs", "logs", "ghost"}, "", "ghost"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newTerminal(t).run(tt.args...)
			if r.exitCode() != 1 {
				t.Fatalf("exit code = %d, want 1\nstdout:\n%s", r.exitCode(), r.stdout)
			}
			if !strings.Contains(r.stderr, tt.wantErr) {
				t.Errorf("stderr = %q, want it to say %q", r.stderr, tt.wantErr)
			}
			shown := strings.Contains(r.stderr, "Usage:")
			if tt.wantUsage == "" {
				if shown {
					t.Errorf("a runtime failure printed the usage text:\n%s", r.stderr)
				}
				return
			}
			if !shown || !strings.Contains(r.stderr, tt.wantUsage) {
				t.Errorf("stderr does not show the usage %q:\n%s", tt.wantUsage, r.stderr)
			}
		})
	}
}

func TestRoot_MigratesLegacyStateWithReadablePaths(t *testing.T) {
	l := labtest.New(t)
	l.Write(t, ".labctl/marker", "legacy")
	t.Chdir(l.Root)
	t.Setenv("SNOWOPS_HOME", filepath.Join(os.Getenv("HOME"), ".snowops"))

	var stdout, stderr bytes.Buffer
	oldOut, oldErr, oldArgs := rootCmd.OutOrStdout(), rootCmd.ErrOrStderr(), rootCmd.Flag("project-dir").Value.String()
	t.Cleanup(func() {
		rootCmd.SetOut(oldOut)
		rootCmd.SetErr(oldErr)
		_ = rootCmd.Flag("project-dir").Value.Set(oldArgs)
	})
	resetCommands(context.Background(), rootCmd)
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"--project-dir", l.Root, "scenario", "list"})
	if err := rootCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("scenario list: %v\nstderr:\n%s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "Moved the lab state from .labctl to ") {
		t.Fatalf("migration warning missing:\n%s", stderr.String())
	}
	if strings.Contains(stderr.String(), l.Root) || strings.Contains(stderr.String(), l.Home) {
		t.Errorf("migration warning leaked an account-specific path:\n%s", stderr.String())
	}
}

// Every golden transcript belongs to a test. One left behind by a renamed or
// deleted test would be reviewed and updated for nothing.
func TestGoldenTranscripts_EachBelongsToATest(t *testing.T) {
	sources, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	var code strings.Builder
	for _, src := range sources {
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		code.Write(data)
	}
	goldens, err := filepath.Glob(filepath.Join("testdata", "golden", "*.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(goldens) == 0 {
		t.Fatal("no golden transcripts found under testdata/golden")
	}
	for _, g := range goldens {
		name := strings.TrimSuffix(filepath.Base(g), ".txt")
		if !strings.Contains(code.String(), `"`+name+`"`) {
			t.Errorf("%s is not written by any test: delete it, or restore the test that owns it", g)
		}
	}
}

// --verbose reaches scripts as LABCTL_VERBOSE=1, both through the run engine
// and through the web UI's executor, so scripts keep the output they trim by
// default (platform/_lib/helm.sh).
func TestVerbose_ReachesScripts(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"scenario", "up", "drill"}, ""},
		{[]string{"scenario", "up", "drill", "--verbose"}, "1"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			t.Setenv("LABCTL_VERBOSE", "")
			l := newTerminal(t)
			writeDrill(t, l)
			l.Write(t, "scenarios/drill/setup.sh", "#!/usr/bin/env bash\necho \"${LABCTL_VERBOSE:-}\" >\"$(dirname \"$0\")/VERBOSE\"\n")
			l.mustRun(tc.args...)

			data, err := os.ReadFile(filepath.Join(l.Root, "scenarios/drill/VERBOSE"))
			if err != nil {
				t.Fatalf("the setup script did not run: %v", err)
			}
			if got := strings.TrimSpace(string(data)); got != tc.want {
				t.Errorf("setup script saw LABCTL_VERBOSE=%q, want %q", got, tc.want)
			}
			if got := scriptExec.GetEnv("LABCTL_VERBOSE"); got != tc.want {
				t.Errorf("web UI executor has LABCTL_VERBOSE=%q, want %q", got, tc.want)
			}
		})
	}
}
