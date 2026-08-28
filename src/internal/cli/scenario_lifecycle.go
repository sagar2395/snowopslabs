// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sagar2395/snowopslabs/internal/run"
	scnsvc "github.com/sagar2395/snowopslabs/internal/service/scenario"
	"github.com/sagar2395/snowopslabs/internal/store"
	"github.com/sagar2395/snowopslabs/internal/toolchain"
)

// `labctl scenario up|down` run activation/deactivation through the durable run
// engine (W4-T01 wiring): the whole multi-component activation is one recorded,
// cancellable run whose transcript streams to the terminal and `labctl runs`, and
// each component it installs is written to the store inventory. The scenario
// engine still owns the declarative install logic and its active-state markers.

// scenarioEngineFactory builds a scenario service over the shared run-engine
// bootstrap, driving the process-wide scenario engine. Overridable in tests.
var scenarioEngineFactory = func(ctx context.Context) (*scnsvc.Service, *store.Store, *run.Engine, func(), error) {
	eng, st, cleanup, err := newRunEngine(ctx)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	svc, err := scnsvc.New(eng, st, scenes, toolchain.NewExec(), cfg.ProjectRoot, scnsvc.WithEnv(scriptEnv()))
	if err != nil {
		cleanup()
		return nil, nil, nil, nil, err
	}
	return svc, st, eng, cleanup, nil
}

// runScenarioOp submits an activate/deactivate through the durable engine and
// streams it, exiting non-zero if the run fails.
func runScenarioOp(cmd *cobra.Command, verb, name string, submit func(context.Context, *scnsvc.Service) (string, error)) error {
	ctx := cmd.Context()
	svc, st, eng, cleanup, err := scenarioEngineFactory(ctx)
	if err != nil {
		return err
	}
	defer cleanup()
	if err := eng.Start(ctx); err != nil {
		return err
	}
	return streamSubmittedRun(ctx, cmd.OutOrStdout(), st, verb+" "+name, func() (string, error) {
		return submit(ctx, svc)
	})
}

// runScenarioReset is the scenario-retry fast-path (W4-T06): tear the scenario
// down (if active) and re-activate it, both as recorded durable runs, in one
// command — so retrying after a failed attempt is a single fast step rather than
// a full lab teardown and rebuild. Re-activation forces (components are
// idempotent helm-upgrade/kubectl-apply, so a converge is quick).
func runScenarioReset(cmd *cobra.Command, name string) error {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()

	svc, st, eng, cleanup, err := scenarioEngineFactory(ctx)
	if err != nil {
		return err
	}
	defer cleanup()
	if err := eng.Start(ctx); err != nil {
		return err
	}

	if s, gerr := scenes.Get(name); gerr == nil && s.Active {
		fmt.Fprintf(out, "Resetting %s — tearing down first…\n\n", name)
		if err := streamSubmittedRun(ctx, out, st, "deactivate "+name, func() (string, error) {
			return svc.Deactivate(ctx, name)
		}); err != nil {
			return err
		}
		fmt.Fprintln(out)
	}
	fmt.Fprintf(out, "Re-activating %s…\n\n", name)
	return streamSubmittedRun(ctx, out, st, "activate "+name, func() (string, error) {
		return svc.Activate(ctx, name, true)
	})
}
