// SPDX-License-Identifier: Apache-2.0
package challenge

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const validChallengeYAML = `
name: test-challenge
displayName: "Test Challenge"
description: "A test challenge"
category: workload
parTime: "10m"
setup:
  type: incident
  ref: bad-deploy-rollout
grading:
  useDetectionCheck: true
hintPenalty: 5
`

func makeEngine(t *testing.T) (*Engine, string) {
	t.Helper()
	dir := t.TempDir()
	cDir := filepath.Join(dir, "challenges")
	sDir := filepath.Join(dir, ".labctl", "challenges")
	if err := os.MkdirAll(cDir, 0o755); err != nil {
		t.Fatal(err)
	}
	e := New(cDir, sDir, "")
	fixed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	e.now = func() time.Time { return fixed }
	return e, cDir
}

func writeChallengeYAML(t *testing.T, cDir, name, content string) {
	t.Helper()
	dir := filepath.Join(cDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "challenge.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoad_Valid(t *testing.T) {
	e, cDir := makeEngine(t)
	writeChallengeYAML(t, cDir, "test-challenge", validChallengeYAML)
	c, err := e.Load("test-challenge")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Name != "test-challenge" {
		t.Errorf("name = %q", c.Name)
	}
	if c.Setup.Ref != "bad-deploy-rollout" {
		t.Errorf("setup.ref = %q", c.Setup.Ref)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	e, _ := makeEngine(t)
	_, err := e.Load("nonexistent")
	if err == nil {
		t.Fatal("expected error for missing challenge")
	}
}

func TestValidate_MissingGrading(t *testing.T) {
	c := &Challenge{
		Name:        "bad",
		DisplayName: "Bad",
		Setup:       SetupSpec{Type: "incident", Ref: "x"},
		Grading:     GradingSpec{}, // neither useDetectionCheck nor checks
	}
	errs := c.Validate()
	if len(errs) == 0 {
		t.Fatal("expected validation error for missing grading spec")
	}
}

func TestValidate_BadParTime(t *testing.T) {
	c := &Challenge{
		Name:        "bad",
		DisplayName: "Bad",
		Setup:       SetupSpec{Type: "incident", Ref: "x"},
		Grading:     GradingSpec{UseDetectionCheck: true},
		ParTime:     "not-a-duration",
	}
	errs := c.Validate()
	found := false
	for _, e := range errs {
		if e == "parTime: invalid duration format (use e.g. 10m, 1h)" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected parTime error, got: %v", errs)
	}
}

func TestChallenges_Empty(t *testing.T) {
	e, _ := makeEngine(t)
	cs, err := e.Challenges()
	if err != nil {
		t.Fatalf("Challenges: %v", err)
	}
	if len(cs) != 0 {
		t.Errorf("expected 0 challenges, got %d", len(cs))
	}
}

func TestChallenges_SkipsMalformed(t *testing.T) {
	e, cDir := makeEngine(t)
	writeChallengeYAML(t, cDir, "good", validChallengeYAML)
	writeChallengeYAML(t, cDir, "bad", "invalid: yaml: {{{{")
	cs, err := e.Challenges()
	if err != nil {
		t.Fatalf("Challenges: %v", err)
	}
	if len(cs) != 1 {
		t.Errorf("expected 1 valid challenge, got %d", len(cs))
	}
}

func TestActiveRun_Lifecycle(t *testing.T) {
	e, cDir := makeEngine(t)
	writeChallengeYAML(t, cDir, "test-challenge", validChallengeYAML)

	// No active run initially.
	active, err := e.Active()
	if err != nil {
		t.Fatalf("Active: %v", err)
	}
	if active != nil {
		t.Fatal("expected no active run")
	}

	// Start.
	run, err := e.Start("test-challenge", false)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if run.ChallengeName != "test-challenge" {
		t.Errorf("challenge name = %q", run.ChallengeName)
	}

	// Active returns it.
	active, err = e.Active()
	if err != nil {
		t.Fatalf("Active after start: %v", err)
	}
	if active == nil {
		t.Fatal("expected active run after start")
	}
}

func TestStart_AlreadyActive(t *testing.T) {
	e, cDir := makeEngine(t)
	writeChallengeYAML(t, cDir, "test-challenge", validChallengeYAML)
	_, _ = e.Start("test-challenge", false)
	_, err := e.Start("test-challenge", false)
	if err == nil {
		t.Fatal("expected error when starting challenge while one is already active")
	}
}

func TestStart_Force(t *testing.T) {
	e, cDir := makeEngine(t)
	writeChallengeYAML(t, cDir, "test-challenge", validChallengeYAML)
	_, _ = e.Start("test-challenge", false)
	_, err := e.Start("test-challenge", true)
	if err != nil {
		t.Fatalf("Force start: %v", err)
	}
}

func TestRecordHint(t *testing.T) {
	e, cDir := makeEngine(t)
	writeChallengeYAML(t, cDir, "test-challenge", validChallengeYAML)
	_, _ = e.Start("test-challenge", false)
	if err := e.RecordHint(); err != nil {
		t.Fatalf("RecordHint: %v", err)
	}
	active, _ := e.Active()
	if active.HintsUsed != 1 {
		t.Errorf("hints = %d, want 1", active.HintsUsed)
	}
}

func TestComplete_RecordsHistory(t *testing.T) {
	e, cDir := makeEngine(t)
	writeChallengeYAML(t, cDir, "test-challenge", validChallengeYAML)
	_, _ = e.Start("test-challenge", false)
	_ = e.RecordHint()

	rec, err := e.Complete(2, 2, "passed", "")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if rec.ChecksPassed != 2 {
		t.Errorf("checks passed = %d", rec.ChecksPassed)
	}
	if rec.HintsUsed != 1 {
		t.Errorf("hints used = %d", rec.HintsUsed)
	}
	if rec.Score <= 0 || rec.Score > 100 {
		t.Errorf("score %d out of range", rec.Score)
	}

	// Active should be cleared.
	active, _ := e.Active()
	if active != nil {
		t.Error("expected no active run after complete")
	}

	// History should have a record.
	history, err := e.History()
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("expected 1 history record, got %d", len(history))
	}
	if history[0].Outcome != "passed" {
		t.Errorf("outcome = %q", history[0].Outcome)
	}
}

func TestComputeScore_AllPass_NoPenalty(t *testing.T) {
	c := &Challenge{ParTime: "10m", HintPenalty: 5}
	elapsed := 5 * time.Minute // under par
	score := computeScore(c, elapsed, 0, 2, 2)
	if score != 100 {
		t.Errorf("expected 100, got %d", score)
	}
}

func TestComputeScore_HintPenalty(t *testing.T) {
	c := &Challenge{ParTime: "10m", HintPenalty: 5}
	score := computeScore(c, 5*time.Minute, 3, 2, 2)
	// 100 - 3*5 = 85
	if score != 85 {
		t.Errorf("expected 85, got %d", score)
	}
}

func TestComputeScore_TimePenalty(t *testing.T) {
	c := &Challenge{ParTime: "10m", HintPenalty: 5}
	score := computeScore(c, 20*time.Minute, 0, 2, 2) // 2x par = 20% penalty
	// 100 - 0 - 20 = 80
	if score != 80 {
		t.Errorf("expected 80, got %d", score)
	}
}

func TestComputeScore_PartialChecks(t *testing.T) {
	c := &Challenge{ParTime: "10m", HintPenalty: 5}
	score := computeScore(c, 5*time.Minute, 0, 1, 2) // 1/2 checks pass
	// base = 100, scaled by 1/2 = 50
	if score != 50 {
		t.Errorf("expected 50, got %d", score)
	}
}

func TestComputeScore_NoChecksPass(t *testing.T) {
	c := &Challenge{ParTime: "10m", HintPenalty: 5}
	score := computeScore(c, 5*time.Minute, 0, 0, 2)
	if score != 0 {
		t.Errorf("expected 0, got %d", score)
	}
}

func TestParDuration(t *testing.T) {
	c := &Challenge{ParTime: "15m"}
	d := c.ParDuration()
	if d != 15*time.Minute {
		t.Errorf("expected 15m, got %v", d)
	}
}

func TestHistory_EmptyIsNil(t *testing.T) {
	e, _ := makeEngine(t)
	h, err := e.History()
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if h != nil {
		t.Error("expected nil history when no runs")
	}
}
