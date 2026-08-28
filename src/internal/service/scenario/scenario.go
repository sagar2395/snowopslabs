// SPDX-License-Identifier: Apache-2.0

// Package scenario puts the scenario half of the break-it/fix-it loop on the
// durable run engine (W4-T01). Unlike lab/platform/incident — whose operations
// are single scripts — activating a scenario is multi-component orchestration
// the scenario engine performs in Go (helm/kubectl per component, per stage). So
// this service runs it as an in-process engine operation (run.Spec.Func): the
// whole activation is one recorded, cancellable run, its transcript streamed to
// the run log, and every component it installs is written to the store's
// component inventory (kind=scenario) so deactivation and teardown know exactly
// what to remove — the same inventory platform components use (W3-T04).
package scenario

import (
	"bytes"
	"context"
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

// Run kinds, matching internal/run's DefaultTimeouts.
const (
	KindActivate   = "scenario.activate"
	KindDeactivate = "scenario.deactivate"
)

var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// LockKey serialises a single scenario's activate/deactivate. Different
// scenarios may act concurrently; the same one may not activate and deactivate
// at once.
func LockKey(name string) string { return "scenario:" + name }

// componentID is a scenario component's stable inventory id.
func componentID(scenario, component string) string {
	return "scenario:" + scenario + "/" + component
}

// Service is the durable scenario-lifecycle façade.
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

// WithEnv layers configuration onto the component scripts' environment.
func WithEnv(env map[string]string) Option { return func(s *Service) { s.env = env } }

// New builds a scenario Service. scenes is the scenario engine that owns the
// declarative install logic; runner and projectRoot back the streaming executor
// the activation drives.
func New(engine *run.Engine, st *store.Store, scenes *scn.Engine, runner toolchain.Runner, projectRoot string, opts ...Option) (*Service, error) {
	if engine == nil || st == nil || scenes == nil || runner == nil {
		return nil, fmt.Errorf("scenario: engine, store, scenario engine and runner are all required")
	}
	s := &Service{engine: engine, store: st, scenes: scenes, runner: runner, root: projectRoot}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

// Activate submits a scenario activation as one recorded run and returns its ID.
// The run installs the scenario's components (via the scenario engine) and
// records each in the component inventory. A concurrent op on the same scenario
// is refused with *run.LockConflictError.
func (s *Service) Activate(ctx context.Context, name string, force bool) (string, error) {
	sc, err := s.lookup(name)
	if err != nil {
		return "", err
	}
	return s.engine.Submit(ctx, run.Spec{
		Kind: KindActivate, Target: name, LockKey: LockKey(name),
		Func: func(fctx context.Context, out io.Writer) error {
			// Route the scenario engine's progress into the run transcript, so
			// activation output is recorded and streamed through the engine rather
			// than racing on os.Stdout.
			s.scenes.SetOutput(out)
			defer s.scenes.SetOutput(nil)
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

// Verify runs the scenario's checks and returns one result per check. It is a
// live-cluster read (not an engine run): verifying an inactive scenario is how
// you prove it is down.
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

const (
	StateInactive     State = "inactive"     // never activated, or last completed op was deactivate
	StateActive       State = "active"       // last completed op was a successful activate
	StateActivating   State = "activating"   // an activate is queued or running now
	StateDeactivating State = "deactivating" // a deactivate is queued or running now
	StateError        State = "error"        // the last completed op failed
)

// Status is a point-in-time answer about one scenario, derived from the store.
type Status struct {
	Scenario string    `json:"scenario"`
	State    State     `json:"state"`
	RunID    string    `json:"runId,omitempty"`
	Since    time.Time `json:"since,omitempty"`
}

// Status derives a scenario's state from the store — no cluster round-trip.
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

// newExec builds a streaming, cancellable CommandExecutor bound to the run's
// context and transcript writer.
func (s *Service) newExec(ctx context.Context, out io.Writer) *streamExec {
	return &streamExec{ctx: ctx, out: out, runner: s.runner, env: s.env, root: s.root}
}

// streamExec adapts the toolchain runner to the scenario engine's CommandExecutor
// interface: it runs helm/kubectl and scenario scripts, streaming their output to
// the run transcript and honouring the run's context so a cancelled activation
// stops promptly.
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

// RunScriptStreamed runs a scenario script (path relative to the project root)
// through bash, so a scenario `type: script` component works the same as under
// the executor path.
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
