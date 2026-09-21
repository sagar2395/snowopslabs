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

// `labctl scenario up|down` run through the run engine: an activation is one
// recorded, cancellable run, and each component it installs is written to the
// inventory. The scenario engine does the installing and keeps the activation
// markers.

// scenarioEngineFactory builds a scenario service on a new run engine, using
// the global scenario engine. Tests replace it.
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

// runScenarioOp starts the run engine, calls submit, and streams the run,
// returning an error if it fails.
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

// runScenarioReset deactivates the scenario if it is active and activates it
// again, as two runs, so a learner can retry without rebuilding the lab.
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
