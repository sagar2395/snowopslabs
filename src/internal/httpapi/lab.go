// SPDX-License-Identifier: Apache-2.0
package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/sagar2395/snowopslabs/internal/lab"
)

func (s *Server) labStore() *lab.Store {
	return lab.NewStore(s.cfg.ProjectRoot)
}

func (s *Server) labDeps(continueOnError bool) lab.Deps {
	return lab.Deps{
		Exec:            s.exec,
		Registry:        s.registry,
		Scenes:          s.scenes,
		ContinueOnError: continueOnError,
	}
}

// handleLabSnapshots lists saved snapshots, newest first.
func (s *Server) handleLabSnapshots(w http.ResponseWriter, r *http.Request) {
	snaps, err := s.labStore().List()
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if snaps == nil {
		snaps = []*lab.Snapshot{}
	}
	respondJSON(w, http.StatusOK, snaps)
}

// handleLabSnapshotTake records the current lab state under the given name.
func (s *Server) handleLabSnapshotTake(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	if !isValidName(name) {
		respondError(w, r, http.StatusBadRequest, "invalid_input", fmt.Sprintf("invalid snapshot name %q: must match ^[a-zA-Z0-9_-]{1,64}$", name))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	snap, warnings := lab.Collect(ctx, name, s.cfg.Profile, s.cfg.DomainSuffix, s.cfg.ProjectRoot, s.registry, s.scenes)
	if err := s.labStore().Save(snap); err != nil {
		respondError(w, r, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"snapshot": snap,
		"warnings": warnings,
	})
}

// handleLabSnapshotDelete removes a snapshot.
func (s *Server) handleLabSnapshotDelete(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	if !isValidName(name) {
		respondError(w, r, http.StatusBadRequest, "invalid_input", fmt.Sprintf("invalid snapshot name %q: must match ^[a-zA-Z0-9_-]{1,64}$", name))
		return
	}
	if err := s.labStore().Delete(name); err != nil {
		respondError(w, r, http.StatusNotFound, "not_found", err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleLabRestore replays a snapshot asynchronously (job pattern).
func (s *Server) handleLabRestore(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	if !isValidName(name) {
		respondError(w, r, http.StatusBadRequest, "invalid_input", fmt.Sprintf("invalid snapshot name %q: must match ^[a-zA-Z0-9_-]{1,64}$", name))
		return
	}
	snap, err := s.labStore().Load(name)
	if err != nil {
		respondError(w, r, http.StatusNotFound, "not_found", err.Error())
		return
	}

	plan := lab.RestorePlan(snap)
	jobID := s.exec.NextActionID()
	label := "Restore lab snapshot: " + name
	s.exec.BroadcastStart(jobID, label)
	go func() {
		s.exec.BroadcastEnd(jobID, label, lab.Execute(plan, s.labDeps(false)))
	}()
	respondJSON(w, http.StatusAccepted, map[string]interface{}{
		"jobId": jobID, "status": "accepted", "steps": len(plan),
	})
}

// handleLabReset tears the lab back to post-init. Destructive, so it
// requires explicit ?confirm=true — the UI shows the confirmation dialog.
func (s *Server) handleLabReset(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("confirm") != "true" {
		respondError(w, r, http.StatusBadRequest, "confirmation_required",
			"lab reset is destructive; call with ?confirm=true")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	current, _ := lab.Collect(ctx, "current", s.cfg.Profile, s.cfg.DomainSuffix, s.cfg.ProjectRoot, s.registry, s.scenes)
	plan := lab.ResetPlan(current.Platform, current.Apps, current.Scenarios)

	jobID := s.exec.NextActionID()
	label := "Reset lab"
	s.exec.BroadcastStart(jobID, label)
	go func() {
		err := lab.Execute(plan, s.labDeps(true))
		// Force-clear scenario and incident activation state regardless of how
		// teardown went: after a reset their prerequisites are gone, so they must
		// read as inactive (and be re-activatable) even if a component teardown
		// failed part-way. The cluster resources are handled by the plan above;
		// this only reconciles the on-disk activation markers.
		s.scenes.DeactivateAll()
		if s.incidents != nil {
			s.incidents.ClearActiveState()
		}
		s.exec.BroadcastEnd(jobID, label, err)
	}()
	respondJSON(w, http.StatusAccepted, map[string]interface{}{
		"jobId": jobID, "status": "accepted", "steps": len(plan),
	})
}
