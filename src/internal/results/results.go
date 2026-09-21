// SPDX-License-Identifier: Apache-2.0

// Package results stores the outcome of every scored run (incidents,
// challenges, learning modules, scenario verifications, comparisons) as one
// JSON record per line in .labctl/history/results.jsonl. The leaderboard is
// built from these records.
package results

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Kind identifies the type of run.
const (
	KindIncident  = "incident"
	KindChallenge = "challenge"
	KindModule    = "module"   // a learn path module completion
	KindScenario  = "scenario" // a scenario verification
	// KindComparison is one workload's results from a `labctl compare`. The
	// records that share a scenario and run ID make up one comparison.
	KindComparison = "comparison"
)

// CheckOutcome is one check's result in a scenario verification record: a
// short summary of checks.Result, so this package does not depend on checks.
type CheckOutcome struct {
	Name   string `json:"name"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail,omitempty"`
}

// NewScenarioRecord builds a Record for a scenario verification, including the
// scenario's objectives and each check's result. Score is the percentage of
// checks that passed; Outcome is "passed" only when all of them did.
func NewScenarioRecord(name, user string, objectives []string, checks []CheckOutcome, startedAt, endedAt time.Time) Record {
	passed := 0
	for _, c := range checks {
		if c.Pass {
			passed++
		}
	}
	outcome := "failed"
	score := -1
	if len(checks) > 0 {
		if passed == len(checks) {
			outcome = "passed"
		}
		score = passed * 100 / len(checks)
	}
	meta := map[string]any{
		"checks":       checks,
		"checksPassed": passed,
		"checksTotal":  len(checks),
	}
	if len(objectives) > 0 {
		meta["objectives"] = objectives
	}
	return Record{
		Kind:      KindScenario,
		Name:      name,
		User:      UserOr(user),
		StartedAt: startedAt,
		EndedAt:   endedAt,
		Elapsed:   int64(endedAt.Sub(startedAt).Seconds()),
		Score:     score,
		Outcome:   outcome,
		Meta:      meta,
	}
}

// Record is the unified run record.
type Record struct {
	Kind      string         `json:"kind"`           // incident | challenge | module
	Name      string         `json:"name"`           // fault name, challenge name, or "<path>/<module>"
	User      string         `json:"user,omitempty"` // $USER at run time
	StartedAt time.Time      `json:"startedAt"`
	EndedAt   time.Time      `json:"endedAt"`
	Elapsed   int64          `json:"elapsedSeconds"` // wall clock seconds
	Score     int            `json:"score"`          // 0–100; -1 = not scored
	Outcome   string         `json:"outcome"`        // passed | failed | aborted | resolved | auto-resolved
	HintsUsed int            `json:"hintsUsed,omitempty"`
	Workload  string         `json:"workload,omitempty"` // app the run was bound to (ADR-0014)
	Meta      map[string]any `json:"meta,omitempty"`     // kind-specific extra fields
}

// NewComparisonRecord builds a Record for one workload in a comparison. It is
// not scored: Score is -1 and Outcome is "measured". The comparison's
// conditions (controls) are stored with the values, since results measured
// under different conditions cannot be compared.
func NewComparisonRecord(scenario, app string, values map[string]float64, controls map[string]string, startedAt, endedAt time.Time) Record {
	return Record{
		Kind:      KindComparison,
		Name:      scenario,
		Workload:  app,
		User:      UserOr(""),
		StartedAt: startedAt,
		EndedAt:   endedAt,
		Elapsed:   int64(endedAt.Sub(startedAt).Seconds()),
		Score:     -1,
		Outcome:   "measured",
		Meta: map[string]any{
			"metrics":  values,
			"controls": controls,
		},
	}
}

// Store is a JSONL append-only results store.
type Store struct {
	path string // full path to the .jsonl file
}

// NewStore creates a Store backed by <dir>/results.jsonl.
func NewStore(dir string) *Store {
	return &Store{path: filepath.Join(dir, "results.jsonl")}
}

// Append writes a record to the store (creates the file if needed).
func (s *Store) Append(r Record) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = fmt.Fprintf(f, "%s\n", data)
	return err
}

// All returns all records in chronological order.
func (s *Store) All() ([]Record, error) {
	return s.query("")
}

// ByKind returns all records for the given kind, in chronological order.
func (s *Store) ByKind(kind string) ([]Record, error) {
	return s.query(kind)
}

func (s *Store) query(kind string) ([]Record, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var recs []Record
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var r Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			continue // skip corrupt lines
		}
		if kind != "" && r.Kind != kind {
			continue
		}
		recs = append(recs, r)
	}
	sort.Slice(recs, func(i, j int) bool {
		return recs[i].StartedAt.Before(recs[j].StartedAt)
	})
	return recs, nil
}

// Progress returns the latest module completion record per path/module name,
// giving a snapshot of current learn progress.
func (s *Store) Progress() (map[string][]Record, error) {
	recs, err := s.ByKind(KindModule)
	if err != nil {
		return nil, err
	}
	byPath := map[string][]Record{}
	for _, r := range recs {
		// Name format: "<path>/<module>"
		parts := strings.SplitN(r.Name, "/", 2)
		if len(parts) != 2 {
			continue
		}
		path := parts[0]
		byPath[path] = append(byPath[path], r)
	}
	return byPath, nil
}

// CurrentUser returns the OS username ($USER, then $USERNAME), or "unknown".
func CurrentUser() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	if u := os.Getenv("USERNAME"); u != "" {
		return u
	}
	return "unknown"
}

// UserOr returns user if it is non-empty, otherwise CurrentUser(). Callers
// pass the authenticated API user, or "" when there is none.
func UserOr(user string) string {
	if user != "" {
		return user
	}
	return CurrentUser()
}
