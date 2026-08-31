// SPDX-License-Identifier: Apache-2.0
package learn

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

// Check mirrors the scenario checks schema — reused for module completion.
type Check struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"` // http | kubectl | promql | script
	URL  string `yaml:"url,omitempty"`
	// ExpectStatus is the canonical expected HTTP status. ExpectedStatus is a
	// deprecated alias kept so external learning-path content authored against
	// the old field keeps loading; new content should use expectStatus (see
	// sdk/schemas/check.schema.json). Prefer Status() over reading either field.
	ExpectStatus   int    `yaml:"expectStatus,omitempty"`
	ExpectedStatus int    `yaml:"expectedStatus,omitempty"`
	Command        string `yaml:"command,omitempty"`
	Query          string `yaml:"query,omitempty"`
	Script         string `yaml:"script,omitempty"`
	TimeoutSeconds int    `yaml:"timeoutSeconds,omitempty"`
}

// Status returns the expected HTTP status, preferring the canonical
// expectStatus and falling back to the deprecated expectedStatus alias.
func (c Check) Status() int {
	if c.ExpectStatus != 0 {
		return c.ExpectStatus
	}
	return c.ExpectedStatus
}

// Action declares how the learner should complete the module.
type Action struct {
	Type string `yaml:"type"` // command | scenario | incident
	Ref  string `yaml:"ref"`  // the command, scenario name, or fault name
}

// Module is one step in a learning path.
type Module struct {
	Name        string `yaml:"name"`
	DisplayName string `yaml:"displayName"`
	Intro       string `yaml:"intro"` // relative path to markdown file
	Action      Action `yaml:"action"`
	Check       Check  `yaml:"check"`
}

// Path is the top-level learning path schema.
type Path struct {
	Name             string   `yaml:"name"`
	DisplayName      string   `yaml:"displayName"`
	Description      string   `yaml:"description"`
	Tags             []string `yaml:"tags"`
	EstimatedMinutes int      `yaml:"estimatedMinutes"`
	Modules          []Module `yaml:"modules"`
	dir              string   // resolved directory on disk
}

// Dir returns the directory this path was loaded from.
func (p *Path) Dir() string { return p.dir }

// Validate checks required fields and returns any validation errors.
func (p *Path) Validate() []string {
	var errs []string
	if p.Name == "" {
		errs = append(errs, "name is required")
	}
	if p.DisplayName == "" {
		errs = append(errs, "displayName is required")
	}
	if len(p.Modules) == 0 {
		errs = append(errs, "at least one module is required")
	}
	for i, m := range p.Modules {
		prefix := fmt.Sprintf("modules[%d]", i)
		if m.Name == "" {
			errs = append(errs, prefix+": name is required")
		}
		if m.Action.Type == "" {
			errs = append(errs, prefix+": action.type is required")
		}
		if m.Action.Ref == "" {
			errs = append(errs, prefix+": action.ref is required")
		}
		if m.Check.Name == "" {
			errs = append(errs, prefix+": check.name is required")
		}
		if m.Check.Type == "" {
			errs = append(errs, prefix+": check.type is required")
		}
		if m.Intro != "" && unsafePath(m.Intro) {
			errs = append(errs, prefix+": intro path must not use .. or be absolute")
		}
	}
	return errs
}

func unsafePath(p string) bool {
	return filepath.IsAbs(p) || strings.Contains(p, "..")
}

// Progress tracks which modules in a path are complete.
type Progress struct {
	PathName      string `json:"path"`
	CompletedIdxs []int  `json:"completed"`
	StartedAt     string `json:"startedAt"`
	LastUpdatedAt string `json:"lastUpdatedAt"`
}

// Engine manages learning paths and per-path progress.
type Engine struct {
	learnDir   string
	stateDir   string
	resultsDir string // directory for the unified results store (empty = disabled)
	now        func() string
}

// New creates an Engine rooted at learnDir with progress persisted in stateDir.
// resultsDir is the directory for the unified results store (e.g. .labctl/history).
func New(learnDir, stateDir, resultsDir string) *Engine {
	return &Engine{
		learnDir:   learnDir,
		stateDir:   stateDir,
		resultsDir: resultsDir,
		now:        func() string { return time.Now().Format(time.RFC3339) },
	}
}

// Paths returns all valid learning paths discovered under learnDir.
func (e *Engine) Paths() ([]*Path, error) {
	entries, err := os.ReadDir(e.learnDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var paths []*Path
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		p, err := e.LoadPath(entry.Name())
		if err != nil {
			continue // skip malformed paths silently
		}
		paths = append(paths, p)
	}
	return paths, nil
}

// LoadPath loads and validates a single path by name.
func (e *Engine) LoadPath(name string) (*Path, error) {
	dir := filepath.Join(e.learnDir, name)
	data, err := os.ReadFile(filepath.Join(dir, "path.yaml"))
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", name, err)
	}
	var p Path
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse %s: %w", name, err)
	}
	p.dir = dir
	if errs := p.Validate(); len(errs) > 0 {
		return nil, fmt.Errorf("validate %s: %s", name, strings.Join(errs, "; "))
	}
	return &p, nil
}

// IntroText returns the intro markdown for a module, or empty string if none.
func (e *Engine) IntroText(p *Path, m Module) (string, error) {
	if m.Intro == "" {
		return "", nil
	}
	data, err := os.ReadFile(filepath.Join(p.dir, m.Intro))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// Progress loads the progress record for a path (nil if not started).
func (e *Engine) Progress(name string) (*Progress, error) {
	data, err := os.ReadFile(e.progressFile(name))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var prog Progress
	if err := json.Unmarshal(data, &prog); err != nil {
		return nil, err
	}
	return &prog, nil
}

// StartPath initialises (or resets) the progress record for a path.
func (e *Engine) StartPath(name string) (*Progress, error) {
	if _, err := e.LoadPath(name); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(e.stateDir, 0o755); err != nil {
		return nil, err
	}
	now := e.now()
	prog := &Progress{
		PathName:      name,
		CompletedIdxs: []int{},
		StartedAt:     now,
		LastUpdatedAt: now,
	}
	return prog, e.saveProgress(prog)
}

// ResetProgress discards a path's progress record so it reads as not-started
// and the learner can begin again from the first module. This is deliberately
// separate from lab reset: tearing down the cluster must not wipe what someone
// has learned, and clearing progress must not touch the cluster. A path that was
// never started is a no-op (no error).
func (e *Engine) ResetProgress(name string) error {
	if _, err := e.LoadPath(name); err != nil {
		return err
	}
	if err := os.Remove(e.progressFile(name)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// MarkComplete records module at idx as complete.
func (e *Engine) MarkComplete(prog *Progress, idx int) error {
	return e.markComplete(prog, idx, "", "")
}

// MarkCompleteModule records module at idx as complete and writes a unified
// result record naming the module as "<pathName>/<moduleName>". user attributes
// the record to the authenticated API user (task 062); pass "" from the CLI to
// fall back to the OS username.
func (e *Engine) MarkCompleteModule(p *Path, prog *Progress, idx int, user string) error {
	moduleName := ""
	if idx >= 0 && idx < len(p.Modules) {
		moduleName = p.Modules[idx].Name
	}
	return e.markComplete(prog, idx, p.Name+"/"+moduleName, user)
}

func (e *Engine) markComplete(prog *Progress, idx int, recordName, user string) error {
	for _, c := range prog.CompletedIdxs {
		if c == idx {
			return nil
		}
	}
	prog.CompletedIdxs = append(prog.CompletedIdxs, idx)
	prog.LastUpdatedAt = e.now()
	if err := e.saveProgress(prog); err != nil {
		return err
	}
	// Write to the unified results store (best effort).
	if e.resultsDir != "" && recordName != "" {
		now := time.Now()
		r := results.Record{
			Kind:      results.KindModule,
			Name:      recordName,
			User:      results.UserOr(user),
			StartedAt: now,
			EndedAt:   now,
			Score:     -1,
			Outcome:   "completed",
		}
		_ = results.NewStore(e.resultsDir).Append(r)
	}
	return nil
}

// NextModuleIdx returns the index of the next incomplete module, or -1 if done.
func NextModuleIdx(p *Path, prog *Progress) int {
	done := map[int]bool{}
	if prog != nil {
		for _, i := range prog.CompletedIdxs {
			done[i] = true
		}
	}
	for i := range p.Modules {
		if !done[i] {
			return i
		}
	}
	return -1
}

func (e *Engine) progressFile(name string) string {
	return filepath.Join(e.stateDir, name+".json")
}

func (e *Engine) saveProgress(prog *Progress) error {
	data, err := json.MarshalIndent(prog, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(e.progressFile(prog.PathName), append(data, '\n'), 0o644)
}
