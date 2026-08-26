// SPDX-License-Identifier: Apache-2.0
package httpapi

import (
	"net/http"
	"path/filepath"

	"github.com/gorilla/mux"
	"github.com/sagar2395/snowopslabs/internal/results"
)

func resultsStore(s *Server) *results.Store {
	return results.NewStore(filepath.Join(s.cfg.ProjectRoot, ".labctl", "history"))
}

// GET /api/results — all records, newest first.
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

// GET /api/results/{kind} — records for a specific kind.
func (s *Server) handleResultsByKind(w http.ResponseWriter, r *http.Request) {
	kind := mux.Vars(r)["kind"]
	switch kind {
	case results.KindIncident, results.KindChallenge, results.KindModule:
	default:
		respondError(w, http.StatusBadRequest, "invalid_kind",
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

// GET /api/leaderboard — per-user aggregate ranking for team mode (task 063).
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

// GET /api/progress — learn module completions grouped by path.
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
