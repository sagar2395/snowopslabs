// SPDX-License-Identifier: Apache-2.0
package challenge

import (
	"strings"
	"testing"
	"time"
)

// TestComputeScore covers the scoring formula edge cases.
func TestComputeScore(t *testing.T) {
	tests := []struct {
		name      string
		c         *Challenge
		elapsed   time.Duration
		hints     int
		passed    int
		total     int
		wantExact int
	}{
		{
			name:    "total zero returns 0",
			c:       &Challenge{},
			elapsed: 0, hints: 0, passed: 0, total: 0,
			wantExact: 0,
		},
		{
			name:    "passed zero returns 0",
			c:       &Challenge{},
			elapsed: 0, hints: 0, passed: 0, total: 5,
			wantExact: 0,
		},
		{
			name:    "all passed with zero hints gives 100",
			c:       &Challenge{},
			elapsed: 0, hints: 0, passed: 3, total: 3,
			wantExact: 100,
		},
		{
			name:    "hints deduct penalty",
			c:       &Challenge{HintPenalty: 10},
			elapsed: 0, hints: 2, passed: 3, total: 3,
			wantExact: 80, // 100 - 2*10 = 80
		},
		{
			name:    "default hint penalty is 5",
			c:       &Challenge{HintPenalty: 0},
			elapsed: 0, hints: 4, passed: 3, total: 3,
			wantExact: 80, // 100 - 4*5 = 80
		},
		{
			name:    "score never goes below 0",
			c:       &Challenge{HintPenalty: 50},
			elapsed: 0, hints: 10, passed: 3, total: 3,
			wantExact: 0, // 100 - 10*50 = -400, clamped to 0
		},
		{
			name:    "time penalty capped at 20",
			c:       &Challenge{ParTime: "5m"},
			elapsed: 100 * time.Minute, hints: 0, passed: 3, total: 3,
			wantExact: 80, // time penalty capped at 20 → 100-20=80
		},
		{
			name:    "within par time no time penalty",
			c:       &Challenge{ParTime: "30m"},
			elapsed: 10 * time.Minute, hints: 0, passed: 3, total: 3,
			wantExact: 100,
		},
		{
			name:    "partial passes scale score",
			c:       &Challenge{},
			elapsed: 0, hints: 0, passed: 1, total: 2,
			wantExact: 50, // 100 * 1/2 = 50
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeScore(tt.c, tt.elapsed, tt.hints, tt.passed, tt.total)
			if got != tt.wantExact {
				t.Errorf("computeScore() = %d, want %d", got, tt.wantExact)
			}
		})
	}
}

// TestChallengeValidate covers required-field validation.
func TestChallengeValidate(t *testing.T) {
	validChallenge := func() *Challenge {
		return &Challenge{
			Name:        "test-challenge",
			DisplayName: "Test Challenge",
			Setup:       SetupSpec{Type: "scenario", Ref: "my-scenario"},
			Grading:     GradingSpec{UseDetectionCheck: true},
		}
	}

	tests := []struct {
		name    string
		mutate  func(*Challenge)
		wantErr string
	}{
		{
			name:    "missing name",
			mutate:  func(c *Challenge) { c.Name = "" },
			wantErr: "name is required",
		},
		{
			name:    "missing displayName",
			mutate:  func(c *Challenge) { c.DisplayName = "" },
			wantErr: "displayName is required",
		},
		{
			name:    "missing setup.type",
			mutate:  func(c *Challenge) { c.Setup.Type = "" },
			wantErr: "setup.type is required",
		},
		{
			name:    "missing setup.ref",
			mutate:  func(c *Challenge) { c.Setup.Ref = "" },
			wantErr: "setup.ref is required",
		},
		{
			name: "no grading mechanism",
			mutate: func(c *Challenge) {
				c.Grading = GradingSpec{}
			},
			wantErr: "grading:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validChallenge()
			tt.mutate(c)
			errs := c.Validate()
			if len(errs) == 0 {
				t.Fatalf("expected validation error containing %q, got none", tt.wantErr)
			}
			found := false
			for _, e := range errs {
				if strings.Contains(e, tt.wantErr) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected error containing %q, got: %v", tt.wantErr, errs)
			}
		})
	}
}
