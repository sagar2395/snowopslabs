// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/json"
	"net/http"
	"path/filepath"

	"github.com/sagar2395/snowopslabs/internal/traffic"
)

// trafficStartRequest is the JSON body for POST /traffic/start. Only profile
// and rps are required; empty fields use the profile's defaults.
type trafficStartRequest struct {
	Profile  string `json:"profile"`
	RPS      int    `json:"rps"`
	Duration string `json:"duration,omitempty"`
	Target   string `json:"target,omitempty"`
	Method   string `json:"method,omitempty"`
}

// handleTrafficInfo lists the available k6 profiles. It only reads the
// profiles directory and returns an ETag.
func (s *Server) handleTrafficInfo(w http.ResponseWriter, r *http.Request) {
	profiles, err := traffic.Profiles(s.cfg.ProjectRoot)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "internal_error", "could not list traffic profiles")
		return
	}
	if profiles == nil {
		profiles = []string{}
	}
	// Include the bound workload so the UI can preselect it as the target.
	writeJSONCached(w, r, http.StatusOK, map[string]any{
		"profiles": profiles,
		"workload": workloadResp(s.boundWorkload("")),
	})
}

// handleTrafficStart validates the request and starts the in-cluster k6
// generator with services/traffic/start.sh. It returns 202 with a job ID;
// progress arrives on the event stream. Starting while traffic is running
// replaces the running generator.
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

	// Pass the settings as TRAFFIC_* variables. Only one traffic run exists at
	// a time, so setting them on the shared executor is safe.
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

// handleTrafficStop stops the generator with services/traffic/stop.sh, which
// deletes its namespace and every k6 pod in it.
func (s *Server) handleTrafficStop(w http.ResponseWriter, r *http.Request) {
	jobID := s.exec.NextActionID()
	go func() {
		_ = s.exec.RunScriptStreamedWith(jobID, "traffic-stop", filepath.Join(traffic.ScriptDir, "stop.sh"))
	}()
	respondJSON(w, http.StatusAccepted, map[string]string{"jobId": jobID, "status": "accepted"})
}
