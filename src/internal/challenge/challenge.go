// SPDX-License-Identifier: Apache-2.0
package challenge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sagar2395/snowopslabs/internal/results"
	"gopkg.in/yaml.v3"
)

// SetupSpec says how to prepare the lab before the timer starts.
type SetupSpec struct {
	Type string `yaml:"type"` // scenario | incident
	Ref  string `yaml:"ref"`  // scenario name or fault name
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
	dir         string
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
	if c.Setup.Ref == "" {
		errs = append(errs, "setup.ref is required")
	}
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
	Elapsed       time.Duration `json:"elapsedSeconds"` // stored as nanoseconds but labelled
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

// New creates a Engine.  resultsDir is the directory containing results.jsonl
// (typically .labctl/history); pass empty string to disable unified store writes.
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
		if os.IsNotExist(err) {
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
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", name, err)
	}
	var c Challenge
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", name, err)
	}
	c.dir = dir
	if errs := c.Validate(); len(errs) > 0 {
		return nil, fmt.Errorf("validate %s: %s", name, strings.Join(errs, "; "))
	}
	return &c, nil
}

// Active returns the current active run, or nil if none.
func (e *Engine) Active() (*ActiveRun, error) {
	data, err := os.ReadFile(e.activeFile())
	if err != nil {
		if os.IsNotExist(err) {
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

// Start begins a challenge run.  The caller is responsible for running the
// setup action (scenario up / incident inject) before calling Start.
func (e *Engine) Start(name string, force bool) (*ActiveRun, error) {
	if _, err := e.Load(name); err != nil {
		return nil, err
	}
	existing, err := e.Active()
	if err != nil {
		return nil, err
	}
	if existing != nil && !force {
		return nil, fmt.Errorf("challenge %q is already active — use --force to override", existing.ChallengeName)
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
		return fmt.Errorf("no active challenge")
	}
	run.HintsUsed++
	return e.saveActive(run)
}

// Complete records the result and clears active state.
// passed/total are the check results. outcome is "passed", "failed", or "aborted".
// user attributes the unified result record to the authenticated API user
// (task 062); pass "" from the CLI to fall back to the OS username.
func (e *Engine) Complete(passed, total int, outcome, user string) (*RunRecord, error) {
	run, err := e.Active()
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, fmt.Errorf("no active challenge")
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
	// Also write to the unified results store (best effort).
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
			Meta: map[string]interface{}{
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
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var recs []RunRecord
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
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

// computeScore calculates the score for a challenge run.
// Formula: base=100, subtract hint penalties, subtract time over-par.
// If not all checks pass, score is proportional to checks passed.
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
		timePenalty = int(overFrac * 20)
		if timePenalty > 20 {
			timePenalty = 20
		}
	}

	score := base - hintDeduction - timePenalty
	if score < 0 {
		score = 0
	}

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
