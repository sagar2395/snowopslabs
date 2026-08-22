// SPDX-License-Identifier: Apache-2.0
package incident

// Progressive hints (task 047): hints.md is plain markdown split on
// "## Hint N" headings, revealed one at a time. Reveals are recorded on the
// active incident's state so scoring can penalize them later.

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
		return nil, fmt.Errorf("hints.md has no '## Hint N' sections")
	}
	var hints []string
	for _, p := range parts[1:] { // parts[0] is the preamble before the first hint
		if text := strings.TrimSpace(p); text != "" {
			hints = append(hints, text)
		}
	}
	if len(hints) == 0 {
		return nil, fmt.Errorf("hints.md has no hint content")
	}
	return hints, nil
}

// Hint is one revealed hint.
type Hint struct {
	Index int    `json:"index"` // 1-based
	Total int    `json:"total"`
	Text  string `json:"text"`
}

// NextHint reveals the next unrevealed hint for the active incident and
// records the reveal (it will cost score in challenge mode).
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
	return &Hint{Index: active.HintsRevealed, Total: len(hints), Text: hints[active.HintsRevealed-1]}, nil
}

// Solution returns the full walkthrough for the active incident (or a named
// fault). It does not end the incident — reading the spoiler and applying
// the fix are still on the user.
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
	return string(data), nil
}
