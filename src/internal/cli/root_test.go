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
		t.Errorf("cmd/labctl/main.go declares %s — move it into internal/cli, "+
			"where the coverage gate applies (ADR-0005)", strings.Join(otherDecls, ", "))
	}

	// main must be a single call into internal/cli.
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
