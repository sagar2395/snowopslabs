// SPDX-License-Identifier: Apache-2.0

// Package incident injects and resolves faults as runs on the run engine.
// Each operation runs one script, incidents/<name>/inject.sh or resolve.sh, as
// a recorded, cancellable run.
//
// Only one incident can be active, so every inject and resolve takes the same
// "incident" lock. Status is read from the store. Checking whether a fault has
// been fixed is done by internal/incident, not here.
package incident

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path"
	"regexp"
	"time"

	"github.com/sagar2395/snowopslabs/internal/run"
	"github.com/sagar2395/snowopslabs/internal/store"
)

// LockKey is the lock every inject and resolve takes. A second operation while
// one is in flight is refused with a *run.LockConflictError.
const LockKey = "incident"

// Run kinds, matching internal/run's DefaultTimeouts.
const (
	KindInject  = "incident.inject"
	KindResolve = "incident.resolve"
)

// validName restricts incident names, which become path segments.
var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// Target is the workload a fault breaks. Its scripts receive it as
// TARGET_NAMESPACE and TARGET_WORKLOAD.
type Target struct {
	Namespace string
	Workload  string
}

func (t Target) env() map[string]string {
	m := map[string]string{}
	if t.Namespace != "" {
		m["TARGET_NAMESPACE"] = t.Namespace
	}
	if t.Workload != "" {
		m["TARGET_WORKLOAD"] = t.Workload
	}
	return m
}

// Service submits incident operations to the run engine and reports their
// state from the store.
type Service struct {
	engine *run.Engine
	store  *store.Store
	env    map[string]string
}

// Option configures a Service.
type Option func(*Service)

// WithEnv sets the base environment for fault scripts, such as DOMAIN_SUFFIX.
// Each operation adds its own TARGET_* values on top.
func WithEnv(env map[string]string) Option {
	return func(s *Service) { s.env = env }
}

// New builds an incident Service over the given engine and store.
func New(engine *run.Engine, st *store.Store, opts ...Option) (*Service, error) {
	if engine == nil {
		return nil, errors.New("incident: a run engine is required")
	}
	if st == nil {
		return nil, errors.New("incident: a store is required")
	}
	s := &Service{engine: engine, store: st}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

// Inject submits a fault's inject.sh and returns the run ID without waiting
// for it to finish.
func (s *Service) Inject(ctx context.Context, name string, target Target) (string, error) {
	return s.submit(ctx, KindInject, name, "inject.sh", target)
}

// Resolve submits a fault's resolve.sh, which restores the lab, and returns
// the run ID.
func (s *Service) Resolve(ctx context.Context, name string, target Target) (string, error) {
	return s.submit(ctx, KindResolve, name, "resolve.sh", target)
}

func (s *Service) submit(ctx context.Context, kind, name, script string, target Target) (string, error) {
	if !validName.MatchString(name) {
		return "", fmt.Errorf("incident: invalid fault name %q", name)
	}
	// Copy, so the shared base map is not modified.
	env := make(map[string]string, len(s.env)+2)
	maps.Copy(env, s.env)
	maps.Copy(env, target.env())
	spec := run.Spec{
		Kind:    kind,
		Target:  name,
		LockKey: LockKey,
		Script:  path.Join("incidents", name, script),
		Env:     env,
	}
	return s.engine.Submit(ctx, spec)
}

// Cancel stops an in-flight incident run.
func (s *Service) Cancel(ctx context.Context, runID string) error {
	return s.engine.Cancel(ctx, runID)
}

// State is the incident lifecycle state as understood from the run history.
type State string

// Incident states.
const (
	StateNone      State = "none"      // no incident operation recorded
	StateInjected  State = "injected"  // last completed op was a successful inject
	StateResolved  State = "resolved"  // last completed op was a successful resolve
	StateInjecting State = "injecting" // an inject is queued or running now
	StateResolving State = "resolving" // a resolve is queued or running now
	StateError     State = "error"     // the last completed op failed
)

// Status describes the incident state at one moment, as read from the store.
type Status struct {
	State State     `json:"state"`
	Fault string    `json:"fault,omitempty"` // the fault the deciding run acted on
	RunID string    `json:"runId,omitempty"`
	Since time.Time `json:"since,omitempty"`
}

// Status reads the incident state from the store without contacting the
// cluster. A queued or running operation takes precedence; otherwise the most
// recent finished inject or resolve decides.
func (s *Service) Status(ctx context.Context) (Status, error) {
	if active, held, err := s.store.ActiveRunForLock(ctx, LockKey); err != nil {
		return Status{}, err
	} else if held {
		state := StateInjecting
		if active.Kind == KindResolve {
			state = StateResolving
		}
		return Status{State: state, Fault: active.Target, RunID: active.ID, Since: active.QueuedAt}, nil
	}

	last, ok, err := s.store.LastRunForLock(ctx, LockKey)
	if err != nil {
		return Status{}, err
	}
	if !ok {
		return Status{State: StateNone}, nil
	}
	st := Status{Fault: last.Target, RunID: last.ID, Since: last.EndedAt}
	switch {
	case last.Status != store.StatusSucceeded:
		st.State = StateError
	case last.Kind == KindResolve:
		st.State = StateResolved
	default: // KindInject succeeded
		st.State = StateInjected
	}
	return st, nil
}
