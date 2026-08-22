// SPDX-License-Identifier: Apache-2.0

package extension

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Built-in, open resolvers. These wrap the mechanisms the CLI already uses (git
// via the git CLI, local dirs via a copy), exposed behind the Resolver interface
// so additional content sources can be added to a Chain without touching the
// engine. See docs/adr/0008-content-extensibility-seam.md.

// GitRunner runs a git command in dir; tests inject a stub.
type GitRunner func(ctx context.Context, dir string, args ...string) error

// DefaultGitRunner shells out to git, streaming output.
func DefaultGitRunner(ctx context.Context, dir string, args ...string) error {
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("git not found on PATH: %w", err)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

// GitResolver clones a git source (URL[@ref] or git+https://…@ref) into destDir
// as a content snapshot (its .git is dropped).
type GitResolver struct {
	Run GitRunner // defaults to DefaultGitRunner
}

// CanResolve handles git refs that are not OCI refs.
func (GitResolver) CanResolve(ref string) bool { return IsGitRef(ref) }

// Resolve clones the source into destDir at an optional @ref.
func (r GitResolver) Resolve(ctx context.Context, ref, destDir string) error {
	run := r.Run
	if run == nil {
		run = DefaultGitRunner
	}
	url := strings.TrimPrefix(ref, "git+")
	url, gitRef := splitGitRef(url)

	args := []string{"clone", "--depth", "1"}
	if gitRef != "" {
		args = append(args, "--branch", gitRef)
	}
	args = append(args, url, destDir)
	if err := run(ctx, "", args...); err != nil {
		return fmt.Errorf("cloning %s: %w", url, err)
	}
	return os.RemoveAll(filepath.Join(destDir, ".git"))
}

// LocalResolver copies a pack from a local directory (or a file:// path). Useful
// for offline development and as the simplest reference resolver.
type LocalResolver struct{}

// CanResolve handles file:// refs and existing local directories.
func (LocalResolver) CanResolve(ref string) bool {
	if strings.HasPrefix(ref, "file://") {
		return true
	}
	if IsGitRef(ref) {
		return false
	}
	info, err := os.Stat(ref)
	return err == nil && info.IsDir()
}

// Resolve copies the local pack tree into destDir.
func (LocalResolver) Resolve(_ context.Context, ref, destDir string) error {
	src := strings.TrimPrefix(ref, "file://")
	return copyTree(src, destDir)
}

// DefaultResolver returns the open resolver chain: git, then local.
func DefaultResolver() Chain {
	return Chain{GitResolver{}, LocalResolver{}}
}

// splitGitRef separates an optional "@ref" suffix from a git URL, preserving
// scp-like URLs (git@host:org/repo.git stays intact).
func splitGitRef(src string) (url, ref string) {
	i := strings.LastIndex(src, "@")
	if i <= 0 {
		return src, ""
	}
	if i < strings.LastIndex(src, "/") || i < strings.LastIndex(src, ":") {
		return src, ""
	}
	return src[:i], src[i+1:]
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
}
