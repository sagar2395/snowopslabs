// SPDX-License-Identifier: Apache-2.0

// Package lab brings a cluster up, tears it down and reports its status, with
// each operation running as a recorded, cancellable run on the run engine.
//
// The platform, scenario and incident services follow the same pattern:
//
//   - The service decides the run kind, the lock key and which script runs;
//     the run engine executes it.
//   - Operations return a run ID at once; callers follow progress through the
//     store instead of waiting.
//   - Status is read from the store, with an optional live check of the
//     cluster.
//
// Snapshots and reset live in internal/lab.
package lab

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"time"

	"github.com/sagar2395/snowopslabs/internal/run"
	"github.com/sagar2395/snowopslabs/internal/store"
)

// LockKey is the lock both up and down take, so one cannot start while the
// other is in flight; the engine refuses the second with a
// *run.LockConflictError.
const LockKey = "lab"

// Run kinds. Each has an entry in run.DefaultTimeouts.
const (
	KindUp   = "lab.up"
	KindDown = "lab.down"
)

// validRuntime restricts runtime names, which become a path segment
// (runtimes/<name>/). Checking here gives a clearer error than the resolver's
// "script not found".
var validRuntime = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}$`)

// Prober reports whether the cluster is reachable right now. Status calls it
// when live is true; a nil Prober disables the live check.
type Prober func(ctx context.Context) (Liveness, error)

// Liveness is the result of a live cluster probe.
type Liveness struct {
	Reachable bool   `json:"reachable"`
	Context   string `json:"context,omitempty"` // current kube-context, when known
	Detail    string `json:"detail,omitempty"`  // human-readable note (version, error)
}

// Service submits lab operations to the run engine and reports lab state from
// the store.
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

// WithEnv sets the environment for runtime scripts. They read settings such as
// HTTP_PORT and DOMAIN_SUFFIX as ${VAR:-default} and never source .env.
func WithEnv(env map[string]string) Option {
	return func(s *Service) { s.env = env }
}

// New builds a lab Service. clusterName is passed to the runtime scripts as
// their first argument.
func New(engine *run.Engine, st *store.Store, clusterName string, opts ...Option) (*Service, error) {
	if engine == nil {
		return nil, errors.New("lab: a run engine is required")
	}
	if st == nil {
		return nil, errors.New("lab: a store is required")
	}
	s := &Service{engine: engine, store: st, cluster: clusterName}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

// Up submits a cluster bring-up for the named runtime and returns the run ID
// without waiting for it to finish.
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
	// path.Join, not filepath.Join: the resolver expects slash-separated
	// relative paths.
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

// Cancel stops an in-flight lab run, including any child processes it started.
func (s *Service) Cancel(ctx context.Context, runID string) error {
	return s.engine.Cancel(ctx, runID)
}

// State is the lab's lifecycle state as understood from the run history.
type State string

// Lab states.
const (
	StateUnknown      State = "unknown"      // no lab operation has ever been recorded
	StateUp           State = "up"           // the last completed operation was a successful up
	StateDown         State = "down"         // the last completed operation was a successful down
	StateProvisioning State = "provisioning" // an up is queued or running now
	StateTearingDown  State = "tearing_down" // a down is queued or running now
	StateError        State = "error"        // the last completed operation failed
)

// Status describes the lab at one moment, as read from the store.
type Status struct {
	State   State     `json:"state"`
	Runtime string    `json:"runtime,omitempty"` // the runtime the deciding run acted on
	RunID   string    `json:"runId,omitempty"`   // the run the state was derived from
	Since   time.Time `json:"since,omitempty"`   // when that run was queued/ended
	// Live is set only when a live probe was requested and a prober is wired.
	Live *Liveness `json:"live,omitempty"`
}

// Status reads the lab state from the store, so it is fast and works when the
// cluster is unreachable. A queued or running operation takes precedence;
// otherwise the most recent finished up or down decides. When live is true and
// a Prober is set, the result also includes a live cluster check.
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

// lastCompleted returns the most recent finished up or down run, by fetching
// the newest run of each kind and taking the later one.
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
