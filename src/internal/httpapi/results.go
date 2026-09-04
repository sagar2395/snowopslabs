// SPDX-License-Identifier: Apache-2.0
package httpapi

import (
	"fmt"
	"net/http"
	"path/filepath"
	"time"

	"github.com/gorilla/mux"
	"github.com/sagar2395/snowopslabs/internal/results"
	"github.com/sagar2395/snowopslabs/pkg/checks"
)

func resultsStore(s *Server) *results.Store {
	return results.NewStore(filepath.Join(s.cfg.ProjectRoot, ".labctl", "history"))
}

// recordScenarioVerify appends a scenario-verification record (objectives +
// per-check pass/fail) to history, so the results view shows whether the user
// solved the scenario. Best-effort — a history write never fails verify.
func (s *Server) recordScenarioVerify(name string, checkResults []checks.Result, startedAt time.Time) {
	var objectives []string
	if sc, err := s.scenes.Get(name); err == nil {
		objectives = sc.Objectives
	}
	outcomes := make([]results.CheckOutcome, 0, len(checkResults))
	for _, r := range checkResults {
		detail := r.Error
		if detail == "" && !r.Pass {
			detail = fmt.Sprintf("got %s, want %s", dashOr(r.Got), dashOr(r.Want))
		}
		outcomes = append(outcomes, results.CheckOutcome{Name: r.Name, Pass: r.Pass, Detail: detail})
	}
	rec := results.NewScenarioRecord(name, "", objectives, outcomes, startedAt, time.Now())
	_ = resultsStore(s).Append(rec)
}

func dashOr(v string) string {
	if v == "" {
		return "-"
	}
	return v
}

// GET /api/v2/results — all records, newest first.
func (s *Server) handleResults(w http.ResponseWriter, r *http.Request) {
	store := resultsStore(s)
	recs, err := store.All()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if recs == nil {
		recs = []results.Record{}
	}
	// Reverse for newest-first.
	for i, j := 0, len(recs)-1; i < j; i, j = i+1, j-1 {
		recs[i], recs[j] = recs[j], recs[i]
	}
	respondJSON(w, http.StatusOK, recs)
}

// GET /api/v2/results/{kind} — records for a specific kind.
func (s *Server) handleResultsByKind(w http.ResponseWriter, r *http.Request) {
	kind := mux.Vars(r)["kind"]
	switch kind {
	case results.KindIncident, results.KindChallenge, results.KindModule:
	default:
		respondError(w, r, http.StatusBadRequest, "invalid_kind",
			"kind must be incident, challenge, or module")
		return
	}
	store := resultsStore(s)
	recs, err := store.ByKind(kind)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if recs == nil {
		recs = []results.Record{}
	}
	// Reverse for newest-first.
	for i, j := 0, len(recs)-1; i < j; i, j = i+1, j-1 {
		recs[i], recs[j] = recs[j], recs[i]
	}
	respondJSON(w, http.StatusOK, recs)
}

// GET /api/v2/leaderboard — per-user aggregate ranking for team mode.
func (s *Server) handleLeaderboard(w http.ResponseWriter, r *http.Request) {
	store := resultsStore(s)
	board, err := store.Leaderboard()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if board == nil {
		board = []results.LeaderboardEntry{}
	}
	respondJSON(w, http.StatusOK, board)
}

// GET /api/v2/progress — learn module completions grouped by path.
func (s *Server) handleProgress(w http.ResponseWriter, r *http.Request) {
	store := resultsStore(s)
	progress, err := store.Progress()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if progress == nil {
		progress = map[string][]results.Record{}
	}
	respondJSON(w, http.StatusOK, progress)
}
