// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"

	"github.com/snowops-oss/snowopslabs/internal/store"
)

// The run console endpoints list recorded runs, show one run's steps and
// transcript, and cancel a run in progress. They read the same run store the
// CLI writes, so they show runs from any labctl process. The HTTP server does
// not execute these runs itself (ADR-0006).

// runView is the API representation of a store.Run. Durations are in
// milliseconds.
type runView struct {
	runRecoveryView
	ID         string     `json:"id"`
	Kind       string     `json:"kind"`
	Target     string     `json:"target,omitempty"`
	Status     string     `json:"status"`
	Actor      string     `json:"actor,omitempty"`
	QueuedAt   time.Time  `json:"queuedAt"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	EndedAt    *time.Time `json:"endedAt,omitempty"`
	DurationMs int64      `json:"durationMs,omitempty"`
	ExitCode   *int       `json:"exitCode,omitempty"`
	Error      string     `json:"error,omitempty"`
}

func toRunView(r store.Run) runView {
	v := runView{
		runRecoveryView: toRunRecoveryView(r),
		ID:              r.ID, Kind: r.Kind, Target: r.Target, Status: string(r.Status),
		Actor: r.Actor, QueuedAt: r.QueuedAt, ExitCode: r.ExitCode, Error: r.Error,
	}
	if !r.StartedAt.IsZero() {
		v.StartedAt = &r.StartedAt
	}
	if !r.EndedAt.IsZero() {
		v.EndedAt = &r.EndedAt
	}
	if r.Duration > 0 {
		v.DurationMs = r.Duration.Milliseconds()
	}
	return v
}

type runRecoveryView struct {
	OwnerID         string     `json:"ownerId,omitempty"`
	RecoveryState   string     `json:"recoveryState,omitempty"`
	RecoveryReason  string     `json:"recoveryReason,omitempty"`
	RecoveryCommand string     `json:"recoveryCommand,omitempty"`
	ReleasedAt      *time.Time `json:"releasedAt,omitempty"`
	ReleasedBy      string     `json:"releasedBy,omitempty"`
}

func toRunRecoveryView(r store.Run) runRecoveryView {
	v := runRecoveryView{RecoveryReason: r.RecoveryReason, ReleasedBy: r.ReleasedBy}
	if r.RecoveryState != store.RecoveryNone && r.RecoveryState != "" {
		v.OwnerID = r.OwnerID
		v.RecoveryState = string(r.RecoveryState)
	}
	if r.RecoveryState == store.RecoveryRequired {
		v.RecoveryCommand = "labctl runs recover " + r.ID + " --force"
	}
	if !r.ReleasedAt.IsZero() {
		v.ReleasedAt = &r.ReleasedAt
	}
	return v
}

type stepView struct {
	Index     int        `json:"index"`
	Name      string     `json:"name"`
	Status    string     `json:"status"`
	StartedAt time.Time  `json:"startedAt"`
	EndedAt   *time.Time `json:"endedAt,omitempty"`
}

type runDetailView struct {
	runView
	Steps []stepView `json:"steps"`
}

type logLineView struct {
	Seq    int64     `json:"seq"`
	At     time.Time `json:"at"`
	Stream string    `json:"stream"`
	Text   string    `json:"text"`
}

type runLogsView struct {
	runRecoveryView
	RunID  string        `json:"runId"`
	Status string        `json:"status"`
	Lines  []logLineView `json:"lines"`
	// NextAfter is the cursor to pass as ?after on the next poll. Done is true
	// once the run has reached a terminal state and no lines remain past the
	// cursor, so a client knows to stop polling.
	NextAfter int64 `json:"nextAfter"`
	Done      bool  `json:"done"`
}

// handleRunsList returns recorded runs, newest first, filtered by ?status and
// ?kind, in the version-appropriate paginated shape.
func (s *Server) handleRunsList(w http.ResponseWriter, r *http.Request) {
	if s.runStore == nil {
		respondError(w, r, http.StatusServiceUnavailable, "unavailable", "the run store is not available")
		return
	}
	filter := store.RunFilter{Kind: r.URL.Query().Get("kind"), Limit: maxPageLimit}
	if raw := r.URL.Query().Get("status"); raw != "" {
		st := store.Status(raw)
		if !st.Valid() {
			respondError(w, r, http.StatusBadRequest, "invalid_request",
				"unknown status (queued, running, succeeded, failed, cancelled, timed_out)")
			return
		}
		filter.Status = []store.Status{st}
	}
	runs, err := s.runStore.ListRuns(r.Context(), filter)
	if err != nil {
		respondInternal(w, r, err)
		return
	}
	views := make([]runView, 0, len(runs))
	for _, run := range runs {
		views = append(views, toRunView(run))
	}
	respondCatalog(w, r, views)
}

// handleRunGet returns one run with its step timeline.
func (s *Server) handleRunGet(w http.ResponseWriter, r *http.Request) {
	if s.runStore == nil {
		respondError(w, r, http.StatusServiceUnavailable, "unavailable", "the run store is not available")
		return
	}
	id := mux.Vars(r)["id"]
	rec, err := s.runStore.GetRun(r.Context(), id)
	if err != nil {
		respondRunLookupError(w, r, err)
		return
	}
	// A hidden run's steps are named by its script, so they are withheld too.
	var steps []store.Step
	if !rec.Hidden() {
		steps, err = s.runStore.ListSteps(r.Context(), id)
		if err != nil {
			respondInternal(w, r, err)
			return
		}
	}
	detail := runDetailView{runView: toRunView(rec), Steps: make([]stepView, 0, len(steps))}
	for _, st := range steps {
		sv := stepView{Index: st.Index, Name: st.Name, Status: string(st.Status), StartedAt: st.StartedAt}
		if !st.EndedAt.IsZero() {
			sv.EndedAt = &st.EndedAt
		}
		detail.Steps = append(detail.Steps, sv)
	}
	respondJSON(w, http.StatusOK, detail)
}

// handleRunLogs returns a run's transcript after the ?after cursor, the cursor
// for the next poll, and whether the run has finished (ADR-0006).
func (s *Server) handleRunLogs(w http.ResponseWriter, r *http.Request) {
	if s.runStore == nil {
		respondError(w, r, http.StatusServiceUnavailable, "unavailable", "the run store is not available")
		return
	}
	id := mux.Vars(r)["id"]
	after, err := parseAfter(r.URL.Query().Get("after"))
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	rec, err := s.runStore.GetRun(r.Context(), id)
	if err != nil {
		respondRunLookupError(w, r, err)
		return
	}
	if rec.Hidden() {
		respondError(w, r, http.StatusForbidden, "hidden_run",
			"this run injected a silent incident: its output is withheld so it cannot name the fault")
		return
	}
	lines, err := s.runStore.ReadLogs(r.Context(), id, after, 1000)
	if err != nil {
		respondInternal(w, r, err)
		return
	}
	out := runLogsView{RunID: id, Status: string(rec.Status), NextAfter: after, Lines: make([]logLineView, 0, len(lines))}
	out.runRecoveryView = toRunRecoveryView(rec)
	for _, l := range lines {
		out.Lines = append(out.Lines, logLineView{Seq: l.Seq, At: l.At, Stream: string(l.Stream), Text: l.Text})
		out.NextAfter = l.Seq
	}
	out.Done = rec.Status.Terminal() && len(lines) == 0
	respondJSON(w, http.StatusOK, out)
}

// handleRunCancel cancels a run. Like `labctl runs cancel`, when the run
// belongs to another process it writes the cancellation to the store, and the
// engine running it, if any, stops it.
func (s *Server) handleRunCancel(w http.ResponseWriter, r *http.Request) {
	if s.runStore == nil {
		respondError(w, r, http.StatusServiceUnavailable, "unavailable", "the run store is not available")
		return
	}
	id := mux.Vars(r)["id"]
	rec, err := s.runStore.GetRun(r.Context(), id)
	if err != nil {
		respondRunLookupError(w, r, err)
		return
	}
	if rec.Status.Terminal() {
		respondError(w, r, http.StatusConflict, "already_finished",
			"run "+id+" has already finished ("+string(rec.Status)+")")
		return
	}
	if rec.RecoveryState == store.RecoveryRequired {
		respondError(w, r, http.StatusConflict, "recovery_required", "run "+id+" requires recovery; check the lab, then use labctl runs recover "+id+" --force")
		return
	}
	if _, err := s.runStore.AppendLogs(r.Context(), id, []store.LogLine{{
		Stream: store.StreamSystem, Text: "cancellation requested via the web UI",
	}}); err != nil {
		respondInternal(w, r, err)
		return
	}
	if err := s.runStore.FinishRun(r.Context(), id, store.StatusCancelled, nil,
		"cancelled by user", time.Now(), rec.Duration); err != nil {
		respondInternal(w, r, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "cancelled", "runId": id})
}

func respondRunLookupError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, store.ErrRunNotFound) {
		respondError(w, r, http.StatusNotFound, "not_found", err.Error())
		return
	}
	respondInternal(w, r, err)
}

// parseAfter reads the ?after log cursor: empty means "from the start" (0).
func parseAfter(raw string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0, errors.New("after must be a non-negative integer")
	}
	return n, nil
}
