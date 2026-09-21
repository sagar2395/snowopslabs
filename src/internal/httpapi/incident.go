// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/sagar2395/snowopslabs/internal/incident"
	"github.com/sagar2395/snowopslabs/pkg/checks"
)

func (s *Server) incidentRunner() *checks.Runner {
	r := checks.NewRunner()
	r.DefaultTimeout = 10 * time.Second
	promURL := os.Getenv("PROMETHEUS_URL")
	if promURL == "" {
		promURL = "http://prometheus." + s.cfg.DomainSuffix
	}
	r.PrometheusURL = promURL
	if s.incidents.AlertmanagerURL == "" {
		s.incidents.AlertmanagerURL = os.Getenv("ALERTMANAGER_URL")
		if s.incidents.AlertmanagerURL == "" {
			s.incidents.AlertmanagerURL = "http://alertmanager." + s.cfg.DomainSuffix
		}
	}
	return r
}

// faultResp is a fault as the UI needs it: the fault, the workload it is bound
// to, and the app prerequisites it names literally.
type faultResp struct {
	*incident.Fault
	// PinnedApps are the apps the fault names literally; entries written as
	// {{.WorkloadName}} are left out. See incident.Engine.PinnedApps.
	PinnedApps []string     `json:"pinnedApps"`
	Workload   WorkloadResp `json:"workload"`
}

// handleListIncidents lists faults plus the active incident (if any).
func (s *Server) handleListIncidents(w http.ResponseWriter, r *http.Request) {
	active, err := s.incidents.Active()
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	// Show faults for the app they would be injected into: the active
	// incident's app if there is one, otherwise the current binding. ListBound
	// does not take the binding lock, so this polled read never waits behind
	// an injection.
	bound := s.activeIncidentWorkload()
	faults := s.incidents.ListBound(bound)
	out := make([]faultResp, 0, len(faults))
	for _, f := range faults {
		out = append(out, faultResp{Fault: f, PinnedApps: s.incidents.PinnedApps(f.Name), Workload: workloadResp(bound)})
	}

	writeJSONCached(w, r, http.StatusOK, map[string]any{
		"faults":   out,
		"active":   active,
		"workload": workloadResp(bound),
	})
}

func (s *Server) handleIncidentInject(w http.ResponseWriter, r *http.Request) {
	name, ok := pathName(w, r, "fault")
	if !ok {
		return
	}
	s.injectAndRespond(w, r, name)
}

func (s *Server) handleIncidentInjectRandom(w http.ResponseWriter, r *http.Request) {
	seed, _ := strconv.ParseInt(r.URL.Query().Get("seed"), 10, 64)
	f, err := s.incidents.PickRandom(seed, r.URL.Query().Get("category"))
	if err != nil {
		respondError(w, r, http.StatusConflict, "no_eligible_fault", err.Error())
		return
	}
	s.injectAndRespond(w, r, f.Name)
}

func (s *Server) injectAndRespond(w http.ResponseWriter, r *http.Request, name string) {
	force := r.URL.Query().Get("force") == "true"
	silent := r.URL.Query().Get("silent") == "true"

	// The app to break. Inject records it, and status, hints and resolve read
	// it back.
	app := r.URL.Query().Get("app")
	if app != "" && !isValidName(app) {
		respondError(w, r, http.StatusBadRequest, "invalid_input", fmt.Sprintf("invalid app name %q", app))
		return
	}
	_, incidents, release, err := s.withWorkload(app)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid_input", err.Error())
		return
	}
	defer release()

	f, err := incidents.Inject(name, s.exec, force, silent)
	if err != nil {
		switch {
		case errors.Is(err, incident.ErrIncidentActive):
			respondError(w, r, http.StatusConflict, "incident_active", err.Error())
		case f == nil:
			respondError(w, r, http.StatusNotFound, "not_found", err.Error())
		default:
			respondError(w, r, http.StatusInternalServerError, "inject_failed", err.Error())
		}
		return
	}

	resp := map[string]any{"status": "injected", "silent": silent}
	if !silent {
		resp["fault"] = f
	}
	respondJSON(w, http.StatusOK, resp)
}

// handleIncidentStatus runs the active incident's detection check.
func (s *Server) handleIncidentStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()

	// Check the app the fault was injected into; a check aimed at the wrong
	// namespace would pass and clear the incident.
	incidents, release := s.bindToActiveIncident()
	defer release()

	res, err := incidents.Status(ctx, s.incidentRunner(), actingUser(r))
	if errors.Is(err, incident.ErrNoActive) {
		respondJSON(w, http.StatusOK, map[string]any{"active": nil})
		return
	}
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	// In silent mode, don't leak the fault's identity until it's resolved.
	if res.Active.Silent && !res.Resolved {
		respondJSON(w, http.StatusOK, map[string]any{
			"active":   map[string]any{"fault": "(hidden)", "injectedAt": res.Active.InjectedAt, "silent": true},
			"resolved": false,
			"check":    map[string]any{"pass": false},
		})
		return
	}
	respondJSON(w, http.StatusOK, res)
}

// handleIncidentHint reveals the next hint (recorded against the run).
func (s *Server) handleIncidentHint(w http.ResponseWriter, r *http.Request) {
	h, err := s.incidents.NextHint()
	if err != nil {
		switch {
		case errors.Is(err, incident.ErrNoActive):
			respondError(w, r, http.StatusConflict, "no_active_incident", err.Error())
		case errors.Is(err, incident.ErrNoMoreHints):
			respondError(w, r, http.StatusConflict, "no_more_hints", err.Error())
		default:
			respondError(w, r, http.StatusInternalServerError, "internal_error", err.Error())
		}
		return
	}
	respondJSON(w, http.StatusOK, h)
}

// handleIncidentHistory returns past runs with MTTR data.
func (s *Server) handleIncidentHistory(w http.ResponseWriter, r *http.Request) {
	recs, err := s.incidents.History()
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if recs == nil {
		recs = []incident.Record{}
	}
	respondJSON(w, http.StatusOK, recs)
}

func (s *Server) handleIncidentResolve(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name != "" && !isValidName(name) {
		respondError(w, r, http.StatusBadRequest, "invalid_input", fmt.Sprintf("invalid fault name %q: must match ^[a-zA-Z0-9_-]{1,64}$", name))
		return
	}
	// resolve.sh acts on the workload that was broken.
	incidents, release := s.bindToActiveIncident()
	defer release()

	f, err := incidents.Resolve(name, s.exec, actingUser(r))
	if err != nil {
		if errors.Is(err, incident.ErrNoActive) {
			respondError(w, r, http.StatusConflict, "no_active_incident", err.Error())
			return
		}
		if f == nil {
			respondError(w, r, http.StatusNotFound, "not_found", err.Error())
			return
		}
		respondError(w, r, http.StatusInternalServerError, "resolve_failed", err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"status": "resolved", "fault": f.Name})
}

// bindToActiveIncident returns an engine bound to the workload the active fault
// was injected into, so status and resolve act on what was actually broken.
func (s *Server) bindToActiveIncident() (*incident.Engine, func()) {
	app := ""
	if a, err := s.incidents.Active(); err == nil && a != nil {
		app = a.App
	}
	incidents, release, err := s.incidentEngineFor(app)
	if err != nil {
		// The recorded app is gone. Carry on with the current binding so the
		// incident can still be resolved.
		incidents, release, _ = s.incidentEngineFor("")
	}
	return incidents, release
}

func (s *Server) incidentEngineFor(app string) (*incident.Engine, func(), error) {
	_, incidents, release, err := s.withWorkload(app)
	return incidents, release, err
}
