// SPDX-License-Identifier: Apache-2.0

package incident

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sagar2395/snowopslabs/internal/executor"
	"github.com/sagar2395/snowopslabs/internal/results"
)

func TestParseHints(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "hints.md"), []byte(`# Hints - something

Preamble text that is not a hint.

## Hint 1
First nudge.

## Hint 2
Second nudge,
on two lines.

## Hint 3
Near-answer.
`), 0644)

	hints, err := ParseHints(dir)
	if err != nil {
		t.Fatalf("ParseHints: %v", err)
	}
	if len(hints) != 3 {
		t.Fatalf("expected 3 hints, got %d: %v", len(hints), hints)
	}
	if hints[0] != "First nudge." {
		t.Errorf("hint 1 = %q", hints[0])
	}
	if !strings.Contains(hints[1], "two lines") {
		t.Errorf("hint 2 = %q", hints[1])
	}

	os.WriteFile(filepath.Join(dir, "hints.md"), []byte("just prose, no headings"), 0644)
	if _, err := ParseHints(dir); err == nil {
		t.Fatal("hints.md without sections must error")
	}
}

func TestNextHint_ProgressionAndPersistence(t *testing.T) {
	e, root := testEngine(t, "fault-a")
	ex := executor.New(root)
	if _, err := e.Inject("fault-a", ex, false, false); err != nil {
		t.Fatal(err)
	}

	for i := 1; i <= 3; i++ {
		h, err := e.NextHint()
		if err != nil {
			t.Fatalf("hint %d: %v", i, err)
		}
		if h.Index != i || h.Total != 3 {
			t.Fatalf("hint %d: got index %d/%d", i, h.Index, h.Total)
		}
	}
	if _, err := e.NextHint(); !errors.Is(err, ErrNoMoreHints) {
		t.Fatalf("expected ErrNoMoreHints, got %v", err)
	}

	// Reveals survive an engine restart (state is on disk).
	e2 := NewEngine(root, "k3d.local")
	if _, err := e2.NextHint(); !errors.Is(err, ErrNoMoreHints) {
		t.Fatalf("hint state must persist across restarts, got %v", err)
	}
	active, _ := e2.Active()
	if active.HintsRevealed != 3 {
		t.Fatalf("HintsRevealed = %d, want 3", active.HintsRevealed)
	}
}

func TestNextHint_NoActive(t *testing.T) {
	e, _ := testEngine(t, "fault-a")
	if _, err := e.NextHint(); !errors.Is(err, ErrNoActive) {
		t.Fatalf("expected ErrNoActive, got %v", err)
	}
}

func TestSolution(t *testing.T) {
	e, root := testEngine(t, "fault-a")
	ex := executor.New(root)

	// By explicit name, without an active incident.
	text, err := e.Solution("fault-a")
	if err != nil || !strings.Contains(text, "Remove the marker") {
		t.Fatalf("Solution by name: %v %q", err, text)
	}

	// Bare call requires an active incident.
	if _, err := e.Solution(""); !errors.Is(err, ErrNoActive) {
		t.Fatalf("expected ErrNoActive, got %v", err)
	}
	if _, err := e.Inject("fault-a", ex, false, false); err != nil {
		t.Fatal(err)
	}
	if text, err := e.Solution(""); err != nil || text == "" {
		t.Fatalf("Solution with active: %v", err)
	}
}

func TestHistory_ManualResolutionRecordsMTTRAndHints(t *testing.T) {
	e, root := testEngine(t, "fault-a")
	ex := executor.New(root)
	ctx := context.Background()

	if _, err := e.Inject("fault-a", ex, false, false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Status(ctx, testRunner(), ""); err != nil { // first check: sets detect time
		t.Fatal(err)
	}
	if _, err := e.NextHint(); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(root, "incidents", "fault-a", "BROKEN")) // manual fix
	res, err := e.Status(ctx, testRunner(), "")
	if err != nil || !res.Resolved {
		t.Fatalf("expected resolution: %v %+v", err, res)
	}

	recs, err := e.History()
	if err != nil || len(recs) != 1 {
		t.Fatalf("history: %v, %d records", err, len(recs))
	}
	rec := recs[0]
	if rec.Fault != "fault-a" || rec.ResolvedBy != "manual" || rec.HintsUsed != 1 {
		t.Fatalf("record wrong: %+v", rec)
	}
	if rec.InjectedAt.IsZero() || rec.ResolvedAt.IsZero() || rec.FirstCheckedAt.IsZero() {
		t.Fatalf("timestamps missing: %+v", rec)
	}
	if rec.ResolveSeconds < 0 || rec.DetectSeconds < 0 {
		t.Fatalf("negative durations: %+v", rec)
	}

	// History survives engine restarts and accumulates.
	e2 := NewEngine(root, "k3d.local")
	if _, err := e2.Inject("fault-a", ex, false, true); err != nil {
		t.Fatal(err)
	}
	if _, err := e2.Resolve("", ex, ""); err != nil { // escape hatch
		t.Fatal(err)
	}
	recs, _ = e2.History()
	if len(recs) != 2 {
		t.Fatalf("expected 2 records, got %d", len(recs))
	}
	if recs[1].ResolvedBy != "auto" || !recs[1].Silent {
		t.Fatalf("second record wrong: %+v", recs[1])
	}
}

func TestHistory_ExplicitResolveWithoutActiveRecordsNothing(t *testing.T) {
	e, root := testEngine(t, "fault-a")
	if _, err := e.Resolve("fault-a", executor.New(root), ""); err != nil {
		t.Fatal(err)
	}
	recs, _ := e.History()
	if len(recs) != 0 {
		t.Fatalf("resolve without an active run must not fabricate history: %+v", recs)
	}
}

func TestHistory_SkipsCorruptLines(t *testing.T) {
	e, root := testEngine(t, "fault-a")
	dir := filepath.Join(root, ".labctl", "history")
	os.MkdirAll(dir, 0755)
	// Write one valid unified record and one corrupt line.
	os.WriteFile(filepath.Join(dir, "results.jsonl"),
		[]byte(`{"kind":"incident","name":"fault-a","startedAt":"2026-01-01T00:00:00Z","endedAt":"2026-01-01T00:05:00Z","elapsedSeconds":300,"score":-1,"outcome":"resolved"}`+"\nnot-json\n"), 0644)

	recs, err := e.History()
	if err != nil || len(recs) != 1 {
		t.Fatalf("corrupt lines must be skipped: %v, %d records", err, len(recs))
	}
}

// The outcome an incident run records is what `labctl results export` and the
// result card read to tell a fix from the escape hatch.
func TestHistory_OutcomeTellsAFixFromTheEscapeHatch(t *testing.T) {
	tests := []struct {
		name           string
		end            func(t *testing.T, e *Engine, root string)
		wantOutcome    string
		wantResolvedBy string
	}{
		{"the detection check passes after a fix", func(t *testing.T, e *Engine, root string) {
			os.Remove(filepath.Join(root, "incidents", "fault-a", "BROKEN"))
			if res, err := e.Status(context.Background(), testRunner(), ""); err != nil || !res.Resolved {
				t.Fatalf("status after the fix: %v %+v", err, res)
			}
		}, results.OutcomeResolved, results.ResolvedByFix},
		{"the escape hatch puts the lab back", func(t *testing.T, e *Engine, root string) {
			if _, err := e.Resolve("", executor.New(root), ""); err != nil {
				t.Fatal(err)
			}
		}, results.OutcomeAutoResolved, results.ResolvedByEscapeHatch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, root := testEngine(t, "fault-a")
			if _, err := e.Inject("fault-a", executor.New(root), false, false); err != nil {
				t.Fatal(err)
			}
			active, err := e.Active()
			if err != nil || active.App == "" {
				t.Fatalf("active incident = %+v, %v; want it to name its app", active, err)
			}
			tt.end(t, e, root)
			recs, err := e.resultsStore().ByKind(results.KindIncident)
			if err != nil || len(recs) != 1 {
				t.Fatalf("results: %v, %d records", err, len(recs))
			}
			rows := results.Export(recs, results.ExportFilter{})
			if recs[0].Workload != active.App {
				t.Errorf("result workload = %q, want the app the incident ran against, %q", recs[0].Workload, active.App)
			}
			if recs[0].Outcome != tt.wantOutcome || rows[0].ResolvedBy != tt.wantResolvedBy {
				t.Errorf("outcome %q exported as %q; want %q, %q", recs[0].Outcome, rows[0].ResolvedBy, tt.wantOutcome, tt.wantResolvedBy)
			}
		})
	}
}
