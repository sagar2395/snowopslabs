// SPDX-License-Identifier: Apache-2.0

// Package toolchain runs the external binaries labctl depends on: bash,
// kubectl, helm, k3d and kind. Those scripts run against a real cluster with
// the user's credentials, so the package guarantees that:
//
//  1. Commands are argv arrays. No value is ever interpolated into a shell
//     string.
//  2. Script paths resolve inside an allowed content root, after following
//     symlinks, so a scenario cannot reach outside its root with "../".
//  3. Runner has a Fake, so the layers above can be tested without a
//     cluster, a network or real binaries.
package toolchain

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Command is a fully resolved invocation. Building one runs nothing; pass it
// to a Runner to execute it.
type Command struct {
	// Path is the absolute path to the binary.
	Path string
	// Args are the arguments after the binary name. Never a shell string.
	Args []string
	// Dir is the working directory; empty means the caller's.
	Dir string
	// Env is added to the parent environment rather than replacing it.
	// Scripts read their settings as ${VAR:-default} and never source .env
	// themselves, so this is how configuration reaches them.
	Env map[string]string
	// Stdout and Stderr receive output. Nil discards it.
	Stdout, Stderr io.Writer
}

// String renders the command for logs and error messages, quoting arguments
// that contain whitespace. It is for display only, never for a shell.
func (c Command) String() string {
	parts := make([]string, 0, len(c.Args)+1)
	parts = append(parts, c.Path)
	for _, a := range c.Args {
		if strings.ContainsAny(a, " \t\n\"'") {
			parts = append(parts, fmt.Sprintf("%q", a))
			continue
		}
		parts = append(parts, a)
	}
	return strings.Join(parts, " ")
}

// EnvSlice returns Env as KEY=VALUE pairs sorted by key.
func (c Command) EnvSlice() []string {
	keys := make([]string, 0, len(c.Env))
	for k := range c.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+c.Env[k])
	}
	return out
}

// Result is the outcome of an execution.
type Result struct {
	ExitCode int
	// Signalled is true when the process was killed by a signal instead of
	// exiting on its own, as happens when a run is cancelled.
	Signalled bool
}

// ExitError reports a non-zero exit. Callers use errors.As to read the code
// rather than parsing a message.
type ExitError struct {
	Command  string
	ExitCode int
	// Stderr is the tail of the error output, when it was captured.
	Stderr string
}

func (e *ExitError) Error() string {
	msg := fmt.Sprintf("%s exited %d", e.Command, e.ExitCode)
	if e.Stderr != "" {
		msg += ": " + e.Stderr
	}
	return msg
}

// Runner executes commands. Production uses Exec; tests use Fake.
//
// Run must honour ctx. On cancellation it stops the whole process group, not
// just the direct child, because helm and kubectl start children of their own
// (ADR-0003).
type Runner interface {
	Run(ctx context.Context, cmd Command) (Result, error)
	// LookPath resolves a binary on PATH, returning its absolute location.
	LookPath(name string) (string, error)
}

// ── Script resolution ───────────────────────────────────────────────────────

// ErrOutsideRoot is returned when a script path escapes its allowlisted root.
var ErrOutsideRoot = errors.New("script path escapes its content root")

// ErrScriptNotFound is returned when a script does not exist.
var ErrScriptNotFound = errors.New("script not found")

// Resolver turns a script path into an absolute one and refuses any path that
// leaves its roots, such as "../../usr/bin/x". Content can come from external
// roots the user configured, so every script goes through this check.
type Resolver struct {
	// Roots are the directories scripts may live in, absolute and with
	// symlinks already resolved.
	Roots []string
}

// NewResolver builds a Resolver from the given roots, making each one
// absolute and resolving its symlinks up front.
func NewResolver(roots ...string) (*Resolver, error) {
	if len(roots) == 0 {
		return nil, errors.New("toolchain: at least one content root is required")
	}
	resolved := make([]string, 0, len(roots))
	for _, r := range roots {
		if r == "" {
			continue
		}
		abs, err := filepath.Abs(r)
		if err != nil {
			return nil, fmt.Errorf("resolving content root %q: %w", r, err)
		}
		// A root may be configured before it exists, so a failure here is
		// not an error; the path is then used as given.
		if resolvedAbs, err := filepath.EvalSymlinks(abs); err == nil {
			abs = resolvedAbs
		}
		resolved = append(resolved, abs)
	}
	if len(resolved) == 0 {
		return nil, errors.New("toolchain: at least one non-empty content root is required")
	}
	return &Resolver{Roots: resolved}, nil
}

// Resolve returns the absolute path of script, which may be given relative to
// any root. It fails if the script does not exist, or if the resolved path —
// after following symlinks — lies outside every root.
func (r *Resolver) Resolve(script string) (string, error) {
	if script == "" {
		return "", fmt.Errorf("%w: empty path", ErrScriptNotFound)
	}

	candidates := make([]string, 0, len(r.Roots)+1)
	if filepath.IsAbs(script) {
		candidates = append(candidates, filepath.Clean(script))
	} else {
		for _, root := range r.Roots {
			candidates = append(candidates, filepath.Join(root, script))
		}
	}

	var firstOutside error
	for _, candidate := range candidates {
		// Resolving symlinks both confirms the file exists and catches a
		// link inside the root that points outside it.
		target, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			continue
		}
		if !r.contains(target) {
			if firstOutside == nil {
				firstOutside = fmt.Errorf("%w: %s resolves to %s, which is outside %s",
					ErrOutsideRoot, script, target, strings.Join(r.Roots, ", "))
			}
			continue
		}
		info, err := os.Stat(target)
		if err != nil {
			continue
		}
		if info.IsDir() {
			return "", fmt.Errorf("%w: %s is a directory", ErrScriptNotFound, script)
		}
		return target, nil
	}

	if firstOutside != nil {
		return "", firstOutside
	}
	return "", fmt.Errorf("%w: %s (searched %s)", ErrScriptNotFound, script, strings.Join(r.Roots, ", "))
}

// contains reports whether path lies within one of the roots. Comparison is on
// path segments, so "/roots" is not treated as being inside "/root".
func (r *Resolver) contains(path string) bool {
	for _, root := range r.Roots {
		if path == root {
			return true
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			continue
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if filepath.IsAbs(rel) {
			continue
		}
		return true
	}
	return false
}
