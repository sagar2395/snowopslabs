// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/sagar2395/snowopslabs/internal/k8s"
	"github.com/sagar2395/snowopslabs/internal/run"
	labsvc "github.com/sagar2395/snowopslabs/internal/service/lab"
	"github.com/sagar2395/snowopslabs/internal/store"
)

// `labctl lab up|down|status` runs cluster lifecycle through the durable run
// engine: every bring-up and teardown is cancellable, time-bounded,
// and recorded in the same store `labctl runs` reads, so an operation can be
// followed live and read back tomorrow. This replaces the fire-and-forget
// executor path for the cluster itself; snapshot/restore/reset stay on their
// existing path until component state lands.

// labEngineFactory builds a lab service over the shared run-engine bootstrap
// (see newRunEngine). The engine is returned UN-started: `up`/`down` start it,
// while read-only `status` must not. Overridable in tests so the lab commands
// can be exercised hermetically with a Fake runner. cleanup shuts the engine
// down (a no-op if never started) and closes the store; always defer it.
var labEngineFactory = func(ctx context.Context) (svc *labsvc.Service, st *store.Store, eng *run.Engine, cleanup func(), err error) {
	eng, st, cleanup, err = newRunEngine(ctx)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	svc, err = labsvc.New(eng, st, cfg.ClusterName,
		labsvc.WithEnv(scriptEnv()),
		labsvc.WithProber(clusterProber()))
	if err != nil {
		cleanup()
		return nil, nil, nil, nil, err
	}
	return svc, st, eng, cleanup, nil
}

// clusterProber answers `lab status --live` with a real kubectl probe. It never
// returns an error: an unreachable cluster is a reachable=false result, not a
// failure of the status command.
func clusterProber() labsvc.Prober {
	return func(ctx context.Context) (labsvc.Liveness, error) {
		pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		info, err := k8s.GetClusterInfo(pctx)
		if err != nil || info == nil || !info.Connected {
			l := labsvc.Liveness{Reachable: false}
			if err != nil {
				l.Detail = err.Error()
			}
			return l, nil
		}
		detail := info.K8sVersion
		if info.NodeCount > 0 {
			detail = fmt.Sprintf("%s, %d node(s)", info.K8sVersion, info.NodeCount)
		}
		return labsvc.Liveness{Reachable: true, Context: info.Context, Detail: detail}, nil
	}
}

// runLabOperation submits an up/down operation and streams its transcript to the
// terminal, exiting non-zero if the run does not succeed. The submit closure
// picks up/down.
func runLabOperation(cmd *cobra.Command, verb string, submit func(context.Context, *labsvc.Service) (string, error)) error {
	ctx := cmd.Context()

	svc, st, eng, cleanup, err := labEngineFactory(ctx)
	if err != nil {
		return err
	}
	defer cleanup()

	return runEngineOperation(ctx, cmd.OutOrStdout(), eng, st, "lab "+verb, func() (string, error) {
		return submit(ctx, svc)
	})
}

func labUpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "up [runtime]",
		Short: "Bring the cluster up through the durable run engine",
		Long: `Provisions the cluster for a runtime (default: the configured profile)
as a recorded, cancellable run. Follow or cancel it later with 'labctl runs'.`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt := labRuntimeArg(args)
			return runLabOperation(cmd, "up", func(ctx context.Context, svc *labsvc.Service) (string, error) {
				return svc.Up(ctx, rt)
			})
		},
	}
}

func labDownCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "down [runtime]",
		Short:        "Tear the cluster down through the durable run engine",
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt := labRuntimeArg(args)
			return runLabOperation(cmd, "down", func(ctx context.Context, svc *labsvc.Service) (string, error) {
				return svc.Down(ctx, rt)
			})
		},
	}
}

func labStatusCmd() *cobra.Command {
	var live bool
	cmd := &cobra.Command{
		Use:          "status",
		Short:        "Show the lab's state from the run history (fast); --live probes the cluster",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			// Status is read-only: build the service but never Start the engine.
			svc, _, _, cleanup, err := labEngineFactory(ctx)
			if err != nil {
				return err
			}
			defer cleanup()

			st, err := svc.Status(ctx, live)
			if err != nil {
				return err
			}
			return writeLabStatus(cmd.OutOrStdout(), st)
		},
	}
	cmd.Flags().BoolVar(&live, "live", false, "additionally probe the cluster for reachability")
	return cmd
}

// writeLabStatus renders a lab Status for a terminal.
func writeLabStatus(out io.Writer, st labsvc.Status) error {
	fmt.Fprintf(out, "State:     %s\n", st.State)
	if st.Runtime != "" {
		fmt.Fprintf(out, "Runtime:   %s\n", st.Runtime)
	}
	if st.RunID != "" {
		fmt.Fprintf(out, "Last run:  %s", st.RunID)
		if !st.Since.IsZero() {
			fmt.Fprintf(out, " (%s)", relativeTime(st.Since))
		}
		fmt.Fprintln(out)
	}
	if st.State == labsvc.StateUnknown {
		fmt.Fprintln(out, "\nNo lab operation recorded yet. Bring one up with: labctl lab up")
	}
	if st.Live != nil {
		fmt.Fprintln(out)
		if st.Live.Reachable {
			fmt.Fprintf(out, "Cluster:   reachable")
			if st.Live.Context != "" {
				fmt.Fprintf(out, " (context %s", st.Live.Context)
				if st.Live.Detail != "" {
					fmt.Fprintf(out, ", %s", st.Live.Detail)
				}
				fmt.Fprint(out, ")")
			}
			fmt.Fprintln(out)
		} else {
			fmt.Fprint(out, "Cluster:   unreachable")
			if st.Live.Detail != "" {
				fmt.Fprintf(out, " (%s)", st.Live.Detail)
			}
			fmt.Fprintln(out)
		}
	}
	return nil
}

// labRuntimeArg resolves the runtime from an optional positional arg, defaulting
// to the configured profile (k3d/kind/incluster).
func labRuntimeArg(args []string) string {
	if len(args) == 1 && args[0] != "" {
		return args[0]
	}
	return cfg.Profile
}

func init() {
	labCmd.AddCommand(labUpCmd())
	labCmd.AddCommand(labDownCmd())
	labCmd.AddCommand(labStatusCmd())
}
