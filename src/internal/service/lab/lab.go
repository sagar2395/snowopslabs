// SPDX-License-Identifier: Apache-2.0

// Package lab moves cluster lifecycle — bring a lab up, tear it down, ask its
// status — onto the durable run engine (internal/run), so every operation is
// cancellable, time-bounded, and recorded in the store rather than shelled out
// and forgotten (ADR-0003/0004/0006).
//
// It sets the shape the platform, scenario and incident services follow:
//
//   - A service is a thin façade over the engine. It owns the run Kind, the
//     exclusive LockKey and the name-to-script mapping; it executes nothing.
//   - Mutations return a run ID immediately — callers stream progress from the
//     store rather than blocking here.
//   - Status comes from the store, with an opt-in live probe for callers that
//     need ground truth.
//
// Snapshot and reset stay on their existing path (internal/lab).
package lab

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"time"

	"github.com/sagar2395/snowopslabs/internal/run"
	"github.com/sagar2395/snowopslabs/internal/store"
)

// LockKey serialises every cluster-lifecycle operation. Bringing a lab up while
// a teardown is in flight (or vice versa) is never valid, so up and down share
// one key: the engine refuses the second with a *run.LockConflictError (409).
const LockKey = "lab"

// Run kinds. They match internal/run's DefaultTimeouts so up gets the long
// cluster-build budget and down the shorter teardown one.
const (
	KindUp   = "lab.up"
	KindDown = "lab.down"
)

// validRuntime guards the name that becomes a path segment (runtimes/<name>/…).
// The resolver would reject an escaping path anyway, but validating here turns a
// typo into a clear "unknown runtime" instead of a "script not found".
var validRuntime = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}$`)

// Prober reports whether the cluster is reachable right now. It is the seam for
// Status(..., live=true): production wires a kubectl/API probe, tests inject a
// deterministic one. A nil Prober means live probing is unavailable.
type Prober func(ctx context.Context) (Liveness, error)

// Liveness is the result of a live cluster probe.
type Liveness struct {
	Reachable bool   `json:"reachable"`
	Context   string `json:"context,omitempty"` // current kube-context, when known
	Detail    string `json:"detail,omitempty"`  // human-readable note (version, error)
}

// Service is the durable lab-lifecycle façade.
type Service struct {
	engine  *run.Engine
	store   *store.Store
	cluster string
	env     map[string]string
	prober  Prober
}

// Option configures a Service.
type Option func(*Service)

// WithProber attaches a live cluster prober used by Status when live=true.
func WithProber(p Prober) Option { return func(s *Service) { s.prober = p } }

// WithEnv layers configuration onto the runtime scripts' environment. The
// scripts read values like HTTP_PORT and DOMAIN_SUFFIX as ${VAR:-default}
// (golden rule 3), so the caller passes the resolved config here rather than
// letting a script source .env itself.
func WithEnv(env map[string]string) Option {
	return func(s *Service) { s.env = env }
}

// New builds a lab Service over the given engine and store. clusterName is the
// argument passed to the runtime scripts (they read it as argv[1]).
func New(engine *run.Engine, st *store.Store, clusterName string, opts ...Option) (*Service, error) {
	if engine == nil {
		return nil, fmt.Errorf("lab: a run engine is required")
	}
	if st == nil {
		return nil, fmt.Errorf("lab: a store is required")
	}
	s := &Service{engine: engine, store: st, cluster: clusterName}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

// Up submits a cluster bring-up for the named runtime and returns the run ID.
// It does not wait: the caller streams the run's progress. A concurrent lab
// operation is refused with *run.LockConflictError.
func (s *Service) Up(ctx context.Context, runtime string) (string, error) {
	return s.submit(ctx, KindUp, runtime, "up.sh")
}

// Down submits a teardown for the named runtime and returns the run ID.
func (s *Service) Down(ctx context.Context, runtime string) (string, error) {
	return s.submit(ctx, KindDown, runtime, "down.sh")
}

func (s *Service) submit(ctx context.Context, kind, runtime, script string) (string, error) {
	if !validRuntime.MatchString(runtime) {
		return "", fmt.Errorf("lab: invalid runtime %q (expected a name like k3d, kind, incluster)", runtime)
	}
	// path.Join (not filepath) keeps the script relative and slash-separated,
	// which is what the engine's content-root resolver expects.
	spec := run.Spec{
		Kind:    kind,
		Target:  runtime,
		LockKey: LockKey,
		Script:  path.Join("runtimes", runtime, script),
		Env:     s.env,
	}
	if s.cluster != "" {
		spec.Args = []string{s.cluster}
	}
	return s.engine.Submit(ctx, spec)
}

// Cancel stops an in-flight lab run. Cancellation reaches the whole process
// group via the engine, so a cancelled `lab up` leaves no orphaned k3d/kubectl
// children behind (ADR-0003).
func (s *Service) Cancel(ctx context.Context, runID string) error {
	return s.engine.Cancel(ctx, runID)
}

// State is the lab's lifecycle state as understood from the run history.
type State string

const (
	StateUnknown      State = "unknown"      // no lab operation has ever been recorded
	StateUp           State = "up"           // the last completed operation was a successful up
	StateDown         State = "down"         // the last completed operation was a successful down
	StateProvisioning State = "provisioning" // an up is queued or running now
	StateTearingDown  State = "tearing_down" // a down is queued or running now
	StateError        State = "error"        // the last completed operation failed
)

// Status is a point-in-time answer about the lab, derived from the store.
type Status struct {
	State   State     `json:"state"`
	Runtime string    `json:"runtime,omitempty"` // the runtime the deciding run acted on
	RunID   string    `json:"runId,omitempty"`   // the run the state was derived from
	Since   time.Time `json:"since,omitempty"`   // when that run was queued/ended
	// Live is set only when a live probe was requested and a prober is wired.
	Live *Liveness `json:"live,omitempty"`
}

// Status answers from the store — no cluster round-trip — so it stays fast and
// works even when the cluster is unreachable. When live is true and a Prober is
// configured, it additionally attaches a fresh cluster probe.
//
// The store read is the deciding one: an in-flight operation wins (the lab is
// mid-transition), otherwise the most recent completed up/down decides.
func (s *Service) Status(ctx context.Context, live bool) (Status, error) {
	st, err := s.deriveState(ctx)
	if err != nil {
		return Status{}, err
	}
	if live && s.prober != nil {
		l, perr := s.prober(ctx)
		if perr != nil {
			l = Liveness{Reachable: false, Detail: perr.Error()}
		}
		st.Live = &l
	}
	return st, nil
}

func (s *Service) deriveState(ctx context.Context) (Status, error) {
	// An in-flight lab run means the lab is mid-transition; report that first.
	if active, held, err := s.store.ActiveRunForLock(ctx, LockKey); err != nil {
		return Status{}, err
	} else if held {
		state := StateProvisioning
		if active.Kind == KindDown {
			state = StateTearingDown
		}
		return Status{State: state, Runtime: active.Target, RunID: active.ID, Since: active.QueuedAt}, nil
	}

	last, ok, err := s.lastCompleted(ctx)
	if err != nil {
		return Status{}, err
	}
	if !ok {
		return Status{State: StateUnknown}, nil
	}

	st := Status{Runtime: last.Target, RunID: last.ID, Since: last.EndedAt}
	switch {
	case last.Status != store.StatusSucceeded:
		st.State = StateError
	case last.Kind == KindUp:
		st.State = StateUp
	default: // KindDown succeeded
		st.State = StateDown
	}
	return st, nil
}

// lastCompleted returns the most recent terminal up-or-down run. It queries each
// kind's newest row (both indexed, LIMIT 1) and takes the later of the two,
// rather than scanning an unbounded window of unrelated runs.
func (s *Service) lastCompleted(ctx context.Context) (store.Run, bool, error) {
	var best store.Run
	found := false
	for _, kind := range []string{KindUp, KindDown} {
		runs, err := s.store.ListRuns(ctx, store.RunFilter{Kind: kind, Limit: 1})
		if err != nil {
			return store.Run{}, false, err
		}
		if len(runs) == 0 {
			continue
		}
		r := runs[0]
		if !found || r.QueuedAt.After(best.QueuedAt) {
			best, found = r, true
		}
	}
	return best, found, nil
}
