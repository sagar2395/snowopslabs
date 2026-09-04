// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/sagar2395/snowopslabs/internal/run"
	incsvc "github.com/sagar2395/snowopslabs/internal/service/incident"
	"github.com/sagar2395/snowopslabs/internal/store"
)

// `labctl incident inject|resolve` run their fault scripts through the durable
// run engine: inject.sh / resolve.sh become recorded, cancellable
// runs visible in `labctl runs` and the web console. The incident engine still
// owns the declarative state — active.yaml, hints, and the MTTR/score record —
// which the CLI updates once the durable run succeeds (MarkInjected /
// RecordScriptResolved), so the game-day scoring is unchanged.

// incidentEngineFactory builds an incident service over the shared run-engine
// bootstrap. Overridable in tests. cleanup shuts the engine down and closes the
// store; always defer it.
var incidentEngineFactory = func(ctx context.Context) (*incsvc.Service, *store.Store, *run.Engine, func(), error) {
	eng, st, cleanup, err := newRunEngine(ctx)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	svc, err := incsvc.New(eng, st, incsvc.WithEnv(scriptEnv()))
	if err != nil {
		cleanup()
		return nil, nil, nil, nil, err
	}
	return svc, st, eng, cleanup, nil
}

// runIncidentOp submits an inject/resolve for a fault through the durable engine
// and streams it, exiting non-zero if the run fails. target carries the fault's
// workload to the scripts as TARGET_*.
func runIncidentOp(cmd *cobra.Command, verb, name string, target incsvc.Target, submit func(context.Context, *incsvc.Service) (string, error)) error {
	ctx := cmd.Context()
	svc, st, eng, cleanup, err := incidentEngineFactory(ctx)
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
