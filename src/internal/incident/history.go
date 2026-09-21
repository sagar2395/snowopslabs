// SPDX-License-Identifier: Apache-2.0

package incident

// MTTR tracking: every completed incident run is recorded in the results
// store (.labctl/history/results.jsonl) with kind "incident".

import (
	"path/filepath"
	"time"

	"github.com/sagar2395/snowopslabs/internal/results"
)

// Record is a completed incident run, read back from its results.Record for
// `labctl incident history`.
type Record struct {
	Fault          string    `json:"fault"`
	Category       string    `json:"category"`
	Severity       string    `json:"severity"`
	Silent         bool      `json:"silent"`
	InjectedAt     time.Time `json:"injectedAt"`
	FirstCheckedAt time.Time `json:"firstCheckedAt,omitempty"`
	ResolvedAt     time.Time `json:"resolvedAt"`
	DetectSeconds  int64     `json:"detectSeconds,omitempty"`
	ResolveSeconds int64     `json:"resolveSeconds"`
	HintsUsed      int       `json:"hintsUsed"`
	ResolvedBy     string    `json:"resolvedBy"`
}

func (e *Engine) resultsStore() *results.Store {
	return results.NewStore(filepath.Join(e.ProjectRoot, ".labctl", "history"))
}

// History returns all recorded incident runs, oldest first.
func (e *Engine) History() ([]Record, error) {
	recs, err := e.resultsStore().ByKind(results.KindIncident)
	if err != nil {
		return nil, err
	}
	var out []Record
	for _, r := range recs {
		rec := incidentRecordFromResult(r)
		out = append(out, rec)
	}
	return out, nil
}

// finishRun appends the run record when an incident ends. user is the
// authenticated API user, or "" to use the OS username.
func (e *Engine) finishRun(active *Active, f *Fault, resolvedBy, user string) {
	now := time.Now().UTC()
	resolveSeconds := int64(now.Sub(active.InjectedAt).Seconds())
	var detectSeconds int64
	if !active.FirstCheckedAt.IsZero() {
		detectSeconds = int64(active.FirstCheckedAt.Sub(active.InjectedAt).Seconds())
	}

	outcome := "resolved"
	if resolvedBy == "auto" {
		outcome = "auto-resolved"
	}

	r := results.Record{
		Kind:      results.KindIncident,
		Name:      f.Name,
		User:      results.UserOr(user),
		StartedAt: active.InjectedAt,
		EndedAt:   now,
		Elapsed:   resolveSeconds,
		Score:     -1, // incidents are not scored unless run as a challenge
		Outcome:   outcome,
		HintsUsed: active.HintsRevealed,
		Meta: map[string]any{
			"category":       f.Category,
			"severity":       f.Severity,
			"silent":         active.Silent,
			"resolvedBy":     resolvedBy,
			"detectSeconds":  detectSeconds,
			"firstCheckedAt": active.FirstCheckedAt,
		},
	}
	// Best effort: history failures must not fail the resolution itself.
	_ = e.resultsStore().Append(r)
}

func incidentRecordFromResult(r results.Record) Record {
	rec := Record{
		Fault:          r.Name,
		InjectedAt:     r.StartedAt,
		ResolvedAt:     r.EndedAt,
		ResolveSeconds: r.Elapsed,
		HintsUsed:      r.HintsUsed,
	}
	if r.Outcome == "auto-resolved" {
		rec.ResolvedBy = "auto"
	} else {
		rec.ResolvedBy = "manual"
	}
	if r.Meta != nil {
		if v, ok := r.Meta["category"].(string); ok {
			rec.Category = v
		}
		if v, ok := r.Meta["severity"].(string); ok {
			rec.Severity = v
		}
		if v, ok := r.Meta["silent"].(bool); ok {
			rec.Silent = v
		}
		if v, ok := r.Meta["detectSeconds"].(float64); ok {
			rec.DetectSeconds = int64(v)
		}
		// Meta values come back from JSON, so the time is an RFC3339 string.
		if v, ok := r.Meta["firstCheckedAt"].(string); ok && v != "" {
			if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
				rec.FirstCheckedAt = t
			}
		}
	}
	return rec
}
