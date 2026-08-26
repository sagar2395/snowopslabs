// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/sagar2395/snowopslabs/internal/scaffold"
	scenariopkg "github.com/sagar2395/snowopslabs/internal/scenario"
	"github.com/sagar2395/snowopslabs/pkg/checks"
	"github.com/spf13/cobra"
)

var scenarioNewForce bool
var scenarioUpForce bool
var scenarioDeployPrereqs bool

var scenarioNewCmd = &cobra.Command{
	Use:   "new <name>",
	Short: "Scaffold a new scenario (valid + verify-ready)",
	Long: `Create scenarios/<name>/ with a valid v2 scenario.yaml and a passing readiness
check, so 'labctl scenario verify <name>' is green out of the box. Edit it from
there. The file carries a $schema modeline for inline editor validation.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := scaffold.Scenario(cfg.ProjectRoot, args[0], scenarioNewForce)
		if err != nil {
			return err
		}
		fmt.Printf("Created %s\n", dir)
		fmt.Printf("\nNext:\n  labctl scenario verify %s\n  $EDITOR %s\n", args[0], dir+"/scenario.yaml")
		return nil
	},
}

var scenarioCmd = &cobra.Command{
	Use:   "scenario",
	Short: "Manage lab scenarios",
}

var scenarioListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available scenarios",
	RunE: func(cmd *cobra.Command, args []string) error {
		scenarios := scenes.List()
		if len(scenarios) == 0 {
			fmt.Println("No scenarios found in scenarios/ directory.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "NAME\tDISPLAY NAME\tCATEGORY\tSOURCE\tSTATUS")
		for _, s := range scenarios {
			status := "inactive"
			if s.Active {
				status = "active"
			}
			source := s.Source
			if source == "" {
				source = "repo"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", s.Name, s.DisplayName, s.Category, source, status)
		}
		_ = w.Flush()

		fmt.Println("\nSee what a scenario installs, its objectives and checks:")
		fmt.Println("  labctl scenario info <name>")

		if errs := scenes.LoadErrors(); len(errs) > 0 {
			fmt.Fprintf(os.Stderr, "\nWarning: %d scenario(s) failed to load (run with -v for details):\n", len(errs))
			for key, err := range errs {
				fmt.Fprintf(os.Stderr, "  %s: %v\n", key, err)
			}
		}
		return nil
	},
}

var scenarioUpCmd = &cobra.Command{
	Use:   "up [scenario-name]",
	Short: "Activate a scenario",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Make sure the scenario's prerequisite apps are actually running before
		// we install into them. Without --deploy-prereqs this returns an error
		// naming the exact commands to fix it.
		if s, err := scenes.Get(args[0]); err == nil {
			if err := ensureAppsDeployed(cmd.Context(), s.Prerequisites.Apps, scenarioDeployPrereqs); err != nil {
				return err
			}
		}
		err := scenes.Up(args[0], exec, scenarioUpForce)
		if errors.Is(err, scenariopkg.ErrAlreadyActive) {
			fmt.Fprintf(os.Stderr, "Scenario %s is already active. Re-run with --force to reinstall.\n", args[0])
			return nil
		}
		return err
	},
}

var scenarioDownCmd = &cobra.Command{
	Use:   "down [scenario-name]",
	Short: "Deactivate a scenario",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return scenes.Down(args[0], exec)
	},
}

var scenarioStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show scenario status",
	RunE: func(cmd *cobra.Command, args []string) error {
		statuses := scenes.Status()
		if len(statuses) == 0 {
			fmt.Println("No scenarios found.")
			return nil
		}

		hasActive := false
		for _, s := range statuses {
			if s.Active {
				hasActive = true
				break
			}
		}

		if !hasActive {
			fmt.Println("No scenarios are currently active.")
			fmt.Println("Use 'labctl scenario list' to see available scenarios.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "NAME\tCATEGORY\tSTATUS")
		for _, s := range statuses {
			if s.Active {
				fmt.Fprintf(w, "%s\t%s\tactive\n", s.Name, s.Category)
			}
		}
		_ = w.Flush()
		fmt.Println("\nInspect a scenario: labctl scenario info <name>")
		return nil
	},
}

var (
	verifyWatch        bool
	verifyInterval     time.Duration
	verifyTimeout      time.Duration
	verifyCheckTimeout time.Duration
)

var scenarioVerifyCmd = &cobra.Command{
	Use:   "verify [scenario-name]",
	Short: "Run a scenario's checks and report pass/fail",
	Long: `Runs the machine-verifiable checks declared in the scenario's
checks block (scenario format v2) and reports each result. Exits non-zero
if any check fails, so it is safe to use in CI and scripts.

With --watch, checks are re-run every --interval until they all pass or
--timeout elapses — useful right after 'scenario up' while pods settle.`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true, // a failing check is a result, not a usage error
	RunE: func(cmd *cobra.Command, args []string) error {
		runner := newCheckRunner()
		ctx := context.Background()
		deadline := time.Now().Add(verifyTimeout)

		for {
			results, err := scenes.Verify(ctx, args[0], runner)
			if err != nil {
				return err
			}
			printCheckResults(results)
			if checks.AllPass(results) {
				fmt.Printf("All %d checks passed.\n", len(results))
				return nil
			}
			failed := 0
			for _, r := range results {
				if !r.Pass {
					failed++
				}
			}
			if !verifyWatch || time.Now().After(deadline) {
				if !verifyWatch {
					fmt.Fprintln(os.Stderr, "\nChecks can fail while pods are still starting. Re-run with --watch to wait,")
					fmt.Fprintln(os.Stderr, "or inspect with: kubectl get pods -A")
				}
				return fmt.Errorf("%d of %d checks failed", failed, len(results))
			}
			fmt.Printf("\n%d of %d checks failing — retrying in %s (until %s)...\n\n",
				failed, len(results), verifyInterval, deadline.Format("15:04:05"))
			time.Sleep(verifyInterval)
		}
	},
}

// newCheckRunner builds a check runner wired to the lab's config: the
// Prometheus endpoint (PROMETHEUS_URL env override, else the ingress
// hostname) and the standard script environment.
func newCheckRunner() *checks.Runner {
	r := checks.NewRunner()
	r.DefaultTimeout = verifyCheckTimeout
	promURL := os.Getenv("PROMETHEUS_URL")
	if promURL == "" {
		promURL = "http://prometheus." + cfg.DomainSuffix
	}
	r.PrometheusURL = promURL
	r.Env = []string{
		"DOMAIN_SUFFIX=" + cfg.DomainSuffix,
		"MONITORING_NAMESPACE=" + cfg.MonitoringNamespace,
		"PROJECT_ROOT=" + cfg.ProjectRoot,
	}
	return r
}

func printCheckResults(results []checks.Result) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, r := range results {
		mark := "PASS"
		if !r.Pass {
			mark = "FAIL"
		}
		detail := ""
		if r.Got != "" || r.Want != "" {
			detail = fmt.Sprintf("got: %s, want: %s", orDash(r.Got), orDash(r.Want))
		}
		if r.Error != "" {
			detail = "error: " + r.Error
		}
		fmt.Fprintf(w, "%s\t%s\t(%s)\t%s\t%dms\n", mark, r.Name, r.Type, detail, r.DurationMS)
	}
	_ = w.Flush()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

var scenarioInfoCmd = &cobra.Command{
	Use:   "info [scenario-name]",
	Short: "Show detailed information about a scenario",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := scenes.Get(args[0])
		if err != nil {
			return err
		}

		fmt.Printf("Name:        %s\n", s.Name)
		fmt.Printf("Display:     %s\n", s.DisplayName)
		fmt.Printf("Category:    %s\n", s.Category)
		fmt.Printf("Description: %s\n", s.Description)

		status := "inactive"
		if s.Active {
			status = "active"
		}
		fmt.Printf("Status:      %s\n", status)

		if len(s.Prerequisites.Platform) > 0 {
			fmt.Printf("\nPrerequisites (platform):\n")
			for _, p := range s.Prerequisites.Platform {
				fmt.Printf("  - %s\n", p)
			}
		}
		if len(s.Prerequisites.Apps) > 0 {
			fmt.Printf("\nPrerequisites (apps):\n")
			for _, a := range s.Prerequisites.Apps {
				fmt.Printf("  - %s\n", a)
			}
		}

		if len(s.Objectives) > 0 {
			fmt.Printf("\nObjectives:\n")
			for _, o := range s.Objectives {
				fmt.Printf("  - %s\n", o)
			}
		}

		printComponent := func(c scenariopkg.Component, indent string) {
			fmt.Printf("%s- %s [%s]", indent, c.Name, c.Type)
			if c.Chart != "" {
				fmt.Printf(" chart=%s", c.Chart)
			}
			if c.Namespace != "" {
				fmt.Printf(" ns=%s", c.Namespace)
			}
			fmt.Println()
		}

		if len(s.Stages) > 0 {
			fmt.Printf("\nStages (%d):\n", len(s.Stages))
			for _, st := range s.Stages {
				fmt.Printf("  %s:\n", st.Name)
				for _, c := range st.Components {
					printComponent(c, "    ")
				}
			}
		} else {
			fmt.Printf("\nComponents (%d):\n", len(s.Components))
			for _, c := range s.Components {
				printComponent(c, "  ")
			}
		}

		if len(s.Checks) > 0 {
			fmt.Printf("\nChecks (%d) — run 'labctl scenario verify %s':\n", len(s.Checks), s.Name)
			for _, c := range s.Checks {
				fmt.Printf("  - %s [%s]\n", c.Name, c.Type)
			}
		}

		if len(s.Explore.URLs) > 0 || len(s.Explore.Commands) > 0 || len(s.Explore.Tips) > 0 {
			fmt.Println("\nExplore:")
			for _, u := range s.Explore.URLs {
				fmt.Printf("  URL: %-25s %s\n", u.Label, scenes.ResolveTemplate(u.URL))
			}
			for _, c := range s.Explore.Commands {
				fmt.Printf("  CMD: %s\n       %s\n", c.Label, scenes.ResolveTemplate(c.Command))
			}
			for _, t := range s.Explore.Tips {
				fmt.Printf("  TIP: %s\n", scenes.ResolveTemplate(t))
			}
		}

		return nil
	},
}

func init() {
	scenarioVerifyCmd.Flags().BoolVar(&verifyWatch, "watch", false, "re-run checks until they all pass or --timeout elapses")
	scenarioVerifyCmd.Flags().DurationVar(&verifyInterval, "interval", 10*time.Second, "delay between re-runs in --watch mode")
	scenarioVerifyCmd.Flags().DurationVar(&verifyTimeout, "timeout", 5*time.Minute, "overall deadline in --watch mode")
	scenarioVerifyCmd.Flags().DurationVar(&verifyCheckTimeout, "check-timeout", 30*time.Second, "per-check timeout")

	scenarioNewCmd.Flags().BoolVar(&scenarioNewForce, "force", false, "overwrite the scenario if it already exists")
	scenarioUpCmd.Flags().BoolVar(&scenarioUpForce, "force", false, "reinstall even if the scenario is already active")
	scenarioUpCmd.Flags().BoolVar(&scenarioDeployPrereqs, "deploy-prereqs", false, "build and deploy any prerequisite apps that are not yet running")

	scenarioCmd.AddCommand(scenarioNewCmd)
	scenarioCmd.AddCommand(scenarioListCmd)
	scenarioCmd.AddCommand(scenarioUpCmd)
	scenarioCmd.AddCommand(scenarioDownCmd)
	scenarioCmd.AddCommand(scenarioStatusCmd)
	scenarioCmd.AddCommand(scenarioInfoCmd)
	scenarioCmd.AddCommand(scenarioVerifyCmd)
	rootCmd.AddCommand(scenarioCmd)
}
