// SPDX-License-Identifier: Apache-2.0
package httpapi

import (
	"encoding/json"
	"net/http"
	"path/filepath"

	"github.com/gorilla/mux"
	"github.com/sagar2395/snowopslabs/internal/challenge"
)

func challengeEngine(s *Server) *challenge.Engine {
	return challenge.New(
		filepath.Join(s.cfg.ProjectRoot, "challenges"),
		filepath.Join(s.cfg.ProjectRoot, ".labctl", "challenges"),
		filepath.Join(s.cfg.ProjectRoot, ".labctl", "history"),
	)
}

func (s *Server) handleListChallenges(w http.ResponseWriter, r *http.Request) {
	eng := challengeEngine(s)
	cs, err := eng.Challenges()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	type summary struct {
		Name        string `json:"name"`
		DisplayName string `json:"displayName"`
		Description string `json:"description"`
		Category    string `json:"category"`
		ParTime     string `json:"parTime"`
	}
	out := make([]summary, 0, len(cs))
	for _, c := range cs {
		out = append(out, summary{
			Name:        c.Name,
			DisplayName: c.DisplayName,
			Description: c.Description,
			Category:    c.Category,
			ParTime:     c.ParTime,
		})
	}
	respondCatalog(w, r, out)
}

func (s *Server) handleChallengeInfo(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	eng := challengeEngine(s)
	c, err := eng.Load(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"name":        c.Name,
		"displayName": c.DisplayName,
		"description": c.Description,
		"category":    c.Category,
		"parTime":     c.ParTime,
		"hintPenalty": c.HintPenalty,
		"setup":       c.Setup,
	})
}

func (s *Server) handleChallengeStatus(w http.ResponseWriter, r *http.Request) {
	eng := challengeEngine(s)
	active, err := eng.Active()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if active == nil {
		respondJSON(w, http.StatusOK, map[string]interface{}{"active": false})
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"active":    true,
		"challenge": active.ChallengeName,
		"startedAt": active.StartedAt,
		"hintsUsed": active.HintsUsed,
		"elapsed":   active.Elapsed().String(),
	})
}

func (s *Server) handleChallengeHistory(w http.ResponseWriter, r *http.Request) {
	eng := challengeEngine(s)
	history, err := eng.History()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if history == nil {
		history = []challenge.RunRecord{}
	}
	respondJSON(w, http.StatusOK, history)
}

func (s *Server) handleChallengeMarkComplete(w http.ResponseWriter, r *http.Request) {
	eng := challengeEngine(s)
	var req struct {
		Passed int `json:"passed"`
		Total  int `json:"total"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	rec, err := eng.Complete(req.Passed, req.Total, "passed", actingUser(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	respondJSON(w, http.StatusOK, rec)
}
