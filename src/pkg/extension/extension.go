// SPDX-License-Identifier: Apache-2.0

// Package extension defines two extension points (ADR-0008): Resolver, which
// fetches content packs from a source, and Hooks, which run around scenario
// stages and checks. The defaults are resolvers for git and local directories
// and hooks that do nothing. Another build can supply its own when it
// constructs the engine, without changing the engine.
//
// It is part of the public SDK and must not import internal/.
package extension

import (
	"context"
	"fmt"
	"strings"
)

// Resolver fetches the pack referenced by ref into destDir. CanResolve lets a
// Chain choose the resolver for a ref.
type Resolver interface {
	CanResolve(ref string) bool
	Resolve(ctx context.Context, ref, destDir string) error
}

// Chain tries each resolver in order and uses the first whose CanResolve
// matches. It is itself a Resolver, so chains compose.
type Chain []Resolver

// CanResolve reports whether any resolver in the chain handles ref.
func (c Chain) CanResolve(ref string) bool {
	for _, r := range c {
		if r.CanResolve(ref) {
			return true
		}
	}
	return false
}

// Resolve dispatches ref to the first matching resolver.
func (c Chain) Resolve(ctx context.Context, ref, destDir string) error {
	for _, r := range c {
		if r.CanResolve(ref) {
			return r.Resolve(ctx, ref, destDir)
		}
	}
	return fmt.Errorf("no resolver can handle ref %q", ref)
}

// ResolveFunc builds a Resolver from a CanResolve function and a Resolve
// function.
type ResolveFunc struct {
	Match func(ref string) bool
	Fetch func(ctx context.Context, ref, destDir string) error
}

// CanResolve implements Resolver.
func (f ResolveFunc) CanResolve(ref string) bool { return f.Match != nil && f.Match(ref) }

// Resolve implements Resolver.
func (f ResolveFunc) Resolve(ctx context.Context, ref, destDir string) error {
	return f.Fetch(ctx, ref, destDir)
}

// IsGitRef reports whether ref looks like a git source (a URL scheme or an scp-
// like git@host:path), excluding OCI and local file refs.
func IsGitRef(ref string) bool {
	if strings.HasPrefix(ref, "oci://") || strings.HasPrefix(ref, "file://") {
		return false
	}
	return strings.Contains(ref, "://") || strings.HasPrefix(ref, "git@") || strings.HasPrefix(ref, "git+")
}

// --- lifecycle hooks ---

// Event identifies a lifecycle point. Stage is set for stage hooks, Check for
// check hooks; Scenario is always set.
type Event struct {
	Scenario string
	Stage    string
	Check    string
}

// Hooks are called around scenario stages and checks. An error from a Pre*
// hook stops that phase. The default hooks do nothing.
type Hooks interface {
	PreStage(ctx context.Context, ev Event) error
	PostStage(ctx context.Context, ev Event) error
	PreCheck(ctx context.Context, ev Event) error
	PostCheck(ctx context.Context, ev Event) error
}

// NoopHooks is the default Hooks: every method returns nil. Embed it to
// implement only the hooks you need.
type NoopHooks struct{}

// PreStage does nothing.
func (NoopHooks) PreStage(context.Context, Event) error { return nil }

// PostStage does nothing.
func (NoopHooks) PostStage(context.Context, Event) error { return nil }

// PreCheck does nothing.
func (NoopHooks) PreCheck(context.Context, Event) error { return nil }

// PostCheck does nothing.
func (NoopHooks) PostCheck(context.Context, Event) error { return nil }

// DefaultHooks returns NoopHooks.
func DefaultHooks() Hooks { return NoopHooks{} }
