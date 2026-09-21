// SPDX-License-Identifier: Apache-2.0

package compare

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sampleReport(scenario string, at time.Time, apps ...string) *Report {
	r := &Report{
		Scenario: scenario, Apps: apps, Profile: "steady", RPS: 40,
		WarmupSeconds: 60, WindowSeconds: 180, StartedAt: at, EndedAt: at.Add(time.Minute),
	}
	for _, a := range apps {
		r.Measurements = append(r.Measurements, Measurement{App: a, Values: map[string]float64{KeyRPS: 40}})
	}
	return r
}

func TestStoreRoundTripsAReport(t *testing.T) {
	s := NewStore(t.TempDir())
	at := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	id, err := s.Append(sampleReport("autoscaling-under-load", at, "go-api", "java-api"))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if !strings.HasPrefix(id, "20260909-120000") {
		t.Errorf("id = %q, want it to start with the run time", id)
	}

	got, err := s.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// The conditions must survive the round trip, or a stored report cannot be
	// compared with anything.
	if got.WarmupSeconds != 60 || got.WindowSeconds != 180 || got.RPS != 40 || got.Profile != "steady" {
		t.Errorf("conditions lost: %+v", got)
	}
	if len(got.Measurements) != 2 || got.Measurements[0].App != "go-api" {
		t.Errorf("measurements lost: %+v", got.Measurements)
	}
}

func TestStoreListsNewestFirst(t *testing.T) {
	s := NewStore(t.TempDir())
	base := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	for i, name := range []string{"first", "second", "third"} {
		if _, err := s.Append(sampleReport(name, base.Add(time.Duration(i)*time.Hour), "a", "b")); err != nil {
			t.Fatal(err)
		}
	}
	sums, err := s.Summaries()
	if err != nil {
		t.Fatalf("Summaries: %v", err)
	}
	if len(sums) != 3 || sums[0].Scenario != "third" || sums[2].Scenario != "first" {
		t.Errorf("order = %v, want newest first", sums)
	}
}

func TestStoreGetAcceptsAnUnambiguousPrefix(t *testing.T) {
	s := NewStore(t.TempDir())
	at := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	id, _ := s.Append(sampleReport("scenario-one", at, "a", "b"))

	if _, err := s.Get(id[:12]); err != nil {
		t.Errorf("prefix lookup failed: %v", err)
	}
	if _, err := s.Get("nope"); err == nil {
		t.Error("want an error for an unknown id")
	}
}

func TestStoreRefusesAnAmbiguousPrefix(t *testing.T) {
	s := NewStore(t.TempDir())
	at := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	_, _ = s.Append(sampleReport("alpha", at, "a", "b"))
	_, _ = s.Append(sampleReport("alruns", at, "a", "b"))

	_, err := s.Get("20260909-120000-al")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("err = %v, want it to report the ambiguity rather than guess", err)
	}
}

// One truncated write must not make the whole history unreadable.
func TestStoreSkipsCorruptLines(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	at := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	if _, err := s.Append(sampleReport("good", at, "a", "b")); err != nil {
		t.Fatal(err)
	}
	f, _ := os.OpenFile(filepath.Join(dir, "comparisons.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
	_, _ = f.WriteString("{\"id\":\"broken\",\n")
	_ = f.Close()

	sums, err := s.Summaries()
	if err != nil {
		t.Fatalf("Summaries: %v", err)
	}
	if len(sums) != 1 || sums[0].Scenario != "good" {
		t.Errorf("summaries = %v, want the one readable run", sums)
	}
}

// An empty comparison is noise in the history a reader has to rule out.
func TestStoreDoesNotRecordAnEmptyRun(t *testing.T) {
	s := NewStore(t.TempDir())
	id, err := s.Append(&Report{Scenario: "nothing-measured"})
	if err != nil || id != "" {
		t.Errorf("Append(empty) = %q, %v; want it skipped", id, err)
	}
	if sums, _ := s.Summaries(); len(sums) != 0 {
		t.Errorf("empty run was recorded: %v", sums)
	}
}

func TestStoreOnMissingFileIsEmptyNotAnError(t *testing.T) {
	s := NewStore(t.TempDir())
	sums, err := s.Summaries()
	if err != nil {
		t.Fatalf("a history that does not exist yet is not an error: %v", err)
	}
	if len(sums) != 0 {
		t.Errorf("got %d summaries from an empty store", len(sums))
	}
}
