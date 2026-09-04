// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/sagar2395/snowopslabs/internal/inventory"
	"github.com/sagar2395/snowopslabs/internal/run"
	"github.com/sagar2395/snowopslabs/internal/store"
	"github.com/sagar2395/snowopslabs/internal/toolchain"
)

// The durable-engine service commands (`labctl lab`, `labctl platform`) share
// this bootstrap: they each open the run store, build a run engine over the real
// toolchain, submit work through their service, and stream the recorded run to
// the terminal. Keeping the wiring here means lab and platform stay identical in
// how they start, stream, and clean up — only their domain (Kind, lock key,
// script) differs.

// newRunEngine opens the default run store and builds an UN-started run engine
// over the real toolchain, rooted at the project. The engine is returned
// un-started on purpose: a mutating command calls Start (which reconciles
// orphaned runs), while a read-only status command must not — starting an engine
// for a status read would cancel an operation running in another terminal.
// cleanup shuts the engine down (a no-op if it was never started) and closes the
// store; callers must always defer it.
func newRunEngine(ctx context.Context) (*run.Engine, *store.Store, func(), error) {
	dbPath, err := store.DefaultPath()
	if err != nil {
		return nil, nil, nil, err
	}
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		return nil, nil, nil, err
	}
	resolver, err := toolchain.NewResolver(cfg.ProjectRoot)
	if err != nil {
		_ = st.Close()
		return nil, nil, nil, err
	}
	// The inventory recorder keeps the store's component inventory in step with
	// what the engine installs/uninstalls, so teardown knows exactly
	// what to remove. It ignores run kinds it doesn't recognise, so it is safe to
	// attach for lab runs too.
	recorder := inventory.NewRecorder(st)
	eng, err := run.New(st, toolchain.NewExec(), resolver,
		run.WithWorkingDir(cfg.ProjectRoot),
		run.WithFinishHook(recorder.RunFinished))
	if err != nil {
		_ = st.Close()
		return nil, nil, nil, err
	}
	cleanup := func() {
		_ = shutdownEngine(eng)
		_ = st.Close()
	}
	return eng, st, cleanup, nil
}

func shutdownEngine(eng *run.Engine) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return eng.Shutdown(ctx)
}

// scriptEnv mirrors the config values the executor path propagates, so the
// runtime and platform scripts see the same environment whichever path drives
// them (golden rule 3: scripts read ${VAR:-default}, never source .env).
func scriptEnv() map[string]string {
	return map[string]string{
		"CLUSTER_NAME":         cfg.ClusterName,
		"DOMAIN_SUFFIX":        cfg.DomainSuffix,
		"HTTP_PORT":            cfg.HTTPPort,
		"HTTPS_PORT":           cfg.HTTPSPort,
		"INGRESS_CLASS":        cfg.IngressClass,
		"INGRESS_PROVIDER":     cfg.IngressProvider,
		"STORAGE_CLASS":        cfg.StorageClass,
		"PROFILE":              cfg.Profile,
		"MONITORING_NAMESPACE": cfg.MonitoringNamespace,
	}
}

// runEngineOperation starts the engine, then submits and streams one operation.
// verb names the operation in the start line and the failure message, e.g.
// "lab up" or "install ingress/traefik".
func runEngineOperation(ctx context.Context, out io.Writer, eng *run.Engine, st *store.Store, verb string, submit func() (string, error)) error {
	// Only a mutating op starts the engine (and thus orphan reconciliation).
	if err := eng.Start(ctx); err != nil {
		return err
	}
	return streamSubmittedRun(ctx, out, st, verb, submit)
}

// streamSubmittedRun submits one operation via submit, streams its transcript to
// out, and returns a non-nil error if the run did not succeed (so the command
// exits non-zero). It assumes the engine is already started — a batch (teardown)
// starts the engine once and streams many runs through this.
func streamSubmittedRun(ctx context.Context, out io.Writer, st *store.Store, verb string, submit func() (string, error)) error {
	id, err := submit()
	if err != nil {
		return err // includes the actionable *run.LockConflictError message
	}
	fmt.Fprintf(out, "%s started (run %s). Following output — Ctrl-C detaches; the run keeps going.\n\n", verb, id)

	if err := streamRunLogs(ctx, out, st, id, true); err != nil {
		return err
	}
	rec, err := st.GetRun(ctx, id)
	if err != nil {
		return err
	}
	if rec.Status != store.StatusSucceeded {
		return fmt.Errorf("%s %s (run %s)", verb, rec.Status, id)
	}
	return nil
}
