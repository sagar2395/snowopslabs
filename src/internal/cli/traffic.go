// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"context"
	"io"
	"maps"
	"strings"

	trafficsvc "github.com/snowops-oss/snowopslabs/internal/service/traffic"
	"github.com/snowops-oss/snowopslabs/internal/traffic"
	"github.com/spf13/cobra"
)

func trafficCmd() *cobra.Command {
	var opts traffic.Options
	cmd := &cobra.Command{
		Use:   "traffic",
		Short: "Generate synthetic load against lab apps (k6)",
		Long: `Runs a k6 load generator in-cluster with a selectable profile, so
scenarios and incidents play out under realistic traffic. Watch the impact
in Grafana next to the app's own request metrics.`,
	}

	start := &cobra.Command{
		Use:   "start",
		Short: "Start (or restart) the traffic generator",
		Long: `Starts the k6 generator with the chosen profile. Starting while a
run is active replaces it with the new settings.

Profiles (services/traffic/profiles/):
  steady  constant rate for the duration (default 10m)
  spike   baseline, sharp 10x spike, recovery (~6m fixed shape)
  soak    sustained moderate load (default 2h)
  browse  weighted read-mix across /, /version, /health
  write   POST a JSON body (best against echo-server /echo)
  errors  toggle go-api into simulated failure under load

The target defaults to the app chosen with --app (go-api when none is), at
its in-cluster root (/), which is access-logged. Point anywhere else with
--target, e.g. --target http://echo-server.echo-server.svc.cluster.local:8080/`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			requested := opts
			if requested.Target == "" {
				requested.Target = scenes.Workload.URL()
			}
			return runTrafficOperation(cmd.Context(), cmd.OutOrStdout(), "Start traffic: "+requested.Profile,
				func(ctx context.Context, svc *trafficsvc.Service) (string, error) {
					return svc.Start(ctx, requested)
				})
		},
	}

	stop := &cobra.Command{
		Use:   "stop",
		Short: "Stop the traffic generator and clean up",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runTrafficOperation(cmd.Context(), cmd.OutOrStdout(), "Stop traffic",
				func(ctx context.Context, svc *trafficsvc.Service) (string, error) {
					return svc.Stop(ctx)
				})
		},
	}

	status := &cobra.Command{
		Use:   "status",
		Short: "Show traffic generator status and recent k6 output",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runTrafficOperation(cmd.Context(), cmd.OutOrStdout(), "Traffic status",
				func(ctx context.Context, svc *trafficsvc.Service) (string, error) {
					return svc.Status(ctx)
				})
		},
	}

	profiles := &cobra.Command{
		Use:   "profiles",
		Short: "List available traffic profiles",
		RunE: func(cmd *cobra.Command, args []string) error {
			profiles, err := traffic.Profiles(cfg.ProjectRoot)
			if err != nil {
				return err
			}
			cmd.Println(strings.Join(profiles, "\n"))
			return nil
		},
	}

	start.Flags().StringVar(&opts.Profile, "profile", "steady", "load profile (see 'labctl traffic profiles')")
	start.Flags().StringVar(&opts.Target, "target", "", "URL to load (default: the --app workload's root /, in-cluster)")
	start.Flags().IntVar(&opts.RPS, "rps", 10, "requests per second (baseline for the spike profile)")
	start.Flags().StringVar(&opts.Duration, "duration", "", "run length, e.g. 10m or 1h30m (default: profile-specific)")
	start.Flags().StringVar(&opts.Method, "method", "", "HTTP method for write/errors profiles (GET/POST/PUT/PATCH/DELETE; default: profile-specific)")

	cmd.AddCommand(start, stop, status, profiles)
	return cmd
}

func runTrafficOperation(ctx context.Context, out io.Writer, verb string,
	submit func(context.Context, *trafficsvc.Service) (string, error),
) error {
	eng, st, cleanup, err := newRunEngine(ctx)
	if err != nil {
		return err
	}
	defer cleanup()

	env := make(map[string]string, len(cfg.ScriptEnv))
	maps.Copy(env, cfg.ScriptEnv)
	maps.Copy(env, scriptEnv())
	svc, err := trafficsvc.New(eng, cfg.ProjectRoot, env)
	if err != nil {
		return err
	}
	return runEngineOperation(ctx, out, eng, st, verb, func() (string, error) {
		return submit(ctx, svc)
	})
}

func init() {
	rootCmd.AddCommand(trafficCmd())
}
