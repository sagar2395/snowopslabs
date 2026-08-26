// SPDX-License-Identifier: Apache-2.0

package toolchain

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// The Fake underpins every hermetic test above this package, so its own
// contract needs to hold: faithful recording, scripted responses, and
// cancellation semantics that match Exec.

func TestFakeRun(t *testing.T) {
	ctx := context.Background()

	t.Run("an unscripted command succeeds silently", func(t *testing.T) {
		f := NewFake()
		var stdout bytes.Buffer
		res, err := f.Run(ctx, Command{Path: "/bin/helm", Args: []string{"version"}, Stdout: &stdout})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if res.ExitCode != 0 {
			t.Errorf("exit code = %d, want 0", res.ExitCode)
		}
		if stdout.Len() != 0 {
			t.Errorf("stdout = %q, want empty", stdout.String())
		}
	})

	t.Run("records every call in order", func(t *testing.T) {
		f := NewFake()
		for _, args := range [][]string{{"get", "pods"}, {"apply", "-f", "x.yaml"}} {
			if _, err := f.Run(ctx, Command{Path: "/bin/kubectl", Args: args}); err != nil {
				t.Fatalf("Run: %v", err)
			}
		}
		got := f.CallStrings()
		if len(got) != 2 {
			t.Fatalf("recorded %d calls, want 2", len(got))
		}
		if got[0] != "/bin/kubectl get pods" {
			t.Errorf("call 0 = %q", got[0])
		}
		if got[1] != "/bin/kubectl apply -f x.yaml" {
			t.Errorf("call 1 = %q", got[1])
		}
	})

	t.Run("matches rules in order, first wins", func(t *testing.T) {
		f := NewFake().
			WhenArgsContain("list", "my-release\n", 0).
			WhenArgsContain("helm", "generic\n", 0)

		var stdout bytes.Buffer
		if _, err := f.Run(ctx, Command{Path: "/bin/helm", Args: []string{"list"}, Stdout: &stdout}); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if got := strings.TrimSpace(stdout.String()); got != "my-release" {
			t.Errorf("stdout = %q, want the more specific rule to win", got)
		}
	})

	t.Run("returns an ExitError for a non-zero rule", func(t *testing.T) {
		f := NewFake().WhenArgsContainStderr("upgrade", "", "release exists", 1)
		var stdout, stderr bytes.Buffer

		res, err := f.Run(ctx, Command{
			Path: "/bin/helm", Args: []string{"upgrade", "--install", "x"},
			Stdout: &stdout, Stderr: &stderr,
		})

		var exitErr *ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("error = %v, want *ExitError", err)
		}
		if exitErr.ExitCode != 1 || res.ExitCode != 1 {
			t.Errorf("exit code = %d/%d, want 1", exitErr.ExitCode, res.ExitCode)
		}
		if got := stderr.String(); got != "release exists" {
			t.Errorf("stderr = %q", got)
		}
		if !strings.Contains(exitErr.Error(), "release exists") {
			t.Errorf("the error should carry stderr: %v", exitErr)
		}
	})

	t.Run("returns a transport error when scripted to", func(t *testing.T) {
		want := errors.New("permission denied")
		f := NewFake().WhenArgsContainError("install", want)

		_, err := f.Run(ctx, Command{Path: "/bin/helm", Args: []string{"install"}})
		if !errors.Is(err, want) {
			t.Fatalf("error = %v, want %v", err, want)
		}
		var exitErr *ExitError
		if errors.As(err, &exitErr) {
			t.Error("a transport error must not be reported as an exit code")
		}
	})

	t.Run("refuses to start on an already-cancelled context, like Exec", func(t *testing.T) {
		f := NewFake()
		cancelled, cancel := context.WithCancel(ctx)
		cancel()

		if _, err := f.Run(cancelled, Command{Path: "/bin/helm"}); !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
		if n := f.CallCount(""); n != 0 {
			t.Errorf("recorded %d calls; nothing should run on a cancelled context", n)
		}
	})

	t.Run("a blocking rule can be cancelled and reports as signalled", func(t *testing.T) {
		f := NewFake().WhenArgsContainBlock("install", time.Minute)

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct {
			res Result
			err error
		}, 1)
		go func() {
			res, err := f.Run(ctx, Command{Path: "/bin/helm", Args: []string{"install"}})
			done <- struct {
				res Result
				err error
			}{res, err}
		}()

		time.Sleep(50 * time.Millisecond)
		cancel()

		select {
		case got := <-done:
			if !errors.Is(got.err, context.Canceled) {
				t.Errorf("error = %v, want context.Canceled", got.err)
			}
			// Matching Exec here is what lets the layers above treat both
			// runners identically.
			if !got.res.Signalled {
				t.Error("Signalled = false; must match Exec's cancellation semantics")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Run did not return after cancellation")
		}
	})

	t.Run("Delay applies to every command", func(t *testing.T) {
		f := NewFake()
		f.Delay = 80 * time.Millisecond

		start := time.Now()
		if _, err := f.Run(ctx, Command{Path: "/bin/kubectl"}); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if elapsed := time.Since(start); elapsed < 60*time.Millisecond {
			t.Errorf("returned in %v; Delay was not applied", elapsed)
		}
	})

	t.Run("nil writers are safe", func(t *testing.T) {
		f := NewFake().WhenArgsContainStderr("x", "out", "err", 0)
		if _, err := f.Run(ctx, Command{Path: "/bin/x"}); err != nil {
			t.Fatalf("Run: %v", err)
		}
	})
}

func TestFakeAssertions(t *testing.T) {
	ctx := context.Background()
	f := NewFake()
	for _, args := range [][]string{{"apply", "-f", "a.yaml"}, {"apply", "-f", "b.yaml"}, {"get", "pods"}} {
		if _, err := f.Run(ctx, Command{Path: "/bin/kubectl", Args: args}); err != nil {
			t.Fatalf("Run: %v", err)
		}
	}

	t.Run("Called", func(t *testing.T) {
		if !f.Called("apply") {
			t.Error("Called(apply) = false")
		}
		if f.Called("delete") {
			t.Error("Called(delete) = true; nothing was deleted")
		}
	})

	t.Run("CallCount", func(t *testing.T) {
		tests := []struct {
			substr string
			want   int
		}{
			{"", 3},
			{"apply", 2},
			{"get", 1},
			{"delete", 0},
		}
		for _, tt := range tests {
			if got := f.CallCount(tt.substr); got != tt.want {
				t.Errorf("CallCount(%q) = %d, want %d", tt.substr, got, tt.want)
			}
		}
	})

	t.Run("Calls returns a copy", func(t *testing.T) {
		calls := f.Calls()
		calls[0].Path = "mutated"
		if f.Calls()[0].Path == "mutated" {
			t.Error("Calls() exposed internal state to mutation")
		}
	})

	t.Run("Reset clears calls but keeps rules", func(t *testing.T) {
		g := NewFake().WhenArgsContain("version", "v1.2.3\n", 0)
		if _, err := g.Run(ctx, Command{Path: "/bin/helm", Args: []string{"version"}}); err != nil {
			t.Fatalf("Run: %v", err)
		}
		g.Reset()
		if n := g.CallCount(""); n != 0 {
			t.Errorf("CallCount after Reset = %d, want 0", n)
		}

		var stdout bytes.Buffer
		if _, err := g.Run(ctx, Command{Path: "/bin/helm", Args: []string{"version"}, Stdout: &stdout}); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if strings.TrimSpace(stdout.String()) != "v1.2.3" {
			t.Error("Reset discarded the scripted rules as well as the calls")
		}
	})
}

func TestFakeLookPath(t *testing.T) {
	t.Run("resolves anything by default", func(t *testing.T) {
		f := NewFake()
		got, err := f.LookPath("helm")
		if err != nil {
			t.Fatalf("LookPath: %v", err)
		}
		if got == "" {
			t.Error("LookPath returned an empty path")
		}
	})

	t.Run("Available restricts what resolves", func(t *testing.T) {
		f := NewFake()
		f.Available = map[string]string{"kubectl": "/usr/local/bin/kubectl"}

		if got, err := f.LookPath("kubectl"); err != nil || got != "/usr/local/bin/kubectl" {
			t.Errorf("LookPath(kubectl) = %q, %v", got, err)
		}
		if _, err := f.LookPath("helm"); err == nil {
			t.Error("LookPath(helm) should fail when it is not in Available")
		}
	})

	t.Run("LookPathErr fails everything", func(t *testing.T) {
		want := errors.New("no PATH")
		f := NewFake()
		f.LookPathErr = want
		if _, err := f.LookPath("kubectl"); !errors.Is(err, want) {
			t.Errorf("error = %v, want %v", err, want)
		}
	})
}

// TestFakeConcurrentUse guards the Fake against data races, since services
// under test will call it from several goroutines.
func TestFakeConcurrentUse(t *testing.T) {
	ctx := context.Background()
	f := NewFake().WhenArgsContain("get", "ok\n", 0)

	done := make(chan struct{})
	for i := range 8 {
		go func() {
			defer func() { done <- struct{}{} }()
			for range 20 {
				_, _ = f.Run(ctx, Command{Path: "/bin/kubectl", Args: []string{"get", "pods"}})
				_ = f.CallCount("get")
				_ = f.Called("get")
			}
			_ = i
		}()
	}
	for range 8 {
		<-done
	}

	if got := f.CallCount("get"); got != 160 {
		t.Errorf("CallCount = %d, want 160", got)
	}
}
