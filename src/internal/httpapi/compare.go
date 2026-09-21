// SPDX-License-Identifier: Apache-2.0
package httpapi

import (
	"net/http"
	"path/filepath"

	"github.com/gorilla/mux"
	"github.com/sagar2395/snowopslabs/internal/compare"
)

func compareStore(s *Server) *compare.Store {
	return compare.NewStore(filepath.Join(s.cfg.ProjectRoot, ".labctl", "history"))
}

// comparisonView is one recorded comparison, shaped for the UI.
//
// The measurement set travels with the numbers rather than being hardcoded in
// the client: a comparison recorded before a metric existed must still render,
// and the front end has no business knowing which way is better for each one.
type comparisonView struct {
	ID            string                `json:"id"`
	Scenario      string                `json:"scenario"`
	Apps          []string              `json:"apps"`
	Profile       string                `json:"profile"`
	RPS           int                   `json:"rps"`
	WarmupSeconds int                   `json:"warmupSeconds"`
	WindowSeconds int                   `json:"windowSeconds"`
	StartedAt     string                `json:"startedAt"`
	Metrics       []metricView          `json:"metrics"`
	Measurements  []compare.Measurement `json:"measurements"`
}

type metricView struct {
	Key         string  `json:"key"`
	Label       string  `json:"label"`
	Unit        string  `json:"unit"`
	LowerBetter bool    `json:"lowerBetter"`
	Neutral     bool    `json:"neutral"`
	Scale       float64 `json:"scale"`
	Digits      int     `json:"digits"`
}

func metricViews() []metricView {
	ms := compare.Metrics()
	out := make([]metricView, 0, len(ms))
	for _, m := range ms {
		lower, noPreference := m.Direction()
		scale, digits := m.Display()
		out = append(out, metricView{
			Key: m.Key, Label: m.Label, Unit: m.Unit,
			LowerBetter: lower, Neutral: noPreference, Scale: scale, Digits: digits,
		})
	}
	return out
}

func toView(id string, r *compare.Report) comparisonView {
	return comparisonView{
		ID: id, Scenario: r.Scenario, Apps: r.Apps, Profile: r.Profile, RPS: r.RPS,
		WarmupSeconds: r.WarmupSeconds, WindowSeconds: r.WindowSeconds,
		StartedAt:    r.StartedAt.UTC().Format("2006-01-02T15:04:05Z"),
		Metrics:      metricViews(),
		Measurements: r.Measurements,
	}
}

// GET /api/v2/comparisons — every recorded comparison, newest first.
func (s *Server) handleListComparisons(w http.ResponseWriter, r *http.Request) {
	reps, ids, err := compareStore(s).List()
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	out := make([]comparisonView, 0, len(reps))
	for i, rep := range reps {
		out = append(out, toView(ids[i], rep))
	}
	writeJSONCached(w, r, http.StatusOK, out)
}

// GET /api/v2/comparisons/{id} — one recorded comparison.
func (s *Server) handleGetComparison(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	rep, err := compareStore(s).Get(id)
	if err != nil {
		respondError(w, r, http.StatusNotFound, "not_found", err.Error())
		return
	}
	writeJSONCached(w, r, http.StatusOK, toView(id, rep))
}
