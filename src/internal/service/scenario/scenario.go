// SPDX-License-Identifier: Apache-2.0

// Package scenario activates and deactivates scenarios as runs on the run
// engine.
//
// Activating a scenario installs several components, so it is not a single
// script: the scenario engine does it in Go, and this service wraps that work
// in a run.Spec.Func. The whole activation is one recorded, cancellable run.
// Each installed component is written to the store's component inventory, so
// deactivation and teardown know what to remove.
package scenario

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/sagar2395/snowopslabs/internal/run"
	scn "github.com/sagar2395/snowopslabs/internal/scenario"
	"github.com/sagar2395/snowopslabs/internal/store"
	"github.com/sagar2395/snowopslabs/internal/toolchain"
	"github.com/sagar2395/snowopslabs/pkg/checks"
)

// Run kinds. Each has an entry in run.DefaultTimeouts.
const (
	KindActivate   = "scenario.activate"
	KindDeactivate = "scenario.deactivate"
)

var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// LockKey returns the lock key for one scenario. Different scenarios can
// change at once; one scenario cannot activate and deactivate at once.
func LockKey(name string) string { return "scenario:" + name }

// componentID returns a scenario component's inventory ID.
func componentID(scenario, component string) string {
	return "scenario:" + scenario + "/" + component
}

// Service submits scenario operations to the run engine and reports scenario
// state from the store.
type Service struct {
	engine *run.Engine
	store  *store.Store
	scenes *scn.Engine
	runner toolchain.Runner
	root   string
	env    map[string]string
}

// Option configures a Service.
type Option func(*Service)

// WithEnv sets the environment for component scripts.
func WithEnv(env map[string]string) Option { return func(s *Service) { s.env = env } }

// New builds a scenario Service. scenes does the installing; runner and
// projectRoot are used to run the helm, kubectl and script commands it issues.
func New(engine *run.Engine, st *store.Store, scenes *scn.Engine, runner toolchain.Runner, projectRoot string, opts ...Option) (*Service, error) {
	if engine == nil || st == nil || scenes == nil || runner == nil {
		return nil, errors.New("scenario: engine, store, scenario engine and runner are all required")
	}
	s := &Service{engine: engine, store: st, scenes: scenes, runner: runner, root: projectRoot}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

// Activate submits a scenario activation and returns the run ID. The run
// installs the scenario's components and records each one in the inventory.
// An operation already in flight on the same scenario causes a
// *run.LockConflictError.
func (s *Service) Activate(ctx context.Context, name string, force bool) (string, error) {
	return s.ActivateWithParams(ctx, name, force, nil)
}

// ActivateWithParams is Activate with parameter overrides that apply to this
// activation only. An empty map behaves like Activate.
func (s *Service) ActivateWithParams(ctx context.Context, name string, force bool, params map[string]string) (string, error) {
	sc, err := s.lookup(name)
	if err != nil {
		return "", err
	}
	return s.engine.Submit(ctx, run.Spec{
		Kind: KindActivate, Target: name, LockKey: LockKey(name),
		Func: func(fctx context.Context, out io.Writer) error {
			// Send the scenario engine's progress to the run transcript.
			s.scenes.SetOutput(out)
			defer s.scenes.SetOutput(nil)
			s.scenes.SetActivationParams(params)
			defer s.scenes.SetActivationParams(nil)
			exec := s.newExec(fctx, out)
			if err := s.scenes.Up(name, exec, force); err != nil {
				return err
			}
			s.recordComponents(fctx, sc)
			fmt.Fprintf(out, "\nrecorded %d component(s) in the inventory\n", len(sc.AllComponents()))
			return nil
		},
	})
}

// Deactivate submits a scenario teardown as one recorded run.
func (s *Service) Deactivate(ctx context.Context, name string) (string, error) {
	sc, err := s.lookup(name)
	if err != nil {
		return "", err
	}
	return s.engine.Submit(ctx, run.Spec{
		Kind: KindDeactivate, Target: name, LockKey: LockKey(name),
		Func: func(fctx context.Context, out io.Writer) error {
			s.scenes.SetOutput(out)
			defer s.scenes.SetOutput(nil)
			exec := s.newExec(fctx, out)
			if err := s.scenes.Down(name, exec); err != nil {
				return err
			}
			s.removeComponents(fctx, sc)
			return nil
		},
	})
}

// Verify runs the scenario's checks against the live cluster and returns one
// result per check. It is not a run and takes no lock.
func (s *Service) Verify(ctx context.Context, name string, runner *checks.Runner) ([]checks.Result, error) {
	if !validName.MatchString(name) {
		return nil, fmt.Errorf("scenario: invalid name %q", name)
	}
	return s.scenes.Verify(ctx, name, runner)
}

// Cancel stops an in-flight scenario run.
func (s *Service) Cancel(ctx context.Context, runID string) error {
	return s.engine.Cancel(ctx, runID)
}

func (s *Service) lookup(name string) (*scn.Scenario, error) {
	if !validName.MatchString(name) {
		return nil, fmt.Errorf("scenario: invalid name %q", name)
	}
	return s.scenes.Get(name)
}

func (s *Service) recordComponents(ctx context.Context, sc *scn.Scenario) {
	for _, comp := range sc.AllComponents() {
		_ = s.store.RecordComponentInstalled(ctx, store.Component{
			ID:        componentID(sc.Name, comp.Name),
			Kind:      "scenario",
			Owner:     sc.Name,
			Ref:       comp.Name,
			Namespace: comp.Namespace,
		})
	}
}

func (s *Service) removeComponents(ctx context.Context, sc *scn.Scenario) {
	now := time.Now()
	for _, comp := range sc.AllComponents() {
		_ = s.store.MarkComponentRemoved(ctx, componentID(sc.Name, comp.Name), "", now)
	}
}

// State is a scenario's lifecycle state as understood from the run history.
type State string

// Scenario states.
const (
	StateInactive     State = "inactive"     // never activated, or last completed op was deactivate
	StateActive       State = "active"       // last completed op was a successful activate
	StateActivating   State = "activating"   // an activate is queued or running now
	StateDeactivating State = "deactivating" // a deactivate is queued or running now
	StateError        State = "error"        // the last completed op failed
)

// Status describes one scenario at one moment, as read from the store.
type Status struct {
	Scenario string    `json:"scenario"`
	State    State     `json:"state"`
	RunID    string    `json:"runId,omitempty"`
	Since    time.Time `json:"since,omitempty"`
}

// Status reads a scenario's state from the store without contacting the
// cluster.
func (s *Service) Status(ctx context.Context, name string) (Status, error) {
	if !validName.MatchString(name) {
		return Status{}, fmt.Errorf("scenario: invalid name %q", name)
	}
	lock := LockKey(name)
	if active, held, err := s.store.ActiveRunForLock(ctx, lock); err != nil {
		return Status{}, err
	} else if held {
		state := StateActivating
		if active.Kind == KindDeactivate {
			state = StateDeactivating
		}
		return Status{Scenario: name, State: state, RunID: active.ID, Since: active.QueuedAt}, nil
	}
	last, ok, err := s.store.LastRunForLock(ctx, lock)
	if err != nil {
		return Status{}, err
	}
	if !ok {
		return Status{Scenario: name, State: StateInactive}, nil
	}
	st := Status{Scenario: name, RunID: last.ID, Since: last.EndedAt}
	switch {
	case last.Status != store.StatusSucceeded:
		st.State = StateError
	case last.Kind == KindDeactivate:
		st.State = StateInactive
	default: // KindActivate succeeded
		st.State = StateActive
	}
	return st, nil
}

// newExec returns a CommandExecutor that runs commands under the run's
// context and writes their output to the run transcript.
func (s *Service) newExec(ctx context.Context, out io.Writer) *streamExec {
	return &streamExec{ctx: ctx, out: out, runner: s.runner, env: s.env, root: s.root}
}

// streamExec implements the scenario engine's CommandExecutor on top of a
// toolchain.Runner. It holds the run's context, so cancelling the run stops the
// command in progress.
type streamExec struct {
	ctx    context.Context
	out    io.Writer
	runner toolchain.Runner
	env    map[string]string
	root   string
}

// RunCommandStreamed runs a binary on PATH (helm, kubectl) with argv-only args.
func (x *streamExec) RunCommandStreamed(_ /*label*/, name string, args ...string) (string, error) {
	path, err := x.runner.LookPath(name)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(x.out, "› %s %s\n", name, strings.Join(args, " "))
	return x.run(toolchain.Command{Path: path, Args: args})
}

// RunScriptStreamed runs a script, given relative to the project root, with
// bash.
func (x *streamExec) RunScriptStreamed(_ /*label*/, scriptPath string, args ...string) (string, error) {
	bash, err := x.runner.LookPath("bash")
	if err != nil {
		return "", err
	}
	full := scriptPath
	if !filepath.IsAbs(full) {
		full = filepath.Join(x.root, scriptPath)
	}
	fmt.Fprintf(x.out, "› %s %s\n", scriptPath, strings.Join(args, " "))
	return x.run(toolchain.Command{Path: bash, Args: append([]string{full}, args...)})
}

func (x *streamExec) run(cmd toolchain.Command) (string, error) {
	var buf bytes.Buffer
	w := io.MultiWriter(x.out, &buf)
	cmd.Dir = x.root
	cmd.Env = x.env
	cmd.Stdout = w
	cmd.Stderr = w
	_, err := x.runner.Run(x.ctx, cmd)
	return buf.String(), err
}
