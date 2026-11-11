// SPDX-License-Identifier: Apache-2.0

// Package challenge sets up, grades and cleans up a timed challenge. The CLI
// and the API both submit through Grader, so a recorded score always comes
// from the challenge's own checks run against the lab.
package challenge

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/snowops-oss/snowopslabs/internal/challenge"
	"github.com/snowops-oss/snowopslabs/internal/config"
	"github.com/snowops-oss/snowopslabs/internal/executor"
	"github.com/snowops-oss/snowopslabs/internal/incident"
	"github.com/snowops-oss/snowopslabs/internal/labcheck"
	"github.com/snowops-oss/snowopslabs/internal/scenario"
	"github.com/snowops-oss/snowopslabs/internal/workload"
	"github.com/snowops-oss/snowopslabs/pkg/checks"
)

// ErrNoChecks reports a challenge whose grading resolves to no checks. With
// nothing to pass it is never recorded as passed.
var ErrNoChecks = errors.New("the challenge has no grading checks: run 'labctl validate'")

// Grader runs a challenge's setup, grading and cleanup against the lab. Its
// engines must already be bound to the workload the challenge acts on.
type Grader struct {
	Engine    *challenge.Engine
	Scenarios *scenario.Engine
	Incidents *incident.Engine
	Config    *config.Config
	Exec      *executor.Executor
}

// Submission is one graded attempt at the active challenge.
type Submission struct {
	Challenge *challenge.Challenge
	Record    *challenge.RunRecord
	Checks    []checks.Result
	// Passed is true when every check passed and the run was recorded.
	Passed bool
	// CleanupErr is why the setup could not be undone after a pass. The score
	// is recorded either way.
	CleanupErr error
}

// Submit grades the active challenge. A pass records the score for user ("" is
// the OS user), ends the run and undoes the setup. A fail scores the attempt
// and leaves the run going, so the learner can fix it and submit again.
func (g *Grader) Submit(ctx context.Context, user string) (*Submission, error) {
	active, err := g.Engine.Active()
	if err != nil {
		return nil, err
	}
	if active == nil {
		return nil, challenge.ErrNoActive
	}
	c, err := g.Engine.Load(active.ChallengeName)
	if err != nil {
		return nil, err
	}
	gradingChecks, err := g.Checks(c)
	if err != nil {
		return nil, err
	}
	if len(gradingChecks) == 0 {
		return nil, fmt.Errorf("challenge %s: %w", c.Name, ErrNoChecks)
	}

	results := make([]checks.Result, 0, len(gradingChecks))
	for _, gc := range gradingChecks {
		runner := labcheck.NewRunner(g.Config, g.workload(c), gc.Dir)
		runner.Env = append(runner.Env, gc.Env...)
		results = append(results, runner.Run(ctx, gc.Check))
	}
	passed := 0
	for _, r := range results {
		if r.Pass {
			passed++
		}
	}
	sub := &Submission{Challenge: c, Checks: results}

	if passed < len(results) {
		if c.Silent {
			sub.Checks = withheld(results)
		}
		sub.Record, err = g.Engine.Attempt(passed, len(results))
		return sub, err
	}
	sub.Record, err = g.Engine.Complete(passed, len(results), "passed", user)
	if err != nil {
		return sub, err
	}
	sub.Passed = true
	// Undo the setup so no incident stays active to block the next challenge.
	sub.CleanupErr = g.Cleanup(c)
	return sub, nil
}

// GradeCheck is one check a challenge is graded with, and where it runs.
type GradeCheck struct {
	Check checks.Check
	// Dir is the directory a script check runs from: the fault's directory
	// for a detection check, otherwise the project root.
	Dir string
	// Env is what the check reads beyond the lab's own settings: a detection
	// check gets its fault's target, as `incident status` gives it.
	Env []string
}

// Checks returns the checks a challenge is graded with: every fault's
// detection check when it grades by those, otherwise its own checks.
func (g *Grader) Checks(c *challenge.Challenge) ([]GradeCheck, error) {
	if c.Grading.UseDetectionCheck && c.Setup.Type == "incident" {
		var out []GradeCheck
		for _, ref := range c.Setup.All() {
			k, dir, env, err := g.Incidents.Detection(ref)
			if err != nil {
				return nil, fmt.Errorf("loading detection check for %s: %w", ref, err)
			}
			out = append(out, GradeCheck{Check: k, Dir: dir, Env: env})
		}
		return out, nil
	}
	cs := make([]GradeCheck, 0, len(c.Grading.Checks))
	for _, gc := range c.Grading.Checks {
		k := g.resolveCheck(c, checks.Check{
			Name:           gc.Name,
			Type:           gc.Type,
			URL:            gc.URL,
			ExpectStatus:   gc.ExpectStatus,
			Script:         gc.Script,
			Query:          gc.Query,
			Operator:       gc.Operator,
			Value:          gc.Value,
			TimeoutSeconds: gc.TimeoutSeconds,
		})
		cs = append(cs, GradeCheck{Check: k, Dir: g.Config.ProjectRoot})
	}
	return cs, nil
}

// resolveCheck expands template variables with the engine that runs the
// challenge's setup.
func (g *Grader) resolveCheck(c *challenge.Challenge, k checks.Check) checks.Check {
	if c.Setup.Type == "scenario" {
		return g.Scenarios.ResolveCheck(k)
	}
	return g.Incidents.ResolveCheck(k)
}

// workload is the app the challenge's setup acts on.
func (g *Grader) workload(c *challenge.Challenge) workload.Workload {
	if c.Setup.Type == "scenario" || g.Incidents == nil {
		return g.Scenarios.Workload
	}
	return g.Incidents.Workload
}

// Setup prepares the lab for a challenge: it injects the faults, in order, or
// activates the scenario the challenge names. The incident engine records one
// fault as active, so each fault after the first is injected over it.
func (g *Grader) Setup(c *challenge.Challenge) error {
	switch c.Setup.Type {
	case "incident":
		for i, ref := range c.Setup.All() {
			if _, err := g.Incidents.Inject(ref, g.Exec, i > 0, c.Silent); err != nil {
				return err
			}
		}
		return nil
	case "scenario":
		return g.Scenarios.Up(c.Setup.Ref, g.Exec, false)
	default:
		return fmt.Errorf("unknown setup type %q", c.Setup.Type)
	}
}

// Cleanup undoes Setup, resolving every fault by name, last injected first.
// A fault that never got injected resolves as a no-op.
func (g *Grader) Cleanup(c *challenge.Challenge) error {
	switch c.Setup.Type {
	case "incident":
		var errs []error
		for _, ref := range slices.Backward(c.Setup.All()) {
			if _, err := g.Incidents.Resolve(ref, g.Exec, ""); err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	case "scenario":
		return g.Scenarios.Down(c.Setup.Ref, g.Exec)
	default:
		return fmt.Errorf("unknown setup type %q", c.Setup.Type)
	}
}

// NextHint reveals the next hint of the active incident challenge and charges
// it to the run. One fault uses its own hint ladder; several take turns, a
// rung of each in setup order, so no fault is spoiled before the others are
// started on.
func (g *Grader) NextHint() (*incident.Hint, error) {
	active, err := g.Engine.Active()
	if err != nil {
		return nil, err
	}
	if active == nil {
		return nil, challenge.ErrNoActive
	}
	c, err := g.Engine.Load(active.ChallengeName)
	if err != nil {
		return nil, err
	}
	if c.Setup.Type != "incident" {
		return nil, fmt.Errorf("hints not available for %s-type challenges", c.Setup.Type)
	}
	refs := c.Setup.All()
	var hint *incident.Hint
	if len(refs) == 1 {
		hint, err = g.Incidents.NextHint()
	} else {
		hint, err = g.turnHint(refs, active.HintsUsed)
	}
	if err != nil {
		return nil, err
	}
	return hint, g.Engine.RecordHint()
}

// turnHint returns hint number used+1 of the faults' ladders taken in turn,
// skipping a fault whose ladder has run out.
func (g *Grader) turnHint(refs []string, used int) (*incident.Hint, error) {
	ladders := make([][]string, len(refs))
	total := 0
	for i, ref := range refs {
		f, err := g.Incidents.Get(ref)
		if err != nil {
			return nil, err
		}
		if ladders[i], err = incident.ParseHints(f.Dir); err != nil {
			return nil, fmt.Errorf("hints for %s: %w", ref, err)
		}
		total += len(ladders[i])
	}
	if used >= total {
		return nil, fmt.Errorf("%w (%d/%d revealed)", incident.ErrNoMoreHints, used, total)
	}
	n := 0
	for rung := 0; ; rung++ {
		for _, ladder := range ladders {
			if rung >= len(ladder) {
				continue
			}
			if n == used {
				return &incident.Hint{Index: used + 1, Total: total, Text: g.Incidents.ResolveTemplate(ladder[rung])}, nil
			}
			n++
		}
	}
}

// withheld is a silent challenge's results with what names its faults taken
// out: each check is "fault N of M", passing or not.
func withheld(results []checks.Result) []checks.Result {
	out := make([]checks.Result, len(results))
	for i, r := range results {
		out[i] = checks.Result{Name: fmt.Sprintf("fault %d of %d", i+1, len(results)), Type: r.Type, Pass: r.Pass}
		if !r.Pass {
			out[i].Error = "not fixed yet"
		}
	}
	return out
}
