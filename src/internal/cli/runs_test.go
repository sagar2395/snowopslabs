// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sagar2395/snowopslabs/internal/store"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func intPtr(i int) *int { return &i }

func TestWriteRunTable(t *testing.T) {
	t.Run("an empty history says so rather than printing a bare header", func(t *testing.T) {
		var out bytes.Buffer
		if err := writeRunTable(&out, nil); err != nil {
			t.Fatalf("writeRunTable: %v", err)
		}
		if !strings.Contains(out.String(), "No runs recorded yet") {
			t.Errorf("output = %q", out.String())
		}
	})

	t.Run("renders one row per run", func(t *testing.T) {
		runs := []store.Run{
			{
				ID: "run_1", Kind: "platform.install", Target: "ingress/traefik",
				Status: store.StatusSucceeded, StartedAt: time.Now().Add(-2 * time.Minute),
				Duration: 90 * time.Second,
			},
			{
				ID: "run_2", Kind: "scenario.verify",
				Status: store.StatusFailed, StartedAt: time.Now().Add(-time.Hour),
				Duration: 3 * time.Second,
			},
		}

		var out bytes.Buffer
		if err := writeRunTable(&out, runs); err != nil {
			t.Fatalf("writeRunTable: %v", err)
		}
		text := out.String()
		for _, want := range []string{"ID", "KIND", "STATUS", "run_1", "platform.install", "ingress/traefik", "run_2", "failed"} {
			if !strings.Contains(text, want) {
				t.Errorf("table should contain %q:\n%s", want, text)
			}
		}
		// A run with no target must show a placeholder, not an empty column.
		if !strings.Contains(text, "-") {
			t.Errorf("an empty target should render as a dash:\n%s", text)
		}
	})
}

func TestStreamRunLogs(t *testing.T) {
	ctx := context.Background()

	t.Run("prints the transcript with stream markers", func(t *testing.T) {
		st := newTestStore(t)
		if err := st.CreateRun(ctx, store.Run{ID: "r", Kind: "test", Target: "thing"}); err != nil {
			t.Fatalf("CreateRun: %v", err)
		}
		if _, err := st.AppendLogs(ctx, "r", []store.LogLine{
			{Stream: store.StreamStdout, Text: "normal output"},
			{Stream: store.StreamStderr, Text: "a warning"},
			{Stream: store.StreamSystem, Text: "cancelled by user"},
		}); err != nil {
			t.Fatalf("AppendLogs: %v", err)
		}
		if err := st.FinishRun(ctx, "r", store.StatusCancelled, nil, "cancelled by user", time.Now(), time.Second); err != nil {
			t.Fatalf("FinishRun: %v", err)
		}

		var out bytes.Buffer
		if err := streamRunLogs(ctx, &out, st, "r", false); err != nil {
			t.Fatalf("streamRunLogs: %v", err)
		}
		text := out.String()

		if !strings.Contains(text, "normal output") {
			t.Errorf("stdout missing:\n%s", text)
		}
		// stderr and engine-authored lines must be distinguishable from
		// ordinary output when scanning a long transcript.
		if !strings.Contains(text, "! a warning") {
			t.Errorf("stderr should be marked:\n%s", text)
		}
		if !strings.Contains(text, "* cancelled by user") {
			t.Errorf("system lines should be marked:\n%s", text)
		}
		if !strings.Contains(text, "Cancelled.") {
			t.Errorf("the summary should state the outcome:\n%s", text)
		}
	})

	t.Run("summarises each terminal outcome", func(t *testing.T) {
		tests := []struct {
			name     string
			status   store.Status
			exitCode *int
			errText  string
			want     string
		}{
			{"succeeded", store.StatusSucceeded, intPtr(0), "", "Succeeded in"},
			{"failed", store.StatusFailed, intPtr(2), "boom", "exit 2"},
			{"cancelled", store.StatusCancelled, nil, "cancelled by user", "Cancelled."},
			{"timed out", store.StatusTimedOut, nil, "timed out after 5m0s", "Timed out"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				st := newTestStore(t)
				if err := st.CreateRun(ctx, store.Run{ID: "r", Kind: "test"}); err != nil {
					t.Fatalf("CreateRun: %v", err)
				}
				if err := st.FinishRun(ctx, "r", tt.status, tt.exitCode, tt.errText, time.Now(), 2*time.Second); err != nil {
					t.Fatalf("FinishRun: %v", err)
				}

				var out bytes.Buffer
				if err := streamRunLogs(ctx, &out, st, "r", false); err != nil {
					t.Fatalf("streamRunLogs: %v", err)
				}
				if !strings.Contains(out.String(), tt.want) {
					t.Errorf("summary should contain %q:\n%s", tt.want, out.String())
				}
			})
		}
	})

	t.Run("reports an unknown run", func(t *testing.T) {
		st := newTestStore(t)
		var out bytes.Buffer
		if err := streamRunLogs(ctx, &out, st, "ghost", false); err == nil {
			t.Fatal("expected an error for an unknown run")
		}
	})

	t.Run("a run with no output still prints its header and summary", func(t *testing.T) {
		st := newTestStore(t)
		if err := st.CreateRun(ctx, store.Run{ID: "r", Kind: "test"}); err != nil {
			t.Fatalf("CreateRun: %v", err)
		}
		if err := st.FinishRun(ctx, "r", store.StatusSucceeded, intPtr(0), "", time.Now(), time.Second); err != nil {
			t.Fatalf("FinishRun: %v", err)
		}

		var out bytes.Buffer
		if err := streamRunLogs(ctx, &out, st, "r", false); err != nil {
			t.Fatalf("streamRunLogs: %v", err)
		}
		if !strings.Contains(out.String(), "Succeeded") {
			t.Errorf("output = %q", out.String())
		}
	})

	t.Run("follow stops once the run reaches a terminal state", func(t *testing.T) {
		st := newTestStore(t)
		if err := st.CreateRun(ctx, store.Run{ID: "r", Kind: "test"}); err != nil {
			t.Fatalf("CreateRun: %v", err)
		}
		if _, err := st.AppendLogs(ctx, "r", []store.LogLine{{Text: "line"}}); err != nil {
			t.Fatalf("AppendLogs: %v", err)
		}
		if err := st.FinishRun(ctx, "r", store.StatusSucceeded, intPtr(0), "", time.Now(), time.Second); err != nil {
			t.Fatalf("FinishRun: %v", err)
		}

		done := make(chan error, 1)
		var out bytes.Buffer
		go func() { done <- streamRunLogs(ctx, &out, st, "r", true) }()

		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("streamRunLogs: %v", err)
			}
			if !strings.Contains(out.String(), "line") {
				t.Errorf("output = %q", out.String())
			}
		case <-time.After(5 * time.Second):
			t.Fatal("follow did not stop after the run finished")
		}
	})

	t.Run("follow honours a cancelled context on an unfinished run", func(t *testing.T) {
		st := newTestStore(t)
		if err := st.CreateRun(ctx, store.Run{ID: "r", Kind: "test"}); err != nil {
			t.Fatalf("CreateRun: %v", err)
		}
		if err := st.StartRun(ctx, "r", time.Now()); err != nil {
			t.Fatalf("StartRun: %v", err)
		}

		followCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
		defer cancel()

		var out bytes.Buffer
		err := streamRunLogs(followCtx, &out, st, "r", true)
		if err == nil {
			t.Fatal("expected the follow loop to stop on context cancellation")
		}
	})
}

func TestRelativeTime(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name string
		at   time.Time
		want string
	}{
		{"zero renders as a dash", time.Time{}, "-"},
		{"seconds", now.Add(-30 * time.Second), "s ago"},
		{"minutes", now.Add(-5 * time.Minute), "m ago"},
		{"hours", now.Add(-3 * time.Hour), "h ago"},
		{"days", now.Add(-50 * time.Hour), "d ago"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := relativeTime(tt.at); !strings.Contains(got, tt.want) {
				t.Errorf("relativeTime() = %q, want it to contain %q", got, tt.want)
			}
		})
	}
}

func TestDurationColumn(t *testing.T) {
	tests := []struct {
		name string
		run  store.Run
		want string
	}{
		{"finished run shows its duration", store.Run{Duration: 90 * time.Second}, "1m30s"},
		{"queued run shows a dash", store.Run{Status: store.StatusQueued}, "-"},
		{
			"running run shows elapsed time with an ellipsis",
			store.Run{Status: store.StatusRunning, StartedAt: time.Now().Add(-10 * time.Second)},
			"…",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := duration(tt.run); !strings.Contains(got, tt.want) {
				t.Errorf("duration() = %q, want it to contain %q", got, tt.want)
			}
		})
	}
}

func TestDash(t *testing.T) {
	if got := dash(""); got != "-" {
		t.Errorf("dash(\"\") = %q, want -", got)
	}
	if got := dash("value"); got != "value" {
		t.Errorf("dash(value) = %q", got)
	}
}

// TestRunsCommandsWiring checks the command tree exists with the flags the
// docs and runbooks reference.
func TestRunsCommandsWiring(t *testing.T) {
	cmd := runsCmd()

	subs := map[string]bool{}
	for _, c := range cmd.Commands() {
		subs[c.Name()] = true
	}
	for _, want := range []string{"list", "logs", "cancel"} {
		if !subs[want] {
			t.Errorf("runs is missing the %q subcommand", want)
		}
	}

	t.Run("list has its filter flags", func(t *testing.T) {
		list := runsListCmd()
		for _, flag := range []string{"limit", "status", "kind"} {
			if list.Flags().Lookup(flag) == nil {
				t.Errorf("runs list is missing --%s", flag)
			}
		}
	})

	t.Run("logs has --follow", func(t *testing.T) {
		if runsLogsCmd().Flags().Lookup("follow") == nil {
			t.Error("runs logs is missing --follow")
		}
	})

	t.Run("list rejects an unknown status", func(t *testing.T) {
		st := newTestStore(t)
		original := openStore
		openStore = func(context.Context) (*store.Store, error) { return st, nil }
		t.Cleanup(func() { openStore = original })

		list := runsListCmd()
		list.SetArgs([]string{"--status", "bogus"})
		list.SetOut(&bytes.Buffer{})
		list.SetErr(&bytes.Buffer{})

		err := list.Execute()
		if err == nil {
			t.Fatal("expected an error for an unknown status")
		}
		// The message must list the valid values rather than just refusing.
		if !strings.Contains(err.Error(), "queued") {
			t.Errorf("error = %q, want it to list valid statuses", err)
		}
	})
}
