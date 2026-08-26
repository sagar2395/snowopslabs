// SPDX-License-Identifier: Apache-2.0

// Package extension defines two anti-lock-in seams (task 070): where pack
// content comes from (Resolver) and optional lifecycle hooks around scenario
// stages and checks (Hooks). The OSS core ships open defaults — built-in
// resolvers for OCI / git / local sources and no-op hooks — so the open engine
// behaves identically. Premium, private, or hosted builds inject custom
// resolvers and hooks at construction without modifying the engine.
//
// Hooks are data/script-driven by design; this package never introduces compiled
// Go plugins. It is part of the public SDK and must not import internal/.
package extension

import (
	"context"
	"fmt"
	"strings"
)

// Resolver fetches the pack referenced by ref into destDir. CanResolve lets a
// Chain pick the right resolver for a ref's scheme, so new source kinds (private
// registries, hosted catalogs) slot in without engine changes.
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

// ResolveFunc adapts a CanResolve predicate + resolve function into a Resolver,
// so callers (and the OSS built-ins) can construct resolvers without a struct.
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

// Hooks are invoked around scenario lifecycle phases. Any method may be a no-op.
// A non-nil error from a Pre* hook aborts that phase (so premium policy can fail
// closed); the OSS default never errors.
type Hooks interface {
	PreStage(ctx context.Context, ev Event) error
	PostStage(ctx context.Context, ev Event) error
	PreCheck(ctx context.Context, ev Event) error
	PostCheck(ctx context.Context, ev Event) error
}

// NoopHooks is the OSS default: every method returns nil. Embed it to implement
// only the hooks you care about.
type NoopHooks struct{}

func (NoopHooks) PreStage(context.Context, Event) error  { return nil }
func (NoopHooks) PostStage(context.Context, Event) error { return nil }
func (NoopHooks) PreCheck(context.Context, Event) error  { return nil }
func (NoopHooks) PostCheck(context.Context, Event) error { return nil }

// DefaultHooks returns the open no-op hooks.
func DefaultHooks() Hooks { return NoopHooks{} }
