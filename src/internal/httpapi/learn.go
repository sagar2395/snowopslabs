// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/json"
	"net/http"
	"path/filepath"

	"github.com/gorilla/mux"
	"github.com/sagar2395/snowopslabs/internal/learn"
)

func learnEngine(s *Server) *learn.Engine {
	return learn.New(
		filepath.Join(s.cfg.ProjectRoot, "learn"),
		filepath.Join(s.cfg.ProjectRoot, ".labctl", "learn"),
		filepath.Join(s.cfg.ProjectRoot, ".labctl", "history"),
	)
}

func (s *Server) handleLearnPaths(w http.ResponseWriter, r *http.Request) {
	eng := learnEngine(s)
	paths, err := eng.Paths()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	type pathSummary struct {
		Name             string   `json:"name"`
		DisplayName      string   `json:"displayName"`
		Description      string   `json:"description"`
		Tags             []string `json:"tags"`
		EstimatedMinutes int      `json:"estimatedMinutes"`
		ModuleCount      int      `json:"moduleCount"`
		CompletedCount   int      `json:"completedCount"`
	}
	var out []pathSummary
	for _, p := range paths {
		prog, _ := eng.Progress(p.Name)
		done := 0
		if prog != nil {
			done = len(prog.CompletedIdxs)
		}
		out = append(out, pathSummary{
			Name:             p.Name,
			DisplayName:      p.DisplayName,
			Description:      p.Description,
			Tags:             p.Tags,
			EstimatedMinutes: p.EstimatedMinutes,
			ModuleCount:      len(p.Modules),
			CompletedCount:   done,
		})
	}
	respondCatalog(w, r, out)
}

func (s *Server) handleLearnPath(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	eng := learnEngine(s)
	p, err := eng.LoadPath(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	prog, _ := eng.Progress(name)
	type moduleOut struct {
		Name        string       `json:"name"`
		DisplayName string       `json:"displayName"`
		HasIntro    bool         `json:"hasIntro"`
		Action      learn.Action `json:"action"`
		Completed   bool         `json:"completed"`
	}
	completed := map[int]bool{}
	if prog != nil {
		for _, i := range prog.CompletedIdxs {
			completed[i] = true
		}
	}
	modules := make([]moduleOut, len(p.Modules))
	for i, m := range p.Modules {
		modules[i] = moduleOut{
			Name:        m.Name,
			DisplayName: m.DisplayName,
			HasIntro:    m.Intro != "",
			Action:      m.Action,
			Completed:   completed[i],
		}
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"name":        p.Name,
		"displayName": p.DisplayName,
		"description": p.Description,
		"tags":        p.Tags,
		"modules":     modules,
	})
}

// learnProgressPayload returns a started path's progress as the start,
// progress and complete endpoints all send it, including the started, nextIdx
// and total fields that learn.Progress does not have.
func learnProgressPayload(p *learn.Path, prog *learn.Progress) map[string]any {
	return map[string]any{
		"path":      prog.PathName,
		"started":   true,
		"completed": prog.CompletedIdxs,
		"total":     len(p.Modules),
		"nextIdx":   learn.NextModuleIdx(p, prog),
		"startedAt": prog.StartedAt,
		"updatedAt": prog.LastUpdatedAt,
	}
}

func (s *Server) handleLearnStart(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	eng := learnEngine(s)
	prog, err := eng.StartPath(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	p, err := eng.LoadPath(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	respondJSON(w, http.StatusOK, learnProgressPayload(p, prog))
}

// handleLearnReset discards a path's progress so the learner can start over.
// It does not touch the cluster.
func (s *Server) handleLearnReset(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	eng := learnEngine(s)
	if err := eng.ResetProgress(name); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"path": name, "started": false})
}

func (s *Server) handleLearnProgress(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	eng := learnEngine(s)
	prog, err := eng.Progress(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if prog == nil {
		respondJSON(w, http.StatusOK, map[string]any{"path": name, "started": false})
		return
	}
	p, err := eng.LoadPath(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	respondJSON(w, http.StatusOK, learnProgressPayload(p, prog))
}

func (s *Server) handleLearnMarkComplete(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	eng := learnEngine(s)
	var req struct {
		ModuleIdx int `json:"moduleIdx"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	prog, err := eng.Progress(name)
	if err != nil || prog == nil {
		http.Error(w, "path not started", http.StatusBadRequest)
		return
	}
	// Load the path so the completion is recorded as "<path>/<module>" and
	// attributed to the authenticated user.
	p, err := eng.LoadPath(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := eng.MarkCompleteModule(p, prog, req.ModuleIdx, actingUser(r)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	respondJSON(w, http.StatusOK, learnProgressPayload(p, prog))
}
