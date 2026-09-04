// SPDX-License-Identifier: Apache-2.0
package httpapi

import (
	"encoding/json"
	"net/http"
	"path/filepath"

	"github.com/sagar2395/snowopslabs/internal/traffic"
)

// trafficStartRequest is the JSON body for POST /traffic/start. Zero fields fall
// back to the profile script's own defaults (only profile + rps are required by
// Options.Validate).
type trafficStartRequest struct {
	Profile  string `json:"profile"`
	RPS      int    `json:"rps"`
	Duration string `json:"duration,omitempty"`
	Target   string `json:"target,omitempty"`
	Method   string `json:"method,omitempty"`
}

// handleTrafficInfo lists the available k6 profiles so the UI can offer them.
// It reads the profiles/ directory only (no cluster call), so it stays cheap and
// carries an ETag for conditional revalidation.
func (s *Server) handleTrafficInfo(w http.ResponseWriter, r *http.Request) {
	profiles, err := traffic.Profiles(s.cfg.ProjectRoot)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "internal_error", "could not list traffic profiles")
		return
	}
	if profiles == nil {
		profiles = []string{}
	}
	writeJSONCached(w, r, http.StatusOK, map[string]any{"profiles": profiles})
}

// handleTrafficStart validates the requested profile/rps/duration/target, then
// launches the in-cluster k6 generator via services/traffic/start.sh. Like the
// other mutations it returns 202 with a job id; progress streams over the event
// channel. Starting while a run is active replaces it (the script's semantics).
func (s *Server) handleTrafficStart(w http.ResponseWriter, r *http.Request) {
	var req trafficStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid_request", "invalid JSON body")
		return
	}
	opts := traffic.Options{
		Profile:  req.Profile,
		Target:   req.Target,
		RPS:      req.RPS,
		Duration: req.Duration,
		Method:   req.Method,
	}
	if err := opts.Validate(s.cfg.ProjectRoot); err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid_input", err.Error())
		return
	}

	// Pass the tunables to the start script via TRAFFIC_* env. Traffic is a
	// singleton (start replaces any active run), so setting these on the shared
	// executor is safe here.
	for k, v := range opts.Env() {
		s.exec.SetEnv(k, v)
	}

	jobID := s.exec.NextActionID()
	label := "traffic-start: " + opts.Profile
	go func() {
		_ = s.exec.RunScriptStreamedWith(jobID, label, filepath.Join(traffic.ScriptDir, "start.sh"))
	}()
	respondJSON(w, http.StatusAccepted, map[string]string{"jobId": jobID, "status": "accepted"})
}

// handleTrafficStop tears down the generator via services/traffic/stop.sh, which
// deletes the dedicated namespace — so no k6 pods are left behind.
func (s *Server) handleTrafficStop(w http.ResponseWriter, r *http.Request) {
	jobID := s.exec.NextActionID()
	go func() {
		_ = s.exec.RunScriptStreamedWith(jobID, "traffic-stop", filepath.Join(traffic.ScriptDir, "stop.sh"))
	}()
	respondJSON(w, http.StatusAccepted, map[string]string{"jobId": jobID, "status": "accepted"})
}
