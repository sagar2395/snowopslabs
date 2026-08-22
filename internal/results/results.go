// SPDX-License-Identifier: Apache-2.0
// Package results provides a unified, append-only store for all scored runs
// (incidents, challenge submissions, learn module completions).  Records are
// written as newline-delimited JSON in .labctl/history/results.jsonl.
//
// Schema is intentionally flat so future team / leaderboard features (task 063)
// can query the file without a migration.
package results

import (
	"encoding/json"
	"fmt"
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
	KindModule    = "module" // a learn path module completion
)

// Record is the unified run record.
type Record struct {
	Kind      string                 `json:"kind"`           // incident | challenge | module
	Name      string                 `json:"name"`           // fault name, challenge name, or "<path>/<module>"
	User      string                 `json:"user,omitempty"` // $USER at run time
	StartedAt time.Time              `json:"startedAt"`
	EndedAt   time.Time              `json:"endedAt"`
	Elapsed   int64                  `json:"elapsedSeconds"` // wall clock seconds
	Score     int                    `json:"score"`          // 0–100; -1 = not scored
	Outcome   string                 `json:"outcome"`        // passed | failed | aborted | resolved | auto-resolved
	HintsUsed int                    `json:"hintsUsed,omitempty"`
	Meta      map[string]interface{} `json:"meta,omitempty"` // kind-specific extra fields
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
	defer f.Close()
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
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var recs []Record
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
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

// currentUser returns the OS username for the record, falling back to "unknown".
func CurrentUser() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	if u := os.Getenv("USERNAME"); u != "" {
		return u
	}
	return "unknown"
}

// UserOr returns user when it is non-empty, otherwise the OS username from
// CurrentUser(). Callers pass the authenticated API user (task 062) when known
// and "" otherwise, so auth-off / CLI behaviour stays byte-identical.
func UserOr(user string) string {
	if user != "" {
		return user
	}
	return CurrentUser()
}
