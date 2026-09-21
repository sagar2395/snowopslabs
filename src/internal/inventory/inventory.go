// SPDX-License-Identifier: Apache-2.0

// Package inventory keeps the store's installed-component inventory in step
// with the runs the engine finishes. Registered as a run.FinishHook, it records
// each successful platform.install as installed and each successful
// platform.uninstall as removed, so teardown knows exactly what to remove.
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

// RunFinished implements run.FinishHook. It records only successful component
// runs. Write errors are ignored: the inventory is best-effort, and the hook
// must not block the engine's worker.
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
		// ErrComponentNotFound here means it was installed outside labctl;
		// there is nothing to update.
		_ = r.store.MarkComponentRemoved(ctx, componentID(run), run.ID, run.EndedAt)
	}
}

// componentID returns the component's inventory ID. The run's lock key already
// has that form ("platform:ingress/traefik"); a run without one falls back to
// its target.
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
