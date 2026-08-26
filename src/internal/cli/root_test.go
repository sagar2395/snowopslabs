// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMainStaysTrivial guards ADR-0005's entrypoint rule.
//
// cmd/labctl contributes no statements to the coverage profile, so the coverage
// gate never evaluates it. That is only acceptable while there is nothing there
// worth evaluating. This test makes "nothing worth evaluating" an enforced
// property rather than a hope: main() must do one thing, and any logic that
// creeps in belongs in internal/cli where it can be tested.
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
			// Any var/const/type declaration is state that wants testing — with
			// one allowed exception: `var version`, stamped by the linker via
			// -X main.version at release time. It carries no logic and cannot
			// live in internal/cli (ldflags target the main package).
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
		t.Errorf("cmd/labctl/main.go declares %s — move it into internal/cli, "+
			"where the coverage gate applies (ADR-0005)", strings.Join(otherDecls, ", "))
	}

	// One statement: hand off to internal/cli. Anything more is logic hiding
	// in the one package nothing measures.
	if n := len(mainFn.Body.List); n > 1 {
		t.Errorf("main() has %d statements, want 1 — cmd/labctl is an entrypoint only (ADR-0005). "+
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

// TestPersistentPreRunSkipsEnvironmentDependentCommands ensures the commands
// that must work in a broken environment are exempt from the heavy
// initialisation in PersistentPreRunE.
//
// doctor is the obvious one: a command whose job is diagnosing a broken
// environment is useless if a broken environment stops it from starting.
func TestPersistentPreRunSkipsEnvironmentDependentCommands(t *testing.T) {
	path := filepath.Join("root.go")
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
