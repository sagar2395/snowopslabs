// SPDX-License-Identifier: Apache-2.0

package results

import (
	"encoding/csv"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Incident outcomes. A run ended with `labctl incident resolve` is
// auto-resolved: the escape hatch put the lab back, not the learner.
const (
	OutcomeResolved     = "resolved"
	OutcomeAutoResolved = "auto-resolved"
)

// How an incident ended, in an export row and on a card.
const (
	ResolvedByFix         = "fix"
	ResolvedByEscapeHatch = "escape-hatch"
)

// Kinds lists every record kind, in the order a person reads them.
var Kinds = []string{KindIncident, KindChallenge, KindScenario, KindModule, KindComparison}

// ExportRow is one run as `labctl results export` writes it. Its fields are
// documented in docs/reference/cli/incidents.md, so a field may be added but
// never renamed or removed.
type ExportRow struct {
	Kind           string    `json:"kind"`
	Name           string    `json:"name"`
	User           string    `json:"user"`
	Workload       string    `json:"workload"`
	StartedAt      time.Time `json:"startedAt"`
	EndedAt        time.Time `json:"endedAt"`
	ElapsedSeconds int64     `json:"elapsedSeconds"`
	HintsUsed      int       `json:"hintsUsed"`
	// Score is 0-100, or nil for a run that is not scored.
	Score   *int   `json:"score"`
	Outcome string `json:"outcome"`
	// ResolvedBy is ResolvedByFix or ResolvedByEscapeHatch for an incident,
	// and empty for every other kind.
	ResolvedBy string `json:"resolvedBy"`
}

// ExportColumns are the CSV header, in the order of ExportRow's fields.
var ExportColumns = []string{
	"kind", "name", "user", "workload", "startedAt", "endedAt",
	"elapsedSeconds", "hintsUsed", "score", "outcome", "resolvedBy",
}

// ExportFilter narrows Export. The zero value keeps every record. A record is
// kept when it started at or after Since and before Until.
type ExportFilter struct {
	Kind  string
	Since time.Time
	Until time.Time
}

// Validate rejects a kind that no record can have, naming the ones that exist.
func (f ExportFilter) Validate() error {
	if f.Kind != "" && !slices.Contains(Kinds, f.Kind) {
		return fmt.Errorf("unknown kind %q: use one of %s", f.Kind, strings.Join(Kinds, ", "))
	}
	if !f.Since.IsZero() && !f.Until.IsZero() && !f.Since.Before(f.Until) {
		return fmt.Errorf("the time range is empty: --since %s is not before --until %s",
			f.Since.Format(time.RFC3339), f.Until.Format(time.RFC3339))
	}
	return nil
}

// Export returns the records f keeps as export rows, in the order given.
func Export(recs []Record, f ExportFilter) []ExportRow {
	rows := []ExportRow{}
	for _, r := range recs {
		if f.Kind != "" && r.Kind != f.Kind {
			continue
		}
		if !f.Since.IsZero() && r.StartedAt.Before(f.Since) {
			continue
		}
		if !f.Until.IsZero() && !r.StartedAt.Before(f.Until) {
			continue
		}
		row := ExportRow{
			Kind:           r.Kind,
			Name:           r.Name,
			User:           r.User,
			Workload:       r.Workload,
			StartedAt:      r.StartedAt,
			EndedAt:        r.EndedAt,
			ElapsedSeconds: r.Elapsed,
			HintsUsed:      r.HintsUsed,
			Outcome:        r.Outcome,
			ResolvedBy:     resolvedBy(r),
		}
		if r.Score >= 0 {
			score := r.Score
			row.Score = &score
		}
		rows = append(rows, row)
	}
	return rows
}

// resolvedBy says how an incident ended, and is empty for other kinds.
func resolvedBy(r Record) string {
	switch {
	case r.Kind != KindIncident:
		return ""
	case r.Outcome == OutcomeAutoResolved:
		return ResolvedByEscapeHatch
	default:
		return ResolvedByFix
	}
}

// WriteCSV writes rows as CSV with an ExportColumns header. Times are RFC 3339
// in UTC and a run that is not scored has an empty score.
func WriteCSV(w io.Writer, rows []ExportRow) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(ExportColumns); err != nil {
		return err
	}
	for _, r := range rows {
		score := ""
		if r.Score != nil {
			score = strconv.Itoa(*r.Score)
		}
		if err := cw.Write([]string{
			cell(r.Kind), cell(r.Name), cell(r.User), cell(r.Workload),
			r.StartedAt.UTC().Format(time.RFC3339), r.EndedAt.UTC().Format(time.RFC3339),
			strconv.FormatInt(r.ElapsedSeconds, 10), strconv.Itoa(r.HintsUsed), score,
			cell(r.Outcome), cell(r.ResolvedBy),
		}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// cell keeps a text value from being read as a formula when the CSV is
// opened in a spreadsheet: one that starts with = + - @ gets a leading '.
func cell(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		return "'" + v
	}
	return v
}
