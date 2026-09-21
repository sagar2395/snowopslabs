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

// The commands that run through the run engine (lab, platform, scenario,
// incident) share this setup: open the run store, build a run engine, submit
// work through a service, and stream the run to the terminal.

// newRunEngine opens the default run store and builds a run engine that is not
// yet started. Commands that change state call Start; read-only commands must
// not, because Start cancels runs it thinks were interrupted, including one
// running in another terminal. Always defer cleanup, which shuts the engine
// down and closes the store.
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
	// Keep the component inventory up to date with installs and uninstalls.
	// The recorder ignores other run kinds.
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

// scriptEnv returns the environment scripts run with through the run engine:
// the same values scriptExec passes, since scripts read their settings from
// the environment and never source .env.
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

		// The bound workload (ADR-0014). Incident scripts also get TARGET_*
		// from the fault's target.
		"WORKLOAD_NAME":      scenes.Workload.Name,
		"WORKLOAD_NAMESPACE": scenes.Workload.Namespace,
		"WORKLOAD_PORT":      scenes.Workload.Port,
		"WORKLOAD_METRIC":    scenes.Workload.Metric,
	}
}

// runEngineOperation starts the engine, then submits and streams one operation.
// verb names the operation in the start line and the failure message, e.g.
// "lab up" or "install ingress/traefik".
func runEngineOperation(ctx context.Context, out io.Writer, eng *run.Engine, st *store.Store, verb string, submit func() (string, error)) error {
	if err := eng.Start(ctx); err != nil {
		return err
	}
	return streamSubmittedRun(ctx, out, st, verb, submit)
}

// streamSubmittedRun submits one operation, streams its transcript to out, and
// returns an error if the run did not succeed. The engine must already be
// started.
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
