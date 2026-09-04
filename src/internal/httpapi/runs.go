// SPDX-License-Identifier: Apache-2.0
package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"

	"github.com/sagar2395/snowopslabs/internal/store"
)

// The run console is a read-mostly view over the durable run store:
// list recorded runs, open one to watch its timeline and live transcript, and
// cancel one that is still going. The store is the same database labctl writes
// through the run engine, so the console shows runs whichever process created
// them — the HTTP server itself does not execute runs (ADR-0006: consumers
// stream from a cursor rather than attaching to a live process).

// runView is the API shape of a run. store.Run is exposed through this DTO so
// the wire format is explicit and stable, and durations are milliseconds.
type runView struct {
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
		ID: r.ID, Kind: r.Kind, Target: r.Target, Status: string(r.Status),
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
		respondError(w, r, http.StatusInternalServerError, "internal_error", err.Error())
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
	steps, err := s.runStore.ListSteps(r.Context(), id)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "internal_error", err.Error())
		return
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

// handleRunLogs returns a run's transcript from the ?after cursor forward, along
// with the cursor for the next poll and whether the run is finished. This is the
// same cursor-forward read the CLI uses, so a reconnecting client never skips or
// duplicates a line (ADR-0006).
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
	lines, err := s.runStore.ReadLogs(r.Context(), id, after, 1000)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	out := runLogsView{RunID: id, Status: string(rec.Status), NextAfter: after, Lines: make([]logLineView, 0, len(lines))}
	for _, l := range lines {
		out.Lines = append(out.Lines, logLineView{Seq: l.Seq, At: l.At, Stream: string(l.Stream), Text: l.Text})
		out.NextAfter = l.Seq
	}
	out.Done = rec.Status.Terminal() && len(lines) == 0
	respondJSON(w, http.StatusOK, out)
}

// handleRunCancel records a cancellation request for a run. Like `labctl runs
// cancel`, when the server is not the process executing the run it writes the
// intent and terminal state to the store; the owning engine (if any) observes it
// and stops. Being explicit beats pretending to kill a process we don't own.
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
	if _, err := s.runStore.AppendLogs(r.Context(), id, []store.LogLine{{
		Stream: store.StreamSystem, Text: "cancellation requested via the web UI",
	}}); err != nil {
		respondError(w, r, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if err := s.runStore.FinishRun(r.Context(), id, store.StatusCancelled, nil,
		"cancelled by user", time.Now(), rec.Duration); err != nil {
		respondError(w, r, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "cancelled", "runId": id})
}

func respondRunLookupError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, store.ErrRunNotFound) {
		respondError(w, r, http.StatusNotFound, "not_found", err.Error())
		return
	}
	respondError(w, r, http.StatusInternalServerError, "internal_error", err.Error())
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
