// SPDX-License-Identifier: Apache-2.0

package toolchain

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCommandString(t *testing.T) {
	tests := []struct {
		name string
		cmd  Command
		want string
	}{
		{"no args", Command{Path: "/bin/ls"}, "/bin/ls"},
		{"simple args", Command{Path: "/bin/kubectl", Args: []string{"get", "pods"}}, "/bin/kubectl get pods"},
		{
			"quotes an argument containing spaces",
			Command{Path: "/bin/helm", Args: []string{"--set", "a=b c"}},
			`/bin/helm --set "a=b c"`,
		},
		{
			"quotes an argument containing a quote",
			Command{Path: "/bin/sh", Args: []string{`say "hi"`}},
			`/bin/sh "say \"hi\""`,
		},
		{
			"quotes an argument containing a newline",
			Command{Path: "/bin/sh", Args: []string{"a\nb"}},
			`/bin/sh "a\nb"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cmd.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCommandEnvSlice(t *testing.T) {
	t.Run("is sorted for determinism", func(t *testing.T) {
		cmd := Command{Env: map[string]string{"ZED": "1", "ALPHA": "2", "MID": "3"}}
		got := strings.Join(cmd.EnvSlice(), ",")
		if want := "ALPHA=2,MID=3,ZED=1"; got != want {
			t.Errorf("EnvSlice() = %q, want %q", got, want)
		}
	})

	t.Run("nil env yields an empty slice", func(t *testing.T) {
		if got := (Command{}).EnvSlice(); len(got) != 0 {
			t.Errorf("EnvSlice() = %v, want empty", got)
		}
	})
}

func TestExitError(t *testing.T) {
	tests := []struct {
		name string
		err  *ExitError
		want string
	}{
		{"without stderr", &ExitError{Command: "helm install", ExitCode: 1}, "helm install exited 1"},
		{
			"includes stderr when captured",
			&ExitError{Command: "helm install", ExitCode: 1, Stderr: "release exists"},
			"helm install exited 1: release exists",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}

	t.Run("is retrievable with errors.As", func(t *testing.T) {
		var target *ExitError
		err := error(&ExitError{Command: "x", ExitCode: 42})
		if !errors.As(err, &target) || target.ExitCode != 42 {
			t.Errorf("errors.As failed to recover the exit code")
		}
	})
}

func TestNewResolver(t *testing.T) {
	t.Run("rejects no roots", func(t *testing.T) {
		if _, err := NewResolver(); err == nil {
			t.Fatal("expected an error with no roots")
		}
	})

	t.Run("rejects only-empty roots", func(t *testing.T) {
		if _, err := NewResolver("", ""); err == nil {
			t.Fatal("expected an error when every root is empty")
		}
	})

	t.Run("makes roots absolute", func(t *testing.T) {
		r, err := NewResolver(".")
		if err != nil {
			t.Fatalf("NewResolver: %v", err)
		}
		if !filepath.IsAbs(r.Roots[0]) {
			t.Errorf("root %q is not absolute", r.Roots[0])
		}
	})

	t.Run("accepts a root that does not exist yet", func(t *testing.T) {
		// A content path may be configured before it is populated.
		missing := filepath.Join(t.TempDir(), "not-created-yet")
		if _, err := NewResolver(missing); err != nil {
			t.Errorf("NewResolver: %v", err)
		}
	})
}

// TestResolverContainment is the security-relevant test in this package. v1
// joined a caller-supplied path onto the project root and ran the result, so a
// traversal escaped happily. Content can come from an external root the user
// pointed at, which makes this check load-bearing.
func TestResolverContainment(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	mustWrite := func(path, body string) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		return path
	}

	inside := mustWrite(filepath.Join(root, "install.sh"), "#!/bin/sh\n")
	nested := mustWrite(filepath.Join(root, "platform", "ingress", "install.sh"), "#!/bin/sh\n")
	secret := mustWrite(filepath.Join(outside, "evil.sh"), "#!/bin/sh\n")

	r, err := NewResolver(root)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	t.Run("resolves a script inside the root", func(t *testing.T) {
		got, err := r.Resolve("install.sh")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if !sameFile(t, got, inside) {
			t.Errorf("resolved to %q, want %q", got, inside)
		}
	})

	t.Run("resolves a nested script", func(t *testing.T) {
		got, err := r.Resolve(filepath.Join("platform", "ingress", "install.sh"))
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if !sameFile(t, got, nested) {
			t.Errorf("resolved to %q, want %q", got, nested)
		}
	})

	t.Run("rejects traversal out of the root", func(t *testing.T) {
		for _, path := range []string{
			"../evil.sh",
			"../../etc/passwd",
			"platform/../../evil.sh",
			"./../../evil.sh",
		} {
			t.Run(path, func(t *testing.T) {
				_, err := r.Resolve(path)
				if err == nil {
					t.Fatalf("Resolve(%q) succeeded; traversal must be refused", path)
				}
				if !errors.Is(err, ErrOutsideRoot) && !errors.Is(err, ErrScriptNotFound) {
					t.Errorf("error = %v, want ErrOutsideRoot or ErrScriptNotFound", err)
				}
			})
		}
	})

	t.Run("rejects an absolute path outside the root", func(t *testing.T) {
		_, err := r.Resolve(secret)
		if !errors.Is(err, ErrOutsideRoot) {
			t.Fatalf("error = %v, want ErrOutsideRoot", err)
		}
	})

	t.Run("rejects a symlink pointing out of the root", func(t *testing.T) {
		// The subtle case: the path looks contained, but following it escapes.
		// Checking only the cleaned path would let this through.
		link := filepath.Join(root, "sneaky.sh")
		if err := os.Symlink(secret, link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		_, err := r.Resolve("sneaky.sh")
		if !errors.Is(err, ErrOutsideRoot) {
			t.Fatalf("error = %v, want ErrOutsideRoot", err)
		}
	})

	t.Run("allows a symlink staying inside the root", func(t *testing.T) {
		link := filepath.Join(root, "alias.sh")
		if err := os.Symlink(inside, link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if _, err := r.Resolve("alias.sh"); err != nil {
			t.Errorf("Resolve: %v", err)
		}
	})

	t.Run("a sibling directory with a shared prefix is not inside", func(t *testing.T) {
		// "/tmp/root-evil" must not count as inside "/tmp/root".
		base := t.TempDir()
		contained := filepath.Join(base, "root")
		sibling := filepath.Join(base, "root-evil")
		mustWrite(filepath.Join(contained, "ok.sh"), "#!/bin/sh\n")
		evil := mustWrite(filepath.Join(sibling, "evil.sh"), "#!/bin/sh\n")

		res, err := NewResolver(contained)
		if err != nil {
			t.Fatalf("NewResolver: %v", err)
		}
		if _, err := res.Resolve(evil); !errors.Is(err, ErrOutsideRoot) {
			t.Fatalf("error = %v, want ErrOutsideRoot", err)
		}
	})

	t.Run("rejects a missing script", func(t *testing.T) {
		_, err := r.Resolve("nope.sh")
		if !errors.Is(err, ErrScriptNotFound) {
			t.Fatalf("error = %v, want ErrScriptNotFound", err)
		}
		// The message must say where we looked.
		if !strings.Contains(err.Error(), root) {
			t.Errorf("error should name the searched root: %v", err)
		}
	})

	t.Run("rejects an empty path", func(t *testing.T) {
		if _, err := r.Resolve(""); !errors.Is(err, ErrScriptNotFound) {
			t.Fatalf("error = %v, want ErrScriptNotFound", err)
		}
	})

	t.Run("rejects a directory", func(t *testing.T) {
		_, err := r.Resolve("platform")
		if !errors.Is(err, ErrScriptNotFound) {
			t.Fatalf("error = %v, want ErrScriptNotFound", err)
		}
		if !strings.Contains(err.Error(), "directory") {
			t.Errorf("error should say it is a directory: %v", err)
		}
	})
}

func TestResolverMultipleRoots(t *testing.T) {
	// The SNOWOPS_CONTENT_PATH case: content from more than one place.
	repoRoot := t.TempDir()
	extraRoot := t.TempDir()

	write := func(dir, name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	write(repoRoot, "in-repo.sh")
	write(extraRoot, "external.sh")
	write(repoRoot, "shared.sh")
	write(extraRoot, "shared.sh")

	r, err := NewResolver(repoRoot, extraRoot)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	t.Run("finds a script in either root", func(t *testing.T) {
		for _, name := range []string{"in-repo.sh", "external.sh"} {
			if _, err := r.Resolve(name); err != nil {
				t.Errorf("Resolve(%s): %v", name, err)
			}
		}
	})

	t.Run("the first root wins a collision", func(t *testing.T) {
		// Matches the catalog rule: in-repo content beats external content.
		got, err := r.Resolve("shared.sh")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if !strings.HasPrefix(got, mustEval(t, repoRoot)) {
			t.Errorf("resolved to %q, want it under the first root %q", got, repoRoot)
		}
	})
}

func TestParseVersion(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "1.29.0", "1.29.0"},
		{"v-prefixed", "v1.29.0", "1.29.0"},
		{"helm short", "v3.14.2+g0e6a2a", "3.14.2"},
		{"k3d", "k3d version v5.8.3\nk3s version v1.31.5-k3s1 (default)", "5.8.3"},
		{"kind", "kind v0.27.0 go1.23.4 linux/amd64", "0.27.0"},
		{"kubectl json", `{"clientVersion":{"gitVersion":"v1.29.0"}}`, "1.29.0"},
		{"bash", "GNU bash, version 5.2.21(1)-release (x86_64-pc-linux-gnu)", "5.2.21"},
		{"macos bash", "GNU bash, version 3.2.57(1)-release (arm64-apple-darwin23)", "3.2.57"},
		{"two-part version gets a zero patch", "1.29", "1.29.0"},
		{"no version present", "command not found", ""},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseVersion(tt.in); got != tt.want {
				t.Errorf("parseVersion(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.29.0", "1.28.0", 1},
		{"1.28.0", "1.29.0", -1},
		{"1.29.0", "1.29.0", 0},
		{"2.0.0", "1.99.99", 1},
		{"1.29.1", "1.29.0", 1},
		{"v1.29.0", "1.29.0", 0},
		{"3.2.57", "3.2.0", 1},
		{"0.20.0", "0.27.0", -1},
		{"1.29", "1.29.0", 0},
	}
	for _, tt := range tests {
		t.Run(tt.a+" vs "+tt.b, func(t *testing.T) {
			got, err := compareVersions(tt.a, tt.b)
			if err != nil {
				t.Fatalf("compareVersions: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}

	t.Run("rejects unparseable input", func(t *testing.T) {
		for _, bad := range []string{"", "abc", "1", "x.y.z"} {
			if _, err := compareVersions(bad, "1.0.0"); err == nil {
				t.Errorf("compareVersions(%q) should have failed", bad)
			}
		}
	})
}

func sameFile(t *testing.T, a, b string) bool {
	t.Helper()
	return mustEval(t, a) == mustEval(t, b)
}

func mustEval(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	return resolved
}

func TestResolverOnWindowsPathsIsSkipped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SnowOps Labs targets macOS and Linux only")
	}
}
