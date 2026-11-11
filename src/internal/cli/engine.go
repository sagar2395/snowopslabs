// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/snowops-oss/snowopslabs/internal/capacity"
	"github.com/snowops-oss/snowopslabs/internal/inventory"
	"github.com/snowops-oss/snowopslabs/internal/run"
	"github.com/snowops-oss/snowopslabs/internal/store"
	"github.com/snowops-oss/snowopslabs/internal/toolchain"
)

// The commands that run through the run engine (lab, platform, scenario,
// incident, traffic) share this setup: open the run store, build a run engine, submit
// work through a service, and stream the run to the terminal.

// newRunEngine opens the default store and builds an unstarted engine.
// Call Start for recorded operations; it preserves live owners.
// Always defer cleanup to shut down the engine and close the store.
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
	env := map[string]string{
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
	if verbose {
		env["LABCTL_VERBOSE"] = "1"
	}
	return env
}

// runEngineOperation starts the engine, then submits and streams one operation.
// verb names the operation in the start line and the failure message, e.g.
// "lab up" or "install ingress/traefik".
func runEngineOperation(ctx context.Context, out io.Writer, eng *run.Engine, st *store.Store, verb string, submit func() (string, error)) error {
	if err := startEngine(ctx, out, eng); err != nil {
		return err
	}
	return streamSubmittedRun(ctx, out, st, verb, submit)
}

// startEngine puts a k3d lab's inotify limit back, then starts the engine.
// Every command that runs scripts against the lab starts its engine here.
func startEngine(ctx context.Context, out io.Writer, eng *run.Engine) error {
	restoreInotifyLimit(ctx, out, toolchain.NewExec())
	return eng.Start(ctx)
}

// restoreInotifyLimit raises fs.inotify.max_user_instances on a k3d lab's
// nodes when it reads below what the lab needs. runtimes/k3d/up.sh sets it when
// it creates the cluster, but a Docker VM restart puts the VM's default back,
// and k3s on a node then dies with "too many open files" while its container
// stays up (R04). A lab that is down or cannot be read is left alone.
func restoreInotifyLimit(ctx context.Context, out io.Writer, runner toolchain.Runner) {
	if cfg == nil || cfg.Profile != "k3d" || cfg.ClusterName == "" {
		return
	}
	label, _ := capacity.NodeLabel(cfg.Profile, cfg.ClusterName)
	limit, err := capacity.RaiseInotifyLimit(ctx, runner, label)
	if err != nil || limit == 0 || limit >= capacity.InotifyInstances {
		return
	}
	fmt.Fprintf(out, "Raised fs.inotify.max_user_instances from %d to %d on the lab's nodes; a Docker VM\n"+
		"restart had reset it. A node that went NotReady meanwhile needs a restart: see 'labctl status'.\n\n",
		limit, capacity.InotifyInstances)
}

// streamSubmittedRun submits one operation, streams its transcript to out, and
// returns an error if the run did not succeed. The engine must already be
// started.
func streamSubmittedRun(ctx context.Context, out io.Writer, st *store.Store, verb string, submit func() (string, error)) error {
	id, err := submit()
	if err != nil {
		return err // includes the actionable *run.LockConflictError message
	}
	fmt.Fprintf(out, "%s started (run %s). Following output - Ctrl-C stops it.\n\n", verb, id)

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
