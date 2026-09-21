// SPDX-License-Identifier: Apache-2.0

package incident

// Hints: hints.md is markdown split on "## Hint N" headings and revealed one
// at a time. Each reveal is recorded on the active incident, so challenge
// scoring can charge for it.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ErrNoMoreHints is returned when every hint has been revealed.
var ErrNoMoreHints = errors.New("no more hints — you've seen them all")

var hintHeading = regexp.MustCompile(`(?m)^## Hint \d+\s*$`)

// ParseHints splits a hints.md into ordered hint texts.
func ParseHints(dir string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "hints.md"))
	if err != nil {
		return nil, err
	}
	parts := hintHeading.Split(string(data), -1)
	if len(parts) < 2 {
		return nil, errors.New("hints.md has no '## Hint N' sections")
	}
	var hints []string
	for _, p := range parts[1:] { // parts[0] is the preamble before the first hint
		if text := strings.TrimSpace(p); text != "" {
			hints = append(hints, text)
		}
	}
	if len(hints) == 0 {
		return nil, errors.New("hints.md has no hint content")
	}
	return hints, nil
}

// HintCount returns how many hints a fault has, or 0 for an unknown fault.
// Showing the full solution charges for all of them.
func (e *Engine) HintCount(name string) int {
	if name == "" {
		active, err := e.Active()
		if err != nil || active == nil {
			return 0
		}
		name = active.Fault
	}
	f, err := e.Get(name)
	if err != nil {
		return 0
	}
	hints, err := ParseHints(f.Dir)
	if err != nil {
		return 0
	}
	return len(hints)
}

// Hint is one revealed hint.
type Hint struct {
	Index int    `json:"index"` // 1-based
	Total int    `json:"total"`
	Text  string `json:"text"`
}

// NextHint returns the next unrevealed hint for the active incident and
// records the reveal, which costs score in a challenge.
func (e *Engine) NextHint() (*Hint, error) {
	active, err := e.Active()
	if err != nil {
		return nil, err
	}
	if active == nil {
		return nil, ErrNoActive
	}
	f, err := e.Get(active.Fault)
	if err != nil {
		return nil, err
	}
	hints, err := ParseHints(f.Dir)
	if err != nil {
		return nil, err
	}
	if active.HintsRevealed >= len(hints) {
		return nil, fmt.Errorf("%w (%d/%d revealed)", ErrNoMoreHints, active.HintsRevealed, len(hints))
	}

	active.HintsRevealed++
	if err := e.saveActive(active); err != nil {
		return nil, fmt.Errorf("recording hint reveal: %w", err)
	}
	// Expand templates so {{.WorkloadName}} names the app the learner is using.
	text := e.resolveTemplate(hints[active.HintsRevealed-1])
	return &Hint{Index: active.HintsRevealed, Total: len(hints), Text: text}, nil
}

// Solution returns the full walkthrough for the active incident, or for the
// named fault. It does not end the incident.
func (e *Engine) Solution(name string) (string, error) {
	if name == "" {
		active, err := e.Active()
		if err != nil {
			return "", err
		}
		if active == nil {
			return "", fmt.Errorf("%w (pass a fault name to read a specific solution)", ErrNoActive)
		}
		name = active.Fault
	}
	f, err := e.Get(name)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(f.Dir, "solution.md"))
	if err != nil {
		return "", err
	}
	return e.resolveTemplate(string(data)), nil
}
