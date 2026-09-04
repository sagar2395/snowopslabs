// SPDX-License-Identifier: Apache-2.0

// Package inventory keeps the store's installed-component inventory in step with
// what the run engine actually did. It is the bridge between the
// domain-agnostic engine and the component table: registered as a run.FinishHook,
// it watches runs finish and records the lasting effect — a succeeded
// platform.install adds the component, a succeeded platform.uninstall marks it
// removed — so teardown can later remove exactly what was installed and report
// anything it could not.
package inventory

import (
	"context"

	platsvc "github.com/sagar2395/snowopslabs/internal/service/platform"
	"github.com/sagar2395/snowopslabs/internal/store"
)

const kindPlatform = "platform"

// Recorder translates finished runs into component-inventory writes.
type Recorder struct {
	store *store.Store
}

// NewRecorder builds a Recorder over the given store.
func NewRecorder(st *store.Store) *Recorder { return &Recorder{store: st} }

// RunFinished implements run.FinishHook. It reacts only to successful component
// operations; a failed install records nothing (the component is not there), and
// a cancelled one likewise. Errors are swallowed: the inventory is a best-effort
// mirror, and a write failure must never wedge the engine's worker.
func (r *Recorder) RunFinished(ctx context.Context, run store.Run) {
	if run.Status != store.StatusSucceeded {
		return
	}
	switch run.Kind {
	case platsvc.KindInstall:
		// The run's lock key is the component's stable identity
		// ("platform:ingress/traefik"); its target is the "category/provider".
		_ = r.store.RecordComponentInstalled(ctx, store.Component{
			ID:          componentID(run),
			Kind:        kindPlatform,
			Ref:         run.Target,
			InstallRun:  run.ID,
			InstalledAt: run.EndedAt,
		})
	case platsvc.KindUninstall:
		// MarkComponentRemoved reports ErrComponentNotFound for something never
		// recorded (installed outside labctl); that is fine — it simply is not in
		// our inventory, and a teardown surfaces that separately.
		_ = r.store.MarkComponentRemoved(ctx, componentID(run), run.ID, run.EndedAt)
	}
}

// componentID is the component's stable inventory id. The engine sets a
// per-component lock key of exactly this form, so prefer it; fall back to
// deriving it from the target for a run that somehow carried no lock.
func componentID(run store.Run) string {
	if run.LockKey != "" {
		return run.LockKey
	}
	return "platform:" + run.Target
}

// InstalledPlatform returns the platform components currently recorded as
// installed, for teardown and status.
func (r *Recorder) InstalledPlatform(ctx context.Context) ([]store.Component, error) {
	return r.store.ListComponents(ctx, store.ComponentFilter{
		Status: store.ComponentInstalled,
		Kind:   kindPlatform,
	})
}
