// SPDX-License-Identifier: Apache-2.0

package compare

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Store keeps comparison reports in an append-only JSONL file. Each line is a
// whole report with its conditions, so results from different runs are never
// mixed.
type Store struct{ path string }

// NewStore returns a store backed by <dir>/comparisons.jsonl.
func NewStore(dir string) *Store {
	return &Store{path: filepath.Join(dir, "comparisons.jsonl")}
}

// Path is where the history lives, for messages that tell a user where to look.
func (s *Store) Path() string { return s.path }

// stored wraps a report with an id, so one run can be addressed later.
type stored struct {
	ID string `json:"id"`
	*Report
}

// ID is a run's stable, sortable handle: the start time plus the scenario.
func reportID(r *Report) string {
	return r.StartedAt.UTC().Format("20060102-150405") + "-" + r.Scenario
}

// Append records a report. A report with no measurements is not recorded.
func (s *Store) Append(r *Report) (string, error) {
	if r == nil || len(r.Measurements) == 0 {
		return "", nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return "", err
	}
	id := reportID(r)
	line, err := json.Marshal(stored{ID: id, Report: r})
	if err != nil {
		return "", err
	}
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		_ = f.Close()
		return "", err
	}
	// Close explicitly and check the error: a failed write can surface only
	// at Close.
	if err := f.Close(); err != nil {
		return "", err
	}
	return id, nil
}

// List returns every recorded report, newest first.
//
// A line that does not parse, such as a truncated write, is skipped.
func (s *Store) List() ([]*Report, []string, error) {
	f, err := os.Open(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = f.Close() }()

	var reps []*Report
	var ids []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var st stored
		if err := json.Unmarshal([]byte(line), &st); err != nil || st.Report == nil {
			continue
		}
		reps = append(reps, st.Report)
		ids = append(ids, st.ID)
	}
	if err := sc.Err(); err != nil {
		return nil, nil, err
	}
	// Newest first, keeping ids aligned with reports.
	for i, j := 0, len(reps)-1; i < j; i, j = i+1, j-1 {
		reps[i], reps[j] = reps[j], reps[i]
		ids[i], ids[j] = ids[j], ids[i]
	}
	return reps, ids, nil
}

// Get returns one report by id, or by prefix when that is unambiguous.
func (s *Store) Get(id string) (*Report, error) {
	reps, ids, err := s.List()
	if err != nil {
		return nil, err
	}
	var found *Report
	matches := 0
	for i, got := range ids {
		if got == id {
			return reps[i], nil
		}
		if strings.HasPrefix(got, id) {
			found = reps[i]
			matches++
		}
	}
	switch {
	case matches == 1:
		return found, nil
	case matches > 1:
		return nil, fmt.Errorf("comparison %q is ambiguous — %d runs share that prefix", id, matches)
	}
	return nil, fmt.Errorf("no comparison %q (see 'labctl compare list')", id)
}

// Summary is one row of the history listing.
type Summary struct {
	ID       string
	Scenario string
	Apps     []string
	When     time.Time
}

// Summaries lists the history for display, newest first.
func (s *Store) Summaries() ([]Summary, error) {
	reps, ids, err := s.List()
	if err != nil {
		return nil, err
	}
	out := make([]Summary, 0, len(reps))
	for i, r := range reps {
		out = append(out, Summary{ID: ids[i], Scenario: r.Scenario, Apps: r.Apps, When: r.StartedAt})
	}
	return out, nil
}
