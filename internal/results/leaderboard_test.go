// SPDX-License-Identifier: Apache-2.0
package results

import (
	"testing"
	"time"
)

func TestBuildLeaderboard_Aggregation(t *testing.T) {
	recs := []Record{
		{Kind: KindChallenge, User: "alice", Score: 90, Outcome: "passed", HintsUsed: 1},
		{Kind: KindChallenge, User: "alice", Score: 80, Outcome: "passed"},
		{Kind: KindChallenge, User: "alice", Score: 0, Outcome: "failed"},
		{Kind: KindIncident, User: "alice", Outcome: "resolved", Elapsed: 120, HintsUsed: 2},
		{Kind: KindModule, User: "alice", Outcome: "completed"},
	}
	lb := BuildLeaderboard(recs)
	if len(lb) != 1 {
		t.Fatalf("expected 1 user, got %d", len(lb))
	}
	a := lb[0]
	if a.User != "alice" {
		t.Errorf("user = %q", a.User)
	}
	if a.TotalScore != 170 {
		t.Errorf("TotalScore = %d, want 170", a.TotalScore)
	}
	if a.ChallengesCompleted != 2 {
		t.Errorf("ChallengesCompleted = %d, want 2", a.ChallengesCompleted)
	}
	if a.IncidentsResolved != 1 {
		t.Errorf("IncidentsResolved = %d, want 1", a.IncidentsResolved)
	}
	if a.ModulesCompleted != 1 {
		t.Errorf("ModulesCompleted = %d, want 1", a.ModulesCompleted)
	}
	if a.HintsUsed != 3 {
		t.Errorf("HintsUsed = %d, want 3", a.HintsUsed)
	}
	if a.AvgMTTRSeconds != 120 {
		t.Errorf("AvgMTTRSeconds = %d, want 120", a.AvgMTTRSeconds)
	}
	if a.Runs != 5 {
		t.Errorf("Runs = %d, want 5", a.Runs)
	}
}

func TestBuildLeaderboard_Ranking(t *testing.T) {
	recs := []Record{
		{Kind: KindChallenge, User: "bob", Score: 50, Outcome: "passed"},
		{Kind: KindChallenge, User: "alice", Score: 95, Outcome: "passed"},
		{Kind: KindChallenge, User: "carol", Score: 95, Outcome: "passed"},
		{Kind: KindChallenge, User: "carol", Score: 10, Outcome: "passed"},
	}
	lb := BuildLeaderboard(recs)
	// carol: 105 (2 challenges), alice: 95 (1), bob: 50 (1)
	want := []string{"carol", "alice", "bob"}
	for i, w := range want {
		if lb[i].User != w {
			t.Errorf("rank %d = %q, want %q (full: %+v)", i, lb[i].User, w, lb)
		}
	}
}

func TestBuildLeaderboard_MTTRTieBreak(t *testing.T) {
	// Two users with identical score and challenge counts; the faster incident
	// responder should rank first.
	recs := []Record{
		{Kind: KindChallenge, User: "slow", Score: 100, Outcome: "passed"},
		{Kind: KindIncident, User: "slow", Outcome: "resolved", Elapsed: 600},
		{Kind: KindChallenge, User: "fast", Score: 100, Outcome: "passed"},
		{Kind: KindIncident, User: "fast", Outcome: "resolved", Elapsed: 60},
	}
	lb := BuildLeaderboard(recs)
	if lb[0].User != "fast" {
		t.Errorf("expected 'fast' first on MTTR tie-break, got %q (%+v)", lb[0].User, lb)
	}
}

func TestBuildLeaderboard_EdgeCases(t *testing.T) {
	t.Run("empty input", func(t *testing.T) {
		if lb := BuildLeaderboard(nil); len(lb) != 0 {
			t.Errorf("expected empty leaderboard, got %d", len(lb))
		}
	})

	t.Run("blank user becomes unknown", func(t *testing.T) {
		lb := BuildLeaderboard([]Record{{Kind: KindChallenge, User: "", Score: 10, Outcome: "passed"}})
		if len(lb) != 1 || lb[0].User != "unknown" {
			t.Errorf("expected 'unknown' user, got %+v", lb)
		}
	})

	t.Run("auto-resolved incident counts but unscored challenge ignored", func(t *testing.T) {
		recs := []Record{
			{Kind: KindIncident, User: "dave", Outcome: "auto-resolved", Elapsed: 30},
			{Kind: KindChallenge, User: "dave", Score: -1, Outcome: "aborted"},
		}
		lb := BuildLeaderboard(recs)
		if lb[0].IncidentsResolved != 1 {
			t.Errorf("auto-resolved should count: %+v", lb[0])
		}
		if lb[0].TotalScore != 0 {
			t.Errorf("negative score must not be added: %+v", lb[0])
		}
		if lb[0].ChallengesCompleted != 0 {
			t.Errorf("aborted challenge must not count: %+v", lb[0])
		}
	})
}

func TestStore_Leaderboard(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	now := time.Now()
	mustAppend(t, store, Record{Kind: KindChallenge, User: "alice", Score: 70, Outcome: "passed", StartedAt: now})
	mustAppend(t, store, Record{Kind: KindChallenge, User: "bob", Score: 40, Outcome: "passed", StartedAt: now})

	lb, err := store.Leaderboard()
	if err != nil {
		t.Fatal(err)
	}
	if len(lb) != 2 || lb[0].User != "alice" {
		t.Errorf("Leaderboard = %+v, want alice first", lb)
	}
}

func mustAppend(t *testing.T, s *Store, r Record) {
	t.Helper()
	if err := s.Append(r); err != nil {
		t.Fatal(err)
	}
}
