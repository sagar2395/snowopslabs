// SPDX-License-Identifier: Apache-2.0

package extension

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChain_DispatchesToFirstMatch(t *testing.T) {
	var hitA, hitB bool
	a := ResolveFunc{
		Match: func(ref string) bool { return strings.HasPrefix(ref, "a:") },
		Fetch: func(context.Context, string, string) error { hitA = true; return nil },
	}
	b := ResolveFunc{
		Match: func(ref string) bool { return strings.HasPrefix(ref, "b:") },
		Fetch: func(context.Context, string, string) error { hitB = true; return nil },
	}
	c := Chain{a, b}

	if !c.CanResolve("b:thing") {
		t.Fatal("chain should report it can resolve b:")
	}
	if err := c.Resolve(context.Background(), "b:thing", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if hitA || !hitB {
		t.Errorf("expected only resolver b to run (a=%v b=%v)", hitA, hitB)
	}
	if err := c.Resolve(context.Background(), "z:none", t.TempDir()); err == nil {
		t.Error("expected error when no resolver matches")
	}
}

func TestIsGitRef(t *testing.T) {
	git := []string{"https://github.com/x/y", "git@github.com:x/y.git", "git+https://h/x@v1", "ssh://h/x"}
	notGit := []string{"oci://ghcr.io/x/y:1", "hello-pack", "./local", "file:///tmp/x"}
	for _, r := range git {
		if !IsGitRef(r) {
			t.Errorf("IsGitRef(%q) = false, want true", r)
		}
	}
	for _, r := range notGit {
		if IsGitRef(r) {
			t.Errorf("IsGitRef(%q) = true, want false", r)
		}
	}
}

func TestNoopHooks_NeverError(t *testing.T) {
	h := DefaultHooks()
	ev := Event{Scenario: "s", Stage: "st", Check: "c"}
	ctx := context.Background()
	if h.PreStage(ctx, ev) != nil || h.PostStage(ctx, ev) != nil ||
		h.PreCheck(ctx, ev) != nil || h.PostCheck(ctx, ev) != nil {
		t.Error("no-op hooks must never error")
	}
}

func TestLocalResolver_CopiesTree(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "pack.yaml"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "scn"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "scn", "scenario.yaml"), []byte("name: x"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := LocalResolver{}
	if !r.CanResolve(src) {
		t.Fatal("LocalResolver should resolve an existing dir")
	}
	dst := filepath.Join(t.TempDir(), "out")
	if err := r.Resolve(context.Background(), src, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "scn", "scenario.yaml")); err != nil {
		t.Errorf("tree not copied: %v", err)
	}
}

func TestGitResolver_StubRunner(t *testing.T) {
	var gotArgs []string
	r := GitResolver{Run: func(_ context.Context, _ string, args ...string) error {
		gotArgs = args
		return nil
	}}
	if !r.CanResolve("https://github.com/x/y") {
		t.Fatal("GitResolver should resolve an https git URL")
	}
	if r.CanResolve("oci://ghcr.io/x/y:1") {
		t.Error("GitResolver must not claim oci refs")
	}
	if err := r.Resolve(context.Background(), "git+https://github.com/x/y@v2", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(gotArgs, " ")
	if !strings.Contains(joined, "clone") || !strings.Contains(joined, "--branch v2") ||
		!strings.Contains(joined, "https://github.com/x/y") {
		t.Errorf("git args wrong: %v", gotArgs)
	}
	if strings.Contains(joined, "git+") {
		t.Errorf("git+ prefix should be stripped: %v", gotArgs)
	}
}
