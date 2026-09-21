// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/sagar2395/snowopslabs/internal/run"
	incsvc "github.com/sagar2395/snowopslabs/internal/service/incident"
	"github.com/sagar2395/snowopslabs/internal/store"
)

// `labctl incident inject|resolve` run inject.sh and resolve.sh through the
// run engine, so they appear in `labctl runs` and the web console. After a run
// succeeds, the CLI updates the incident engine's own state (active incident,
// hints, results) with MarkInjected or RecordScriptResolved.

// incidentEngineFactory builds an incident service on a new run engine. Tests
// replace it. Always defer cleanup, which shuts the engine down and closes the
// store.
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

// runIncidentOp starts the run engine, calls submit, and streams the run,
// returning an error if it fails. submit carries the fault's target, which the
// scripts receive as TARGET_*.
func runIncidentOp(cmd *cobra.Command, verb, name string, submit func(context.Context, *incsvc.Service) (string, error)) error {
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
