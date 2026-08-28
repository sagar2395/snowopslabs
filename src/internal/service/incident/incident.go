// SPDX-License-Identifier: Apache-2.0

// Package incident moves the mutating half of the break-it/fix-it loop —
// injecting a fault and resolving it — onto the durable run engine (W4-T03).
// Injection and resolution are single scripts (incidents/<name>/{inject,resolve}.sh),
// so they map onto the engine exactly like lab and platform: a recorded,
// cancellable run under an exclusive lock.
//
// Only one incident is active at a time, so all injects and resolves share one
// global "incident" lock — the engine refuses a second inject while one is in
// flight, and refuses a resolve racing an inject. The "is an incident active?"
// question is answered from the store, the same way lab and platform answer
// their state.
//
// Detection (does the fault's check pass?) is not here: it is a read over the
// live cluster owned by the incident engine's checks runner, not a script the
// engine executes. This package is the durable script layer beneath that.
package incident

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"time"

	"github.com/sagar2395/snowopslabs/internal/run"
	"github.com/sagar2395/snowopslabs/internal/store"
)

// LockKey serialises every incident operation. One incident is active at a time,
// so inject and resolve share this single key — a second inject while one is in
// flight is refused with a *run.LockConflictError.
const LockKey = "incident"

// Run kinds, matching internal/run's DefaultTimeouts.
const (
	KindInject  = "incident.inject"
	KindResolve = "incident.resolve"
)

// validName guards the incident name that becomes a path segment.
var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// Target is the workload a fault breaks; exported to its scripts as
// TARGET_NAMESPACE / TARGET_WORKLOAD, matching the incident engine's contract.
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

// Service is the durable incident-lifecycle façade.
type Service struct {
	engine *run.Engine
	store  *store.Store
	env    map[string]string
}

// Option configures a Service.
type Option func(*Service)

// WithEnv layers base configuration onto the fault scripts' environment (e.g.
// DOMAIN_SUFFIX). The fault's own TARGET_* are added per operation.
func WithEnv(env map[string]string) Option {
	return func(s *Service) { s.env = env }
}

// New builds an incident Service over the given engine and store.
func New(engine *run.Engine, st *store.Store, opts ...Option) (*Service, error) {
	if engine == nil {
		return nil, fmt.Errorf("incident: a run engine is required")
	}
	if st == nil {
		return nil, fmt.Errorf("incident: a store is required")
	}
	s := &Service{engine: engine, store: st}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

// Inject submits a fault's inject.sh and returns the run ID. It does not wait; a
// second incident operation while one is in flight is refused.
func (s *Service) Inject(ctx context.Context, name string, target Target) (string, error) {
	return s.submit(ctx, KindInject, name, "inject.sh", target)
}

// Resolve submits a fault's resolve.sh — the escape hatch that always restores
// the lab.
func (s *Service) Resolve(ctx context.Context, name string, target Target) (string, error) {
	return s.submit(ctx, KindResolve, name, "resolve.sh", target)
}

func (s *Service) submit(ctx context.Context, kind, name, script string, target Target) (string, error) {
	if !validName.MatchString(name) {
		return "", fmt.Errorf("incident: invalid fault name %q", name)
	}
	// Base env plus the fault's target, without mutating the shared base map.
	env := make(map[string]string, len(s.env)+2)
	for k, v := range s.env {
		env[k] = v
	}
	for k, v := range target.env() {
		env[k] = v
	}
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

const (
	StateNone      State = "none"      // no incident operation recorded
	StateInjected  State = "injected"  // last completed op was a successful inject
	StateResolved  State = "resolved"  // last completed op was a successful resolve
	StateInjecting State = "injecting" // an inject is queued or running now
	StateResolving State = "resolving" // a resolve is queued or running now
	StateError     State = "error"     // the last completed op failed
)

// Status is a point-in-time answer about the active incident, from the store.
type Status struct {
	State State     `json:"state"`
	Fault string    `json:"fault,omitempty"` // the fault the deciding run acted on
	RunID string    `json:"runId,omitempty"`
	Since time.Time `json:"since,omitempty"`
}

// Status derives the incident state from the store — no cluster round-trip. An
// in-flight op wins; otherwise the most recent completed inject/resolve decides.
// A store-derived StateInjected is the durable answer to "is a fault active?".
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
