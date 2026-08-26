// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func seedRun(t *testing.T, s *Store, id string) {
	t.Helper()
	if err := s.CreateRun(context.Background(), Run{ID: id, Kind: "test"}); err != nil {
		t.Fatalf("CreateRun(%s): %v", id, err)
	}
}

func TestAppendLogs(t *testing.T) {
	ctx := context.Background()

	t.Run("assigns monotonic sequence numbers across calls", func(t *testing.T) {
		s := newStore(t)
		seedRun(t, s, "r")

		last, err := s.AppendLogs(ctx, "r", []LogLine{{Text: "one"}, {Text: "two"}})
		if err != nil {
			t.Fatalf("AppendLogs: %v", err)
		}
		if last != 2 {
			t.Errorf("last seq = %d, want 2", last)
		}

		last, err = s.AppendLogs(ctx, "r", []LogLine{{Text: "three"}})
		if err != nil {
			t.Fatalf("second AppendLogs: %v", err)
		}
		if last != 3 {
			t.Errorf("last seq = %d, want 3", last)
		}

		lines, err := s.ReadLogs(ctx, "r", 0, 10)
		if err != nil {
			t.Fatalf("ReadLogs: %v", err)
		}
		var got []string
		for i, l := range lines {
			if l.Seq != int64(i+1) {
				t.Errorf("line %d: seq = %d, want %d", i, l.Seq, i+1)
			}
			got = append(got, l.Text)
		}
		if strings.Join(got, ",") != "one,two,three" {
			t.Errorf("lines = %v, want one,two,three", got)
		}
	})

	t.Run("sequences are per run, not global", func(t *testing.T) {
		s := newStore(t)
		seedRun(t, s, "a")
		seedRun(t, s, "b")

		if _, err := s.AppendLogs(ctx, "a", []LogLine{{Text: "a1"}, {Text: "a2"}}); err != nil {
			t.Fatalf("AppendLogs(a): %v", err)
		}
		last, err := s.AppendLogs(ctx, "b", []LogLine{{Text: "b1"}})
		if err != nil {
			t.Fatalf("AppendLogs(b): %v", err)
		}
		if last != 1 {
			t.Errorf("run b's first seq = %d, want 1 — sequences must not be global", last)
		}
	})

	t.Run("defaults the stream to stdout and the time to the clock", func(t *testing.T) {
		fixed := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
		s := newStore(t, WithClock(func() time.Time { return fixed }))
		seedRun(t, s, "r")

		if _, err := s.AppendLogs(ctx, "r", []LogLine{{Text: "no stream set"}}); err != nil {
			t.Fatalf("AppendLogs: %v", err)
		}
		lines, _ := s.ReadLogs(ctx, "r", 0, 10)
		if lines[0].Stream != StreamStdout {
			t.Errorf("stream = %q, want stdout", lines[0].Stream)
		}
		if !lines[0].At.Equal(fixed) {
			t.Errorf("timestamp = %v, want %v", lines[0].At, fixed)
		}
	})

	t.Run("preserves each stream", func(t *testing.T) {
		s := newStore(t)
		seedRun(t, s, "r")
		want := []Stream{StreamStdout, StreamStderr, StreamSystem}
		var lines []LogLine
		for _, st := range want {
			lines = append(lines, LogLine{Stream: st, Text: string(st)})
		}
		if _, err := s.AppendLogs(ctx, "r", lines); err != nil {
			t.Fatalf("AppendLogs: %v", err)
		}
		got, _ := s.ReadLogs(ctx, "r", 0, 10)
		for i, l := range got {
			if l.Stream != want[i] {
				t.Errorf("line %d: stream = %q, want %q", i, l.Stream, want[i])
			}
		}
	})

	t.Run("truncates an oversized line rather than storing it whole", func(t *testing.T) {
		s := newStore(t)
		seedRun(t, s, "r")
		huge := strings.Repeat("x", maxLogLine*2)
		if _, err := s.AppendLogs(ctx, "r", []LogLine{{Text: huge}}); err != nil {
			t.Fatalf("AppendLogs: %v", err)
		}
		lines, _ := s.ReadLogs(ctx, "r", 0, 10)
		if len(lines[0].Text) >= len(huge) {
			t.Errorf("line was not truncated: %d bytes", len(lines[0].Text))
		}
		if !strings.Contains(lines[0].Text, "truncated") {
			t.Error("a truncated line must say so, or the user thinks output was lost")
		}
	})

	t.Run("an empty batch returns the current cursor without writing", func(t *testing.T) {
		s := newStore(t)
		seedRun(t, s, "r")
		if _, err := s.AppendLogs(ctx, "r", []LogLine{{Text: "one"}}); err != nil {
			t.Fatalf("AppendLogs: %v", err)
		}
		last, err := s.AppendLogs(ctx, "r", nil)
		if err != nil {
			t.Fatalf("AppendLogs(nil): %v", err)
		}
		if last != 1 {
			t.Errorf("cursor = %d, want 1", last)
		}
	})

	t.Run("rejects logs for a run that does not exist", func(t *testing.T) {
		// The foreign key is what stops orphaned logs accumulating invisibly.
		s := newStore(t)
		if _, err := s.AppendLogs(ctx, "ghost", []LogLine{{Text: "x"}}); err == nil {
			t.Fatal("expected a foreign-key error for an unknown run")
		}
	})

	t.Run("honours a cancelled context", func(t *testing.T) {
		s := newStore(t)
		seedRun(t, s, "r")
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := s.AppendLogs(cancelled, "r", []LogLine{{Text: "x"}}); err == nil {
			t.Fatal("expected an error on a cancelled context")
		}
	})
}

// TestReadLogs_CursorResume is the property that makes reconnection safe: a
// client that reconnects with the last sequence it saw gets every line it
// missed, exactly once. v1 dropped events for slow clients with no way to tell.
func TestReadLogs_CursorResume(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seedRun(t, s, "r")

	var all []LogLine
	for i := range 100 {
		all = append(all, LogLine{Text: fmt.Sprintf("line-%03d", i)})
	}
	if _, err := s.AppendLogs(ctx, "r", all); err != nil {
		t.Fatalf("AppendLogs: %v", err)
	}

	// Read in pages, as a streaming client would, "disconnecting" between each.
	var seen []string
	cursor := int64(0)
	for {
		page, err := s.ReadLogs(ctx, "r", cursor, 7)
		if err != nil {
			t.Fatalf("ReadLogs: %v", err)
		}
		if len(page) == 0 {
			break
		}
		for _, l := range page {
			if l.Seq != cursor+1 {
				t.Fatalf("gap or duplicate: got seq %d, expected %d", l.Seq, cursor+1)
			}
			cursor = l.Seq
			seen = append(seen, l.Text)
		}
	}

	if len(seen) != len(all) {
		t.Fatalf("read %d lines, want %d", len(seen), len(all))
	}
	for i, text := range seen {
		if want := fmt.Sprintf("line-%03d", i); text != want {
			t.Fatalf("line %d = %q, want %q", i, text, want)
		}
	}
}

func TestReadLogs(t *testing.T) {
	ctx := context.Background()

	t.Run("an unknown run reads empty, not an error", func(t *testing.T) {
		// A client may open a stream before the first line lands.
		s := newStore(t)
		lines, err := s.ReadLogs(ctx, "ghost", 0, 10)
		if err != nil {
			t.Fatalf("ReadLogs: %v", err)
		}
		if len(lines) != 0 {
			t.Errorf("got %d lines, want 0", len(lines))
		}
	})

	t.Run("a cursor past the end reads empty", func(t *testing.T) {
		s := newStore(t)
		seedRun(t, s, "r")
		if _, err := s.AppendLogs(ctx, "r", []LogLine{{Text: "one"}}); err != nil {
			t.Fatalf("AppendLogs: %v", err)
		}
		lines, err := s.ReadLogs(ctx, "r", 99, 10)
		if err != nil {
			t.Fatalf("ReadLogs: %v", err)
		}
		if len(lines) != 0 {
			t.Errorf("got %d lines, want 0", len(lines))
		}
	})

	t.Run("limit caps the page", func(t *testing.T) {
		s := newStore(t)
		seedRun(t, s, "r")
		var lines []LogLine
		for i := range 10 {
			lines = append(lines, LogLine{Text: fmt.Sprint(i)})
		}
		if _, err := s.AppendLogs(ctx, "r", lines); err != nil {
			t.Fatalf("AppendLogs: %v", err)
		}
		got, err := s.ReadLogs(ctx, "r", 0, 3)
		if err != nil {
			t.Fatalf("ReadLogs: %v", err)
		}
		if len(got) != 3 {
			t.Errorf("got %d lines, want 3", len(got))
		}
	})
}

func TestLastLogSeq(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seedRun(t, s, "r")

	if seq, err := s.LastLogSeq(ctx, "r"); err != nil || seq != 0 {
		t.Errorf("empty run: seq = %d, err = %v; want 0, nil", seq, err)
	}
	if _, err := s.AppendLogs(ctx, "r", []LogLine{{Text: "a"}, {Text: "b"}}); err != nil {
		t.Fatalf("AppendLogs: %v", err)
	}
	if seq, err := s.LastLogSeq(ctx, "r"); err != nil || seq != 2 {
		t.Errorf("seq = %d, err = %v; want 2, nil", seq, err)
	}
}

func TestSteps(t *testing.T) {
	ctx := context.Background()

	t.Run("starting a step closes the previous one", func(t *testing.T) {
		// Scripts announce the step they are entering; they do not announce
		// the end of the one before.
		s := newStore(t)
		seedRun(t, s, "r")
		base := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)

		if _, err := s.StartStep(ctx, "r", "install-prometheus", base); err != nil {
			t.Fatalf("StartStep: %v", err)
		}
		if _, err := s.StartStep(ctx, "r", "install-grafana", base.Add(time.Minute)); err != nil {
			t.Fatalf("StartStep: %v", err)
		}

		steps, err := s.ListSteps(ctx, "r")
		if err != nil {
			t.Fatalf("ListSteps: %v", err)
		}
		if len(steps) != 2 {
			t.Fatalf("got %d steps, want 2", len(steps))
		}
		if steps[0].Status != StepSucceeded {
			t.Errorf("first step status = %q, want succeeded", steps[0].Status)
		}
		if !steps[0].EndedAt.Equal(base.Add(time.Minute)) {
			t.Errorf("first step ended at %v, want the second step's start", steps[0].EndedAt)
		}
		if steps[1].Status != StepRunning {
			t.Errorf("second step status = %q, want running", steps[1].Status)
		}
	})

	t.Run("indices increment from zero", func(t *testing.T) {
		s := newStore(t)
		seedRun(t, s, "r")
		for i, name := range []string{"a", "b", "c"} {
			idx, err := s.StartStep(ctx, "r", name, time.Now())
			if err != nil {
				t.Fatalf("StartStep(%s): %v", name, err)
			}
			if idx != i {
				t.Errorf("step %s: index = %d, want %d", name, idx, i)
			}
		}
	})

	t.Run("FinishSteps records where an interrupted run died", func(t *testing.T) {
		s := newStore(t)
		seedRun(t, s, "r")
		if _, err := s.StartStep(ctx, "r", "install-kafka", time.Now()); err != nil {
			t.Fatalf("StartStep: %v", err)
		}
		if err := s.FinishSteps(ctx, "r", StepFailed, time.Now()); err != nil {
			t.Fatalf("FinishSteps: %v", err)
		}
		steps, _ := s.ListSteps(ctx, "r")
		if steps[0].Status != StepFailed {
			t.Errorf("status = %q, want failed", steps[0].Status)
		}
		if steps[0].EndedAt.IsZero() {
			t.Error("a finished step must have an end time")
		}
	})

	t.Run("FinishSteps leaves already-closed steps alone", func(t *testing.T) {
		s := newStore(t)
		seedRun(t, s, "r")
		base := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
		if _, err := s.StartStep(ctx, "r", "first", base); err != nil {
			t.Fatalf("StartStep: %v", err)
		}
		if _, err := s.StartStep(ctx, "r", "second", base.Add(time.Minute)); err != nil {
			t.Fatalf("StartStep: %v", err)
		}
		if err := s.FinishSteps(ctx, "r", StepFailed, base.Add(2*time.Minute)); err != nil {
			t.Fatalf("FinishSteps: %v", err)
		}

		steps, _ := s.ListSteps(ctx, "r")
		if steps[0].Status != StepSucceeded {
			t.Errorf("the completed step was rewritten to %q", steps[0].Status)
		}
		if steps[1].Status != StepFailed {
			t.Errorf("the open step should be failed, got %q", steps[1].Status)
		}
	})

	t.Run("a run with no steps lists empty", func(t *testing.T) {
		s := newStore(t)
		seedRun(t, s, "r")
		steps, err := s.ListSteps(ctx, "r")
		if err != nil {
			t.Fatalf("ListSteps: %v", err)
		}
		if len(steps) != 0 {
			t.Errorf("got %d steps, want 0", len(steps))
		}
	})

	t.Run("FinishSteps on a run with no steps is a no-op", func(t *testing.T) {
		s := newStore(t)
		seedRun(t, s, "r")
		if err := s.FinishSteps(ctx, "r", StepSucceeded, time.Now()); err != nil {
			t.Errorf("FinishSteps: %v", err)
		}
	})
}

func TestAudit(t *testing.T) {
	ctx := context.Background()

	t.Run("round-trips an entry", func(t *testing.T) {
		s := newStore(t)
		at := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
		want := AuditEntry{
			At: at, Actor: "sagar", Action: "platform.install",
			Target: "ingress/traefik", RunID: "run-1", Source: "cli",
			Detail: "via labctl platform up",
		}
		if err := s.Audit(ctx, want); err != nil {
			t.Fatalf("Audit: %v", err)
		}

		got, err := s.ListAudit(ctx, 10)
		if err != nil {
			t.Fatalf("ListAudit: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("got %d entries, want 1", len(got))
		}
		if got[0] != want {
			t.Errorf("got %+v, want %+v", got[0], want)
		}
	})

	t.Run("requires an action", func(t *testing.T) {
		s := newStore(t)
		err := s.Audit(ctx, AuditEntry{Actor: "sagar"})
		if err == nil || !strings.Contains(err.Error(), "action is required") {
			t.Fatalf("error = %v, want a missing-action complaint", err)
		}
	})

	t.Run("outlives the run it references", func(t *testing.T) {
		// run_id is deliberately not a foreign key: pruning run history must
		// not erase the record that something happened.
		s := newStore(t)
		base := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
		seedRun(t, s, "r")
		if err := s.FinishRun(ctx, "r", StatusSucceeded, intPtr(0), "", base, time.Second); err != nil {
			t.Fatalf("FinishRun: %v", err)
		}
		if err := s.Audit(ctx, AuditEntry{Action: "lab.up", RunID: "r"}); err != nil {
			t.Fatalf("Audit: %v", err)
		}

		if _, err := s.PruneRuns(ctx, base.Add(time.Hour)); err != nil {
			t.Fatalf("PruneRuns: %v", err)
		}

		got, err := s.ListAudit(ctx, 10)
		if err != nil {
			t.Fatalf("ListAudit: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("audit entry was deleted with its run: got %d entries", len(got))
		}
		if got[0].RunID != "r" {
			t.Errorf("runID = %q, want it preserved", got[0].RunID)
		}
	})

	t.Run("lists newest first and honours the limit", func(t *testing.T) {
		s := newStore(t)
		base := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
		for i := range 5 {
			if err := s.Audit(ctx, AuditEntry{
				At:     base.Add(time.Duration(i) * time.Minute),
				Action: fmt.Sprintf("action-%d", i),
			}); err != nil {
				t.Fatalf("Audit: %v", err)
			}
		}
		got, err := s.ListAudit(ctx, 2)
		if err != nil {
			t.Fatalf("ListAudit: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d entries, want 2", len(got))
		}
		if got[0].Action != "action-4" || got[1].Action != "action-3" {
			t.Errorf("order wrong: %s, %s", got[0].Action, got[1].Action)
		}
	})

	t.Run("an empty log lists cleanly", func(t *testing.T) {
		s := newStore(t)
		got, err := s.ListAudit(ctx, 10)
		if err != nil {
			t.Fatalf("ListAudit: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %d entries, want 0", len(got))
		}
	})
}
