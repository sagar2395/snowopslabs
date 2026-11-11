// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"time"

	"github.com/snowops-oss/snowopslabs/internal/labcheck"
	"github.com/snowops-oss/snowopslabs/internal/learn"
	"github.com/snowops-oss/snowopslabs/internal/workload"
)

func learnEngine(s *Server) *learn.Engine {
	return learn.New(
		filepath.Join(s.cfg.ProjectRoot, "learn"),
		filepath.Join(s.cfg.StateDir, "learn"),
		filepath.Join(s.cfg.StateDir, "history"),
	)
}

func (s *Server) handleLearnPaths(w http.ResponseWriter, r *http.Request) {
	eng := learnEngine(s)
	paths, err := eng.Paths()
	if err != nil {
		respondInternal(w, r, err)
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
	name, ok := pathName(w, r, "path")
	if !ok {
		return
	}
	eng := learnEngine(s)
	p, err := eng.LoadPath(name)
	if err != nil {
		respondLoadError(w, r, "learning path", name, err)
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
	name, ok := pathName(w, r, "path")
	if !ok {
		return
	}
	eng := learnEngine(s)
	p, err := eng.LoadPath(name)
	if err != nil {
		respondLoadError(w, r, "learning path", name, err)
		return
	}
	prog, err := eng.StartPath(name)
	if err != nil {
		respondInternal(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, learnProgressPayload(p, prog))
}

// handleLearnReset discards a path's progress so the learner can start over.
// It does not touch the cluster.
func (s *Server) handleLearnReset(w http.ResponseWriter, r *http.Request) {
	name, ok := pathName(w, r, "path")
	if !ok {
		return
	}
	eng := learnEngine(s)
	if _, err := eng.LoadPath(name); err != nil {
		respondLoadError(w, r, "learning path", name, err)
		return
	}
	if err := eng.ResetProgress(name); err != nil {
		respondInternal(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"path": name, "started": false})
}

func (s *Server) handleLearnProgress(w http.ResponseWriter, r *http.Request) {
	name, ok := pathName(w, r, "path")
	if !ok {
		return
	}
	eng := learnEngine(s)
	p, err := eng.LoadPath(name)
	if err != nil {
		respondLoadError(w, r, "learning path", name, err)
		return
	}
	prog, err := eng.Progress(name)
	if err != nil {
		respondInternal(w, r, err)
		return
	}
	if prog == nil {
		respondJSON(w, http.StatusOK, map[string]any{"path": name, "started": false})
		return
	}
	respondJSON(w, http.StatusOK, learnProgressPayload(p, prog))
}

// learnGradeBudget is the time limit for grading one module; its check also
// has its own timeout.
const learnGradeBudget = 2 * time.Minute

// handleLearnComplete grades a module by running its check against the lab,
// as `labctl learn next` does. Only a passing check records the module; the
// answer carries the check's result either way.
func (s *Server) handleLearnComplete(w http.ResponseWriter, r *http.Request) {
	name, ok := pathName(w, r, "path")
	if !ok {
		return
	}
	eng := learnEngine(s)
	var req struct {
		ModuleIdx int `json:"moduleIdx"`
	}
	if !decodeBody(w, r, &req, false) {
		return
	}
	// Load the path so the completion is recorded as "<path>/<module>" and
	// attributed to the authenticated user.
	p, err := eng.LoadPath(name)
	if err != nil {
		respondLoadError(w, r, "learning path", name, err)
		return
	}
	prog, err := eng.Progress(name)
	if err != nil {
		respondInternal(w, r, err)
		return
	}
	if prog == nil {
		respondError(w, r, http.StatusConflict, "path_not_started",
			fmt.Sprintf("learning path %q has not been started: start it first", name))
		return
	}
	if req.ModuleIdx < 0 || req.ModuleIdx >= len(p.Modules) {
		respondError(w, r, http.StatusBadRequest, "invalid_input",
			fmt.Sprintf("moduleIdx %d is out of range: learning path %q has %d module(s), numbered from 0", req.ModuleIdx, name, len(p.Modules)))
		return
	}
	// Learning paths are graded against the default app, as in the CLI.
	bound := s.boundWorkload(workload.DefaultApp)
	ctx, cancel := context.WithTimeout(r.Context(), learnGradeBudget)
	defer cancel()
	res, err := eng.Grade(ctx, p, prog, req.ModuleIdx, labcheck.NewRunner(s.cfg, bound, s.cfg.ProjectRoot), s.learnResolver(bound), actingUser(r))
	if err != nil {
		respondInternal(w, r, err)
		return
	}
	payload := learnProgressPayload(p, prog)
	payload["check"] = res
	respondJSON(w, http.StatusOK, payload)
}

// learnResolver fills a learning-path check field for the app it is graded
// against.
func (s *Server) learnResolver(bound workload.Workload) func(string) string {
	var expand func(string) string
	if s.incidents != nil {
		incidents := s.incidents.Clone()
		incidents.Workload = bound
		expand = incidents.ResolveTemplate
	}
	return learn.Resolver(s.cfg.DomainSuffix, expand)
}
