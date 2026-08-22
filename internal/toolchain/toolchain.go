// SPDX-License-Identifier: Apache-2.0

// Package toolchain is the single door between SnowOps Labs and the external
// binaries it orchestrates: bash, kubectl, helm, k3d, kind.
//
// Everything here exists because of one rule — Go orchestrates, scripts do the
// work — and one hazard: those scripts run against a real cluster with the
// user's credentials. So the package guarantees three things.
//
//  1. Commands are built as argv arrays. No user-supplied value is ever
//     interpolated into a shell string, because there is no shell string.
//  2. Script paths resolve inside an allowlisted root, symlinks included. A
//     scenario cannot reach outside its content root by way of "../".
//  3. Every adapter has a Fake, so the layers above can be tested without a
//     cluster, a network, or a real binary anywhere in sight.
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

// Command is a fully-resolved invocation, ready to execute. It is deliberately
// inert: building one performs validation but never runs anything, so callers
// can construct, inspect and log a command before deciding to execute it.
type Command struct {
	// Path is the absolute path to the binary.
	Path string
	// Args are the arguments after the binary name. Never a shell string.
	Args []string
	// Dir is the working directory; empty means the caller's.
	Dir string
	// Env is added to (not substituted for) the parent environment. Scripts
	// read ${VAR:-default} from here — golden rule 3 says they must never
	// source .env themselves.
	Env map[string]string
	// Stdout and Stderr receive output. Nil discards it.
	Stdout, Stderr io.Writer
}

// String renders the command for logs and error messages. Arguments containing
// whitespace are quoted so the output is unambiguous, but this is for humans
// only — it is never parsed back or handed to a shell.
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

// Env sorted deterministically, as KEY=VALUE. Used when constructing the
// process environment and when asserting in tests.
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
	// Signalled is true when the process was terminated by a signal rather
	// than exiting on its own — the normal outcome of a cancellation.
	Signalled bool
}

// ExitError reports a non-zero exit. Callers use errors.As to read the code
// rather than parsing a message.
type ExitError struct {
	Command  string
	ExitCode int
	// Stderr is the tail of the error output, when captured. Enough to make
	// the error actionable without dumping the whole log into one line.
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
// Run must honour ctx: on cancellation it terminates the process *group*, not
// just the direct child, because helm and kubectl spawn their own children
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

// Resolver turns a relative script path into an absolute one, refusing any
// path that leaves its root.
//
// This closes a real hole: v1's executor joined a caller-supplied path onto the
// project root and ran whatever came out, so "../../../../usr/bin/whatever"
// would have executed happily. Content can come from an external root the user
// pointed at, so this check is load-bearing, not theoretical.
type Resolver struct {
	// Roots are the directories scripts may live in, absolute and with
	// symlinks already resolved.
	Roots []string
}

// NewResolver builds a Resolver from the given roots. Each root is made
// absolute and symlink-resolved once, up front, so the per-call check is a
// cheap prefix comparison against a canonical path.
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
		// A root that does not exist yet is not an error — a content path may
		// be configured before it is populated — but one that does exist gets
		// its symlinks collapsed so containment checks compare like with like.
		if real, err := filepath.EvalSymlinks(abs); err == nil {
			abs = real
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
		// EvalSymlinks both proves existence and collapses any symlink that
		// might point out of the root. Checking the cleaned path alone would
		// miss a symlink planted inside the tree.
		real, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			continue
		}
		if !r.contains(real) {
			if firstOutside == nil {
				firstOutside = fmt.Errorf("%w: %s resolves to %s, which is outside %s",
					ErrOutsideRoot, script, real, strings.Join(r.Roots, ", "))
			}
			continue
		}
		info, err := os.Stat(real)
		if err != nil {
			continue
		}
		if info.IsDir() {
			return "", fmt.Errorf("%w: %s is a directory", ErrScriptNotFound, script)
		}
		return real, nil
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
