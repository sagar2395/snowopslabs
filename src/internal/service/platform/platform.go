// SPDX-License-Identifier: Apache-2.0

// Package platform installs, uninstalls and probes platform components as runs
// on the run engine, following the same pattern as internal/service/lab.
//
// Unlike the lab, there are many components, so the lock and the state are per
// component ("platform:<category>/<provider>"). Two different components can
// install at once; one component cannot install and uninstall at once.
package platform

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/sagar2395/snowopslabs/internal/run"
	"github.com/sagar2395/snowopslabs/internal/store"
)

// Run kinds. Each has an entry in run.DefaultTimeouts.
const (
	KindInstall   = "platform.install"
	KindUninstall = "platform.uninstall"
	KindStatus    = "platform.status"
)

// segment restricts each category and provider segment, which become part of a
// script path (platform/<category>/<provider>/).
var segment = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}$`)

// Component returns a component's "category/provider" name, used as the run
// target and in the lock key.
func Component(category, provider string) string {
	if category == "" {
		return provider
	}
	return category + "/" + provider
}

// LockKey returns the lock key for one component. The "platform:" prefix keeps
// it distinct from other services' keys.
func LockKey(category, provider string) string {
	return "platform:" + Component(category, provider)
}

// Service submits component operations to the run engine and reports
// component state from the store.
type Service struct {
	engine *run.Engine
	store  *store.Store
	env    map[string]string
}

// Option configures a Service.
type Option func(*Service)

// WithEnv sets the environment for component scripts, such as DOMAIN_SUFFIX
// and MONITORING_NAMESPACE.
func WithEnv(env map[string]string) Option {
	return func(s *Service) { s.env = env }
}

// New builds a platform Service over the given engine and store.
func New(engine *run.Engine, st *store.Store, opts ...Option) (*Service, error) {
	if engine == nil {
		return nil, errors.New("platform: a run engine is required")
	}
	if st == nil {
		return nil, errors.New("platform: a store is required")
	}
	s := &Service{engine: engine, store: st}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

// Install submits a provider install and returns the run ID without waiting.
// An operation already in flight on the same component causes a
// *run.LockConflictError.
func (s *Service) Install(ctx context.Context, category, provider string) (string, error) {
	return s.submit(ctx, KindInstall, category, provider, "install.sh", LockKey(category, provider))
}

// Uninstall submits a provider uninstall and returns the run ID.
func (s *Service) Uninstall(ctx context.Context, category, provider string) (string, error) {
	return s.submit(ctx, KindUninstall, category, provider, "uninstall.sh", LockKey(category, provider))
}

// Probe submits the component's status.sh and returns the run ID. It is the
// live check behind --live. It takes no lock, so it never conflicts with other
// runs.
func (s *Service) Probe(ctx context.Context, category, provider string) (string, error) {
	return s.submit(ctx, KindStatus, category, provider, "status.sh", "")
}

func (s *Service) submit(ctx context.Context, kind, category, provider, script, lock string) (string, error) {
	if err := validate(category, provider); err != nil {
		return "", err
	}
	spec := run.Spec{
		Kind:    kind,
		Target:  Component(category, provider),
		LockKey: lock,
		Script:  path.Join("platform", category, provider, script),
		Env:     s.env,
	}
	return s.engine.Submit(ctx, spec)
}

// validate rejects a category or provider that is not a safe path. category
// may be nested (monitoring/metrics); each of its segments is checked.
func validate(category, provider string) error {
	if !segment.MatchString(provider) {
		return fmt.Errorf("platform: invalid provider %q", provider)
	}
	for seg := range strings.SplitSeq(category, "/") {
		if seg == "" || !segment.MatchString(seg) {
			return fmt.Errorf("platform: invalid category %q", category)
		}
	}
	return nil
}

// Cancel stops an in-flight component run (install/uninstall/probe).
func (s *Service) Cancel(ctx context.Context, runID string) error {
	return s.engine.Cancel(ctx, runID)
}

// State is a component's lifecycle state as understood from its run history.
type State string

// Component states.
const (
	StateUnknown    State = "unknown"    // no install/uninstall recorded for this component
	StateInstalled  State = "installed"  // last completed op was a successful install
	StateRemoved    State = "removed"    // last completed op was a successful uninstall
	StateInstalling State = "installing" // an install is queued or running now
	StateRemoving   State = "removing"   // an uninstall is queued or running now
	StateError      State = "error"      // the last completed op failed
)

// Status describes one component at one moment, as read from the store.
type Status struct {
	Component string    `json:"component"`
	State     State     `json:"state"`
	RunID     string    `json:"runId,omitempty"`
	Since     time.Time `json:"since,omitempty"`
}

// Status reads a component's state from the store, so it is fast and works
// when the cluster is unreachable. A queued or running install or uninstall
// takes precedence; otherwise the most recent finished one decides. Probe runs
// are ignored.
func (s *Service) Status(ctx context.Context, category, provider string) (Status, error) {
	if err := validate(category, provider); err != nil {
		return Status{}, err
	}
	comp := Component(category, provider)
	lock := LockKey(category, provider)

	if active, held, err := s.store.ActiveRunForLock(ctx, lock); err != nil {
		return Status{}, err
	} else if held {
		state := StateInstalling
		if active.Kind == KindUninstall {
			state = StateRemoving
		}
		return Status{Component: comp, State: state, RunID: active.ID, Since: active.QueuedAt}, nil
	}

	last, ok, err := s.store.LastRunForLock(ctx, lock)
	if err != nil {
		return Status{}, err
	}
	if !ok {
		return Status{Component: comp, State: StateUnknown}, nil
	}
	st := Status{Component: comp, RunID: last.ID, Since: last.EndedAt}
	switch {
	case last.Status != store.StatusSucceeded:
		st.State = StateError
	case last.Kind == KindUninstall:
		st.State = StateRemoved
	default: // KindInstall succeeded
		st.State = StateInstalled
	}
	return st, nil
}
