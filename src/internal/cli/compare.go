// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/sagar2395/snowopslabs/internal/compare"
	scnsvc "github.com/sagar2395/snowopslabs/internal/service/scenario"
	"github.com/sagar2395/snowopslabs/internal/traffic"
	"github.com/sagar2395/snowopslabs/internal/workload"
)

var compareOpts struct {
	apps    []string
	profile string
	rps     int
	warmup  time.Duration
	window  time.Duration
	keep    bool
}

func compareStore() *compare.Store {
	return compare.NewStore(filepath.Join(cfg.ProjectRoot, ".labctl", "history"))
}

// cliLab implements compare.Lab using the same engines as the scenario and
// traffic commands.
type cliLab struct {
	cmd  *cobra.Command
	keep bool
}

// Bind binds every engine to the named app, so the next scenario activation
// uses it.
func (l *cliLab) Bind(app string) (workload.Workload, error) {
	if err := bindWorkload(app, true); err != nil {
		return workload.Workload{}, err
	}
	return scenes.Workload, nil
}

func (l *cliLab) EnsureDeployed(ctx context.Context, w workload.Workload) error {
	return ensureAppsDeployed(ctx, []string{w.Name}, true)
}

func (l *cliLab) ScenarioUp(_ context.Context, name string, _ workload.Workload) error {
	return runScenarioOp(l.cmd, "activate", name, func(ctx context.Context, svc *scnsvc.Service) (string, error) {
		return svc.ActivateWithParams(ctx, name, true, nil)
	})
}

func (l *cliLab) ScenarioDown(_ context.Context, name string, _ workload.Workload) error {
	if l.keep {
		return nil
	}
	return runScenarioOp(l.cmd, "deactivate", name, func(ctx context.Context, svc *scnsvc.Service) (string, error) {
		return svc.Deactivate(ctx, name)
	})
}

func (l *cliLab) TrafficStart(_ context.Context, w workload.Workload, profile string, rps int, d time.Duration) error {
	o := traffic.Options{
		Profile: profile,
		// Target the in-cluster URL, not the ingress, so every app is reached
		// the same way.
		Target:   w.URL(),
		RPS:      rps,
		Duration: d.String(),
	}
	if err := o.Validate(cfg.ProjectRoot); err != nil {
		return err
	}
	for k, v := range o.Env() {
		scriptExec.SetEnv(k, v)
	}
	_, err := scriptExec.RunScriptStreamed("Load "+w.Name, filepath.Join(traffic.ScriptDir, "start.sh"))
	return err
}

func (l *cliLab) TrafficStop(_ context.Context) error {
	_, err := scriptExec.RunScriptStreamed("Stop traffic", filepath.Join(traffic.ScriptDir, "stop.sh"))
	return err
}

var compareCmd = &cobra.Command{
	Use:   "compare",
	Short: "Measure one scenario against two or more application stacks",
	Long: `Run the same scenario against different workloads and diff the result.

Every app is measured under identical conditions — same traffic profile, same
request rate, same window length, and a warmup that is excluded from the
measurement — because a comparison that skips those measures the runtime's
warm-up rather than the application.

Apps are measured one at a time, never together: two workloads under load on the
same node contend for CPU, and whichever ran second would look slower.`,
}

var compareRunCmd = &cobra.Command{
	Use:   "run <scenario>",
	Short: "Measure a scenario against several apps and report the difference",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		o := compare.Options{
			Scenario: args[0],
			Apps:     compareOpts.apps,
			Profile:  compareOpts.profile,
			RPS:      compareOpts.rps,
			Warmup:   compareOpts.warmup,
			Window:   compareOpts.window,
		}
		if err := o.Validate(); err != nil {
			return err
		}
		if _, err := scenes.Get(o.Scenario); err != nil {
			return err
		}

		per := o.Warmup + o.Window
		fmt.Fprintf(os.Stderr,
			"Comparing %s across %s.\nEach app: %s warmup (excluded) + %s measured — about %s in total.\n\n",
			o.Scenario, strings.Join(o.Apps, ", "), o.Warmup, o.Window,
			(per * time.Duration(len(o.Apps))).Round(time.Minute))

		// The same runner scenario checks use, so both read the same
		// Prometheus.
		runner := newCheckRunner()

		rep, err := compare.Run(cmd.Context(), o, &cliLab{cmd: cmd, keep: compareOpts.keep}, runner, compare.RealSleeper)
		if err != nil {
			return err
		}

		id, err := compareStore().Append(rep)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: the comparison ran but could not be recorded: %v\n", err)
		}

		fmt.Println()
		compare.Render(cmd.OutOrStdout(), o, rep.Measurements)
		if id != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "\nRecorded as %s — 'labctl compare show %s'\n", id, id)
		}
		return nil
	},
}

var compareListCmd = &cobra.Command{
	Use:   "list",
	Short: "List recorded comparisons, newest first",
	RunE: func(cmd *cobra.Command, _ []string) error {
		sums, err := compareStore().Summaries()
		if err != nil {
			return err
		}
		if len(sums) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No comparisons recorded yet. Run one:")
			fmt.Fprintln(cmd.OutOrStdout(), "  labctl compare run autoscaling-under-load --apps go-api,java-api")
			return nil
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "ID\tWHEN\tSCENARIO\tAPPS")
		for _, s := range sums {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", s.ID, s.When.Local().Format("2006-01-02 15:04"),
				s.Scenario, strings.Join(s.Apps, ", "))
		}
		return w.Flush()
	},
}

var compareShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show a recorded comparison",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		rep, err := compareStore().Get(args[0])
		if err != nil {
			return err
		}
		compare.Render(cmd.OutOrStdout(), rep.Options(), rep.Measurements)
		return nil
	},
}

func init() {
	compareRunCmd.Flags().StringSliceVar(&compareOpts.apps, "apps", nil,
		"apps to measure, in order; the first is the baseline (e.g. go-api,java-api)")
	// "steady" by default: a constant request rate, so differences come from
	// the workload rather than the load.
	compareRunCmd.Flags().StringVar(&compareOpts.profile, "profile", "steady", "traffic profile")
	compareRunCmd.Flags().IntVar(&compareOpts.rps, "rps", 40, "offered request rate, identical for every app")
	compareRunCmd.Flags().DurationVar(&compareOpts.warmup, "warmup", time.Minute,
		"load time excluded from the measurement, so JIT and cache warm-up are not compared")
	compareRunCmd.Flags().DurationVar(&compareOpts.window, "window", 3*time.Minute,
		"length of the measured window (at least 2m, so it spans several scrapes)")
	compareRunCmd.Flags().BoolVar(&compareOpts.keep, "keep", false,
		"leave the scenario active after the last app (for inspecting the lab afterwards)")

	compareCmd.AddCommand(compareRunCmd, compareListCmd, compareShowCmd)
	rootCmd.AddCommand(compareCmd)
}
