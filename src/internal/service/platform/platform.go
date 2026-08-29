// SPDX-License-Identifier: Apache-2.0

// Package platform moves platform-component lifecycle — install a provider,
// uninstall it, probe its status — onto the durable run engine (internal/run),
// the parallel sibling of internal/service/lab (W3-T03). It follows the same
// shape: a thin, domain-specific façade over the engine that owns the run Kind,
// the exclusive LockKey, and how a (category, provider) pair maps to a script;
// it never executes anything itself.
//
// The difference from lab is granularity. There is one lab, but many platform
// components, so the exclusive lock — and the store-derived state — is per
// component ("platform:<category>/<provider>"). Two different components install
// concurrently; the same component cannot install and uninstall at once.
package platform

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/sagar2395/snowopslabs/internal/run"
	"github.com/sagar2395/snowopslabs/internal/store"
)

// Run kinds. They match internal/run's DefaultTimeouts, so install gets the long
// budget and status a short one.
const (
	KindInstall   = "platform.install"
	KindUninstall = "platform.uninstall"
	KindStatus    = "platform.status"
)

// segment guards each path segment that becomes part of a script location
// (platform/<category>/<provider>/…). The resolver would reject an escaping path
// anyway; validating here turns a typo into a clear "invalid component".
var segment = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}$`)

// Component is the canonical "category/provider" identity of one component. It
// is the run Target and the basis of the lock key.
func Component(category, provider string) string {
	if category == "" {
		return provider
	}
	return category + "/" + provider
}

// LockKey serialises the install/uninstall of one component. Namespaced so it
// can never collide with lab's "lab" key or another service's.
func LockKey(category, provider string) string {
	return "platform:" + Component(category, provider)
}

// Service is the durable platform-lifecycle façade.
type Service struct {
	engine *run.Engine
	store  *store.Store
	env    map[string]string
}

// Option configures a Service.
type Option func(*Service)

// WithEnv layers configuration onto the component scripts' environment (the same
// values the executor path sets: DOMAIN_SUFFIX, MONITORING_NAMESPACE, …).
func WithEnv(env map[string]string) Option {
	return func(s *Service) { s.env = env }
}

// New builds a platform Service over the given engine and store.
func New(engine *run.Engine, st *store.Store, opts ...Option) (*Service, error) {
	if engine == nil {
		return nil, fmt.Errorf("platform: a run engine is required")
	}
	if st == nil {
		return nil, fmt.Errorf("platform: a store is required")
	}
	s := &Service{engine: engine, store: st}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

// Install submits a provider install and returns the run ID. It does not wait.
// A concurrent op on the same component is refused with *run.LockConflictError.
func (s *Service) Install(ctx context.Context, category, provider string) (string, error) {
	return s.submit(ctx, KindInstall, category, provider, "install.sh", LockKey(category, provider))
}

// Uninstall submits a provider uninstall and returns the run ID.
func (s *Service) Uninstall(ctx context.Context, category, provider string) (string, error) {
	return s.submit(ctx, KindUninstall, category, provider, "uninstall.sh", LockKey(category, provider))
}

// Probe submits a status.sh run for a component and returns the run ID — the
// --live check. It takes no lock, so a probe never conflicts with anything and
// two probes may overlap harmlessly. The caller streams it like any run.
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

// validate rejects a category/provider whose segments could not name a real
// on-disk component. category may be nested (monitoring/metrics); every segment
// and the provider must be a safe path token.
func validate(category, provider string) error {
	if !segment.MatchString(provider) {
		return fmt.Errorf("platform: invalid provider %q", provider)
	}
	for _, seg := range strings.Split(category, "/") {
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

const (
	StateUnknown    State = "unknown"    // no install/uninstall recorded for this component
	StateInstalled  State = "installed"  // last completed op was a successful install
	StateRemoved    State = "removed"    // last completed op was a successful uninstall
	StateInstalling State = "installing" // an install is queued or running now
	StateRemoving   State = "removing"   // an uninstall is queued or running now
	StateError      State = "error"      // the last completed op failed
)

// Status is a point-in-time answer about one component, derived from the store.
type Status struct {
	Component string    `json:"component"`
	State     State     `json:"state"`
	RunID     string    `json:"runId,omitempty"`
	Since     time.Time `json:"since,omitempty"`
}

// Status derives a component's state from the store — no cluster round-trip, so
// it is fast and works when the cluster is unreachable. An in-flight op wins
// (mid-transition); otherwise the most recent completed install/uninstall
// decides. Status (probe) runs take no lock, so they never confuse this.
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
