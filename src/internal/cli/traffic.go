// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"path/filepath"
	"strings"

	"github.com/sagar2395/snowopslabs/internal/traffic"
	"github.com/spf13/cobra"
)

var trafficOpts traffic.Options

var trafficCmd = &cobra.Command{
	Use:   "traffic",
	Short: "Generate synthetic load against lab apps (k6)",
	Long: `Runs a k6 load generator in-cluster with a selectable profile, so
scenarios and incidents play out under realistic traffic. Watch the impact
in Grafana next to the app's own request metrics.`,
}

var trafficStartCmd = &cobra.Command{
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

The target defaults to go-api's root (/), which is access-logged, so traffic
shows up in 'kubectl logs deploy/go-api'. Point elsewhere with --target, e.g.
--target http://echo-server.echo-server.svc.cluster.local:8080/`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := trafficOpts.Validate(cfg.ProjectRoot); err != nil {
			return err
		}
		for k, v := range trafficOpts.Env() {
			exec.SetEnv(k, v)
		}
		_, err := exec.RunScriptStreamed("Start traffic: "+trafficOpts.Profile,
			filepath.Join(traffic.ScriptDir, "start.sh"))
		return err
	},
}

var trafficStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the traffic generator and clean up",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, err := exec.RunScriptStreamed("Stop traffic",
			filepath.Join(traffic.ScriptDir, "stop.sh"))
		return err
	},
}

var trafficStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show traffic generator status and recent k6 output",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, err := exec.RunScriptStreamed("Traffic status",
			filepath.Join(traffic.ScriptDir, "status.sh"))
		return err
	},
}

var trafficProfilesCmd = &cobra.Command{
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

func init() {
	trafficStartCmd.Flags().StringVar(&trafficOpts.Profile, "profile", "steady", "load profile (see 'labctl traffic profiles')")
	trafficStartCmd.Flags().StringVar(&trafficOpts.Target, "target", "", "URL to load (default: go-api health endpoint, in-cluster)")
	trafficStartCmd.Flags().IntVar(&trafficOpts.RPS, "rps", 10, "requests per second (baseline for the spike profile)")
	trafficStartCmd.Flags().StringVar(&trafficOpts.Duration, "duration", "", "run length, e.g. 10m or 1h30m (default: profile-specific)")
	trafficStartCmd.Flags().StringVar(&trafficOpts.Method, "method", "", "HTTP method for write/errors profiles (GET/POST/PUT/PATCH/DELETE; default: profile-specific)")

	trafficCmd.AddCommand(trafficStartCmd)
	trafficCmd.AddCommand(trafficStopCmd)
	trafficCmd.AddCommand(trafficStatusCmd)
	trafficCmd.AddCommand(trafficProfilesCmd)
	rootCmd.AddCommand(trafficCmd)
}
