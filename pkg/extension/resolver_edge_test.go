// SPDX-License-Identifier: Apache-2.0
package extension

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDefaultResolver_ChainComposition verifies that DefaultResolver returns a
// chain handling both supported ref schemes, and rejects an unsupported one.
func TestDefaultResolver_ChainComposition(t *testing.T) {
	src := t.TempDir()
	chain := DefaultResolver()

	tests := []struct {
		name string
		ref  string
		want bool
	}{
		{"local dir", src, true},
		{"git https url", "https://github.com/example/content", true},
		{"git+ prefixed url", "git+https://github.com/example/content@v1", true},
		{"oci ref is no longer supported", "oci://ghcr.io/example/pack:v1", false},
		{"nonexistent local path", filepath.Join(src, "missing"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := chain.CanResolve(tt.ref); got != tt.want {
				t.Errorf("CanResolve(%q) = %v, want %v", tt.ref, got, tt.want)
			}
		})
	}
}

// TestSplitGitRef covers the URL / @ref splitting logic.
func TestSplitGitRef(t *testing.T) {
	tests := []struct {
		src string
		url string
		ref string
	}{
		{"https://github.com/x/y@v1.0", "https://github.com/x/y", "v1.0"},
		{"https://github.com/x/y", "https://github.com/x/y", ""},
		// scp-like: the @ is part of the host, not a ref separator
		{"git@github.com:x/y.git", "git@github.com:x/y.git", ""},
	}
	for _, tt := range tests {
		u, r := splitGitRef(tt.src)
		if u != tt.url || r != tt.ref {
			t.Errorf("splitGitRef(%q) = (%q, %q), want (%q, %q)", tt.src, u, r, tt.url, tt.ref)
		}
	}
}

// TestLocalResolver_FilePrefix verifies that file:// refs are accepted.
func TestLocalResolver_FilePrefix(t *testing.T) {
	dir := t.TempDir()
	r := LocalResolver{}
	if !r.CanResolve("file://" + dir) {
		t.Error("LocalResolver should CanResolve file:// refs")
	}
}

// TestLocalResolver_NonexistentDir verifies that a path that does not exist
// is not claimed by LocalResolver.
func TestLocalResolver_NonexistentDir(t *testing.T) {
	r := LocalResolver{}
	if r.CanResolve("/totally/nonexistent/path/xyz") {
		t.Error("LocalResolver should not CanResolve a path that does not exist")
	}
}

// TestGitResolver_ErrorPropagation verifies that Resolve wraps runner errors.
func TestGitResolver_ErrorPropagation(t *testing.T) {
	r := GitResolver{Run: func(ctx context.Context, dir string, args ...string) error {
		return errors.New("network timeout")
	}}
	err := r.Resolve(context.Background(), "https://github.com/x/y", t.TempDir())
	if err == nil {
		t.Fatal("expected error from failed git runner, got nil")
	}
	if !strings.Contains(err.Error(), "cloning") {
		t.Errorf("error should mention 'cloning': %v", err)
	}
}

// TestCopyTree_CopiesFiles verifies that copyTree copies files and directories
// recursively.
func TestCopyTree_CopiesFiles(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "out")

	// Create source structure.
	os.MkdirAll(filepath.Join(src, "sub"), 0755)
	os.WriteFile(filepath.Join(src, "root.txt"), []byte("root"), 0644)
	os.WriteFile(filepath.Join(src, "sub", "nested.txt"), []byte("nested"), 0644)

	if err := copyTree(src, dst); err != nil {
		t.Fatalf("copyTree: %v", err)
	}

	// Verify copied files exist.
	for _, rel := range []string{"root.txt", filepath.Join("sub", "nested.txt")} {
		if _, err := os.Stat(filepath.Join(dst, rel)); err != nil {
			t.Errorf("expected %s to exist in dst: %v", rel, err)
		}
	}
}
