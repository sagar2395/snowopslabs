// SPDX-License-Identifier: Apache-2.0

// Package challenge loads timed challenges from challenges/<name>/, tracks the
// active run, and scores a submission from its checks, the hints used and the
// time taken against par.
package challenge

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/snowops-oss/snowopslabs/internal/results"
	"gopkg.in/yaml.v3"
)

// ErrNoActive reports that no challenge is running. RecordHint, Attempt and
// Complete return it, so a caller can tell "nothing to act on" from a failure.
var ErrNoActive = errors.New("no active challenge")

// SetupSpec says how to prepare the lab before the timer starts.
type SetupSpec struct {
	Type string `yaml:"type" json:"type"`         // scenario | incident
	Ref  string `yaml:"ref,omitempty" json:"ref"` // scenario name or fault name
	// Refs are faults injected together, in order. A challenge sets Ref or
	// Refs, never both.
	Refs []string `yaml:"refs,omitempty" json:"refs,omitempty"`
}

// All returns every scenario or fault the setup names, in setup order.
func (s SetupSpec) All() []string {
	if len(s.Refs) > 0 {
		return s.Refs
	}
	if s.Ref == "" {
		return nil
	}
	return []string{s.Ref}
}

// GradingSpec describes how to compute the score on submit.
type GradingSpec struct {
	UseDetectionCheck bool     `yaml:"useDetectionCheck,omitempty"` // use the fault's detection check
	Checks            []GCheck `yaml:"checks,omitempty"`            // explicit check list
}

// GCheck is a check inline in challenge.yaml (mirrors the checks package schema).
type GCheck struct {
	Name           string `yaml:"name"`
	Type           string `yaml:"type"`
	URL            string `yaml:"url,omitempty"`
	ExpectStatus   int    `yaml:"expectStatus,omitempty"`
	Script         string `yaml:"script,omitempty"`
	Query          string `yaml:"query,omitempty"`
	Operator       string `yaml:"operator,omitempty"`
	Value          string `yaml:"value,omitempty"`
	TimeoutSeconds int    `yaml:"timeoutSeconds,omitempty"`
}

// Challenge is the top-level challenge.yaml schema.
type Challenge struct {
	Name        string      `yaml:"name"`
	DisplayName string      `yaml:"displayName"`
	Description string      `yaml:"description"`
	Category    string      `yaml:"category"`
	ParTime     string      `yaml:"parTime"` // e.g. "10m"
	Setup       SetupSpec   `yaml:"setup"`
	Grading     GradingSpec `yaml:"grading"`
	HintPenalty int         `yaml:"hintPenalty"` // % per hint; default 5
	// Silent keeps the setup's names from the learner until the run ends:
	// finding the faults is the exercise.
	Silent bool `yaml:"silent,omitempty"`
	dir    string
}

// Dir returns the directory this challenge was loaded from.
func (c *Challenge) Dir() string { return c.dir }

// ParDuration parses ParTime as a duration. Returns 0 if unset or unparseable.
func (c *Challenge) ParDuration() time.Duration {
	if c.ParTime == "" {
		return 0
	}
	d, _ := time.ParseDuration(c.ParTime)
	return d
}

// Validate returns any schema errors.
func (c *Challenge) Validate() []string {
	var errs []string
	if c.Name == "" {
		errs = append(errs, "name is required")
	}
	if c.DisplayName == "" {
		errs = append(errs, "displayName is required")
	}
	if c.Setup.Type == "" {
		errs = append(errs, "setup.type is required")
	}
	errs = append(errs, c.Setup.validate()...)
	if !c.Grading.UseDetectionCheck && len(c.Grading.Checks) == 0 {
		errs = append(errs, "grading: either useDetectionCheck or checks must be set")
	}
	if c.ParTime != "" {
		if _, err := time.ParseDuration(c.ParTime); err != nil {
			errs = append(errs, "parTime: invalid duration format (use e.g. 10m, 1h)")
		}
	}
	return errs
}

// validate checks the setup names its content once: by ref, or by a list of
// distinct faults in refs.
func (s SetupSpec) validate() []string {
	switch {
	case s.Ref == "" && len(s.Refs) == 0:
		return []string{"setup.ref is required (or setup.refs, for several faults)"}
	case s.Ref != "" && len(s.Refs) > 0:
		return []string{"setup: set ref or refs, not both"}
	case len(s.Refs) == 0:
		return nil
	case s.Type != "incident":
		return []string{"setup.refs: only incidents can be set up together"}
	case len(s.Refs) < 2:
		return []string{"setup.refs: list two or more faults, or use setup.ref for one"}
	}
	seen := map[string]bool{}
	for _, r := range s.Refs {
		if r == "" || seen[r] {
			return []string{fmt.Sprintf("setup.refs: %q is empty or listed twice", r)}
		}
		seen[r] = true
	}
	return nil
}

// ActiveRun is the state of a currently running challenge.
type ActiveRun struct {
	ChallengeName string    `json:"challenge"`
	StartedAt     time.Time `json:"startedAt"`
	HintsUsed     int       `json:"hintsUsed"`
}

// Elapsed returns how long the challenge has been running.
func (a *ActiveRun) Elapsed() time.Duration {
	return time.Since(a.StartedAt)
}

// RunRecord is the persisted result of a completed challenge run.
type RunRecord struct {
	ChallengeName string        `json:"challenge"`
	StartedAt     time.Time     `json:"startedAt"`
	FinishedAt    time.Time     `json:"finishedAt"`
	Elapsed       time.Duration `json:"elapsedSeconds"` // JSON value is nanoseconds, despite the key
	HintsUsed     int           `json:"hintsUsed"`
	ChecksPassed  int           `json:"checksPassed"`
	ChecksTotal   int           `json:"checksTotal"`
	Score         int           `json:"score"`   // 0–100
	Outcome       string        `json:"outcome"` // passed | failed | aborted
}

// Engine manages challenges.
type Engine struct {
	challengeDir string
	stateDir     string
	resultsDir   string // directory for the unified results store
	now          func() time.Time
}

// New creates an Engine. resultsDir is the directory holding results.jsonl
// (usually the lab state directory's history/); pass "" to skip writing results records.
func New(challengeDir, stateDir, resultsDir string) *Engine {
	return &Engine{
		challengeDir: challengeDir,
		stateDir:     stateDir,
		resultsDir:   resultsDir,
		now:          time.Now,
	}
}

// Challenges returns all valid challenges.
func (e *Engine) Challenges() ([]*Challenge, error) {
	entries, err := os.ReadDir(e.challengeDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []*Challenge
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		c, err := e.Load(entry.Name())
		if err != nil {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

// Load loads and validates a single challenge by name.
func (e *Engine) Load(name string) (*Challenge, error) {
	dir := filepath.Join(e.challengeDir, name)
	data, err := os.ReadFile(filepath.Join(dir, "challenge.yaml"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, &notFoundError{name: name}
	}
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", name, err)
	}
	var c Challenge
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("challenge %s is malformed (run 'labctl validate'): %w", name, err)
	}
	c.dir = dir
	if errs := c.Validate(); len(errs) > 0 {
		return nil, fmt.Errorf("challenge %s is malformed (run 'labctl validate'): %s", name, strings.Join(errs, "; "))
	}
	return &c, nil
}

// notFoundError reports a challenge that does not exist. It matches
// fs.ErrNotExist, and its text names the command that lists the ones that do.
type notFoundError struct{ name string }

func (e *notFoundError) Error() string {
	return fmt.Sprintf("challenge %q not found (see 'labctl challenge list')", e.name)
}

func (e *notFoundError) Is(target error) bool { return target == fs.ErrNotExist }

// Active returns the current active run, or nil if none.
func (e *Engine) Active() (*ActiveRun, error) {
	data, err := os.ReadFile(e.activeFile())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var run ActiveRun
	if err := json.Unmarshal(data, &run); err != nil {
		return nil, err
	}
	return &run, nil
}

// Start begins a challenge run. The caller must run the setup action
// (scenario up or incident inject) first.
func (e *Engine) Start(name string, force bool) (*ActiveRun, error) {
	if _, err := e.Load(name); err != nil {
		return nil, err
	}
	existing, err := e.Active()
	if err != nil {
		return nil, err
	}
	if existing != nil && !force {
		return nil, fmt.Errorf("challenge %q is already active - use --force to override", existing.ChallengeName)
	}
	if err := os.MkdirAll(e.stateDir, 0o755); err != nil {
		return nil, err
	}
	run := &ActiveRun{
		ChallengeName: name,
		StartedAt:     e.now(),
	}
	return run, e.saveActive(run)
}

// RecordHint increments the hint counter on the active run.
func (e *Engine) RecordHint() error {
	run, err := e.Active()
	if err != nil {
		return err
	}
	if run == nil {
		return ErrNoActive
	}
	run.HintsUsed++
	return e.saveActive(run)
}

// Attempt scores the active run without recording or ending it, so a learner
// can fix the failing checks and submit again. The clock keeps running, so
// each retry costs time.
func (e *Engine) Attempt(passed, total int) (*RunRecord, error) {
	run, err := e.Active()
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, ErrNoActive
	}
	c, err := e.Load(run.ChallengeName)
	if err != nil {
		return nil, err
	}
	elapsed := e.now().Sub(run.StartedAt)
	return &RunRecord{
		ChallengeName: run.ChallengeName,
		StartedAt:     run.StartedAt,
		FinishedAt:    e.now(),
		Elapsed:       elapsed,
		HintsUsed:     run.HintsUsed,
		ChecksPassed:  passed,
		ChecksTotal:   total,
		Score:         computeScore(c, elapsed, run.HintsUsed, passed, total),
		Outcome:       "failed",
	}, nil
}

// Complete records the result and clears the active run. passed and total are
// the check counts; outcome is "passed", "failed" or "aborted". user is the
// authenticated API user, or "" to use the OS username.
func (e *Engine) Complete(passed, total int, outcome, user string) (*RunRecord, error) {
	run, err := e.Active()
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, ErrNoActive
	}
	c, err := e.Load(run.ChallengeName)
	if err != nil {
		return nil, err
	}
	now := e.now()
	elapsed := now.Sub(run.StartedAt)
	score := computeScore(c, elapsed, run.HintsUsed, passed, total)
	rec := &RunRecord{
		ChallengeName: run.ChallengeName,
		StartedAt:     run.StartedAt,
		FinishedAt:    now,
		Elapsed:       elapsed,
		HintsUsed:     run.HintsUsed,
		ChecksPassed:  passed,
		ChecksTotal:   total,
		Score:         score,
		Outcome:       outcome,
	}
	if err := e.appendHistory(rec); err != nil {
		return rec, err
	}
	// Best effort: a failed results write does not fail the run.
	if e.resultsDir != "" {
		r := results.Record{
			Kind:      results.KindChallenge,
			Name:      rec.ChallengeName,
			User:      results.UserOr(user),
			StartedAt: rec.StartedAt,
			EndedAt:   rec.FinishedAt,
			Elapsed:   int64(elapsed.Seconds()),
			Score:     rec.Score,
			Outcome:   rec.Outcome,
			HintsUsed: rec.HintsUsed,
			Meta: map[string]any{
				"checksPassed": rec.ChecksPassed,
				"checksTotal":  rec.ChecksTotal,
			},
		}
		_ = results.NewStore(e.resultsDir).Append(r)
	}
	return rec, os.Remove(e.activeFile())
}

// History returns all past run records in chronological order.
func (e *Engine) History() ([]RunRecord, error) {
	data, err := os.ReadFile(e.historyFile())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var recs []RunRecord
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var r RunRecord
		if err := json.Unmarshal([]byte(line), &r); err == nil {
			recs = append(recs, r)
		}
	}
	return recs, nil
}

// computeScore returns a run's score out of 100: 100 minus the hint penalty
// and the time-over-par penalty (at most 20), never below 0, then scaled by the
// share of checks that passed.
func computeScore(c *Challenge, elapsed time.Duration, hintsUsed, passed, total int) int {
	if total == 0 {
		return 0
	}
	if passed == 0 {
		return 0
	}

	base := 100
	penalty := c.HintPenalty
	if penalty == 0 {
		penalty = 5
	}
	hintDeduction := hintsUsed * penalty

	timePenalty := 0
	par := c.ParDuration()
	if par > 0 && elapsed > par {
		over := elapsed - par
		overFrac := float64(over) / float64(par)
		timePenalty = min(int(overFrac*20), 20)
	}

	score := max(base-hintDeduction-timePenalty, 0)

	// Scale by check pass rate if not all passed.
	if passed < total {
		score = score * passed / total
	}
	return score
}

func (e *Engine) activeFile() string {
	return filepath.Join(e.stateDir, "active.json")
}

func (e *Engine) historyFile() string {
	return filepath.Join(e.stateDir, "history.jsonl")
}

func (e *Engine) saveActive(run *ActiveRun) error {
	data, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(e.activeFile(), append(data, '\n'), 0o644)
}

func (e *Engine) appendHistory(rec *RunRecord) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(e.historyFile(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = fmt.Fprintf(f, "%s\n", data)
	return err
}
