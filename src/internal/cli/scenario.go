// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	resultspkg "github.com/sagar2395/snowopslabs/internal/results"
	"github.com/sagar2395/snowopslabs/internal/scaffold"
	scenariopkg "github.com/sagar2395/snowopslabs/internal/scenario"
	scnsvc "github.com/sagar2395/snowopslabs/internal/service/scenario"
	"github.com/sagar2395/snowopslabs/internal/workload"
	"github.com/sagar2395/snowopslabs/pkg/checks"
	"github.com/spf13/cobra"
)

var scenarioNewForce bool
var scenarioUpForce bool
var scenarioDeployPrereqs bool
var scenarioUpParams map[string]string

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
		name := args[0]
		// Make sure the scenario's prerequisite apps are actually running before
		// we install into them. Without --deploy-prereqs this returns an error
		// naming the exact commands to fix it.
		s, err := scenes.Get(name)
		if err != nil {
			return err
		}
		if err := ensureAppsDeployed(cmd.Context(), scenes.ResolvedPrereqApps(s), scenarioDeployPrereqs); err != nil {
			return err
		}
		// Platform prerequisites (opencost, prometheus, ingress, …) are not
		// auto-installed; warn up front if any is missing so a later check
		// failure isn't the first the learner hears of it.
		warnMissingPlatformPrereqs(cmd.Context(), os.Stderr, s.Prerequisites.Platform)
		// An already-active scenario is a friendly no-op unless --force (which
		// re-installs; components are idempotent).
		if s.Active && !scenarioUpForce {
			fmt.Fprintf(os.Stderr, "Scenario %s is already active. Re-run with --force to reinstall.\n", name)
			return nil
		}
		err = runScenarioOp(cmd, "activate", name, func(ctx context.Context, svc *scnsvc.Service) (string, error) {
			return svc.ActivateWithParams(ctx, name, scenarioUpForce, scenarioUpParams)
		})
		if err != nil {
			return err
		}
		warnMissingHosts(cmd.Context(), os.Stderr, cfg.DomainSuffix)
		return nil
	},
}

var scenarioDownCmd = &cobra.Command{
	Use:   "down [scenario-name]",
	Short: "Deactivate a scenario",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if s, err := scenes.Get(name); err == nil && !s.Active {
			fmt.Fprintf(os.Stderr, "Scenario %s is not active.\n", name)
			return nil
		}
		// Tear down against the workload the scenario was brought up against.
		if err := rebindTo(scenes.ActiveApp(name)); err != nil {
			return err
		}
		return runScenarioOp(cmd, "deactivate", name, func(ctx context.Context, svc *scnsvc.Service) (string, error) {
			return svc.Deactivate(ctx, name)
		})
	},
}

var scenarioResetCmd = &cobra.Command{
	Use:   "reset [scenario-name]",
	Short: "Fast retry: tear the scenario down and re-activate it in one step",
	Long: `Resets a scenario for another attempt without a full lab teardown:
it deactivates the scenario (if active) and re-activates it, both as recorded,
cancellable runs. Because component installs are idempotent, a retry converges
in seconds rather than minutes.`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runScenarioReset(cmd, args[0])
	},
}

var scenarioStatusCmd = &cobra.Command{
	Use:   "status [scenario-name]",
	Short: "Show scenario status",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		statuses := scenes.Status()
		if len(statuses) == 0 {
			fmt.Println("No scenarios found.")
			return nil
		}

		// With a name argument, report just that scenario's status instead of
		// listing every active one — matching what `status <name>` implies.
		if len(args) == 1 {
			name := args[0]
			for _, s := range statuses {
				if s.Name == name {
					state := "inactive"
					if s.Active {
						state = "active"
					}
					fmt.Printf("%s (%s): %s\n", s.Name, s.Category, state)
					return nil
				}
			}
			return fmt.Errorf("scenario %q not found (see 'labctl scenario list')", name)
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
		// Grade against the workload the scenario was activated against — the
		// check scripts read WORKLOAD_* from the executor environment.
		if err := rebindTo(scenes.ActiveApp(args[0])); err != nil {
			return err
		}
		runner := newCheckRunner()
		ctx := context.Background()
		startedAt := time.Now()
		deadline := startedAt.Add(verifyTimeout)

		for {
			results, err := scenes.Verify(ctx, args[0], runner)
			if err != nil {
				return err
			}
			printCheckResults(results)
			if checks.AllPass(results) {
				fmt.Printf("All %d checks passed.\n", len(results))
				recordScenarioVerify(args[0], results, startedAt)
				return nil
			}
			// Separate genuine failures from checks that are merely pending the
			// user's next drill step, so "you haven't run backup yet" never reads
			// as "the scenario is broken".
			var failed, pending int
			for _, r := range results {
				switch {
				case r.Pass:
				case r.Pending:
					pending++
				default:
					failed++
				}
			}
			if !verifyWatch || time.Now().After(deadline) {
				printVerifyRemediation(results)
				recordScenarioVerify(args[0], results, startedAt)
				if failed == 0 && pending > 0 {
					// Nothing regressed — the scenario just isn't finished. Report
					// it as an incomplete drill, not a failure.
					return fmt.Errorf("%d step(s) still pending — complete the drill above, then re-verify", pending)
				}
				return fmt.Errorf("%d of %d checks failed", failed, len(results))
			}
			fmt.Printf("\n%d of %d checks not yet passing — retrying in %s (until %s)...\n\n",
				failed+pending, len(results), verifyInterval, deadline.Format("15:04:05"))
			time.Sleep(verifyInterval)
		}
	},
}

// recordScenarioVerify appends a scenario-verification record to the results
// history — the scenario's objectives plus each check's pass/fail — so `results`
// and the UI can show whether the user actually solved the scenario.
// Best-effort: a history write must never fail the verify command itself.
func recordScenarioVerify(name string, results []checks.Result, startedAt time.Time) {
	var objectives []string
	if s, err := scenes.Get(name); err == nil {
		objectives = s.Objectives
	}
	rec := resultspkg.NewScenarioRecord(name, "", objectives, checkOutcomes(results), startedAt, time.Now())
	_ = resultspkg.NewStore(filepath.Join(cfg.ProjectRoot, ".labctl", "history")).Append(rec)
}

// checkOutcomes flattens check results into the compact display shape the
// results store records.
func checkOutcomes(results []checks.Result) []resultspkg.CheckOutcome {
	out := make([]resultspkg.CheckOutcome, 0, len(results))
	for _, r := range results {
		detail := r.Error
		if detail == "" && !r.Pass {
			detail = fmt.Sprintf("got %s, want %s", orDash(r.Got), orDash(r.Want))
		}
		out = append(out, resultspkg.CheckOutcome{Name: r.Name, Pass: r.Pass, Detail: detail})
	}
	return out
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
	// A check script grades the bound workload, so it needs the workload's
	// identity for the same reason a component script does (ADR-0014).
	// Without these a check can only hardcode an app name, which is the thing
	// the workload binding exists to remove.
	r.Env = []string{
		"DOMAIN_SUFFIX=" + cfg.DomainSuffix,
		"MONITORING_NAMESPACE=" + cfg.MonitoringNamespace,
		"PROJECT_ROOT=" + cfg.ProjectRoot,
		// The same Prometheus the promql checks use, so a script check and a
		// promql check in one scenario cannot disagree about where to look.
		"PROMETHEUS_URL=" + promURL,
		"WORKLOAD_NAME=" + scenes.Workload.Name,
		"WORKLOAD_NAMESPACE=" + scenes.Workload.Namespace,
		"WORKLOAD_PORT=" + scenes.Workload.Port,
		"WORKLOAD_METRIC=" + scenes.Workload.Metric,
	}
	return r
}

func printCheckResults(results []checks.Result) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, r := range results {
		mark := "PASS"
		if !r.Pass {
			// A check declared pending is expected to be red until the user
			// performs the matching drill step — render it as PENDING, not a
			// hard FAIL, so it doesn't read as breakage.
			if r.Pending {
				mark = "PENDING"
			} else {
				mark = "FAIL"
			}
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

// printVerifyRemediation prints targeted next-step guidance for the checks that
// did not pass, instead of the old blanket "pods may still be starting" line
// that misdirected troubleshooting whenever the real fix was an action (take a
// backup, restore the marker). Each failing check with a declared Remediation
// gets its own line; the generic pods/--watch hint is printed only when a check
// failed with no remediation of its own — the case where waiting might actually
// help (a rollout still settling).
func printVerifyRemediation(results []checks.Result) {
	var steps []string
	genericFailure := false
	for _, r := range results {
		if r.Pass {
			continue
		}
		if r.Remediation != "" {
			steps = append(steps, fmt.Sprintf("  → %s: %s", r.Name, r.Remediation))
			continue
		}
		if !r.Pending {
			genericFailure = true
		}
	}
	if len(steps) > 0 {
		fmt.Fprintln(os.Stderr, "\nNext step(s):")
		for _, s := range steps {
			fmt.Fprintln(os.Stderr, s)
		}
	}
	if genericFailure {
		fmt.Fprintln(os.Stderr, "\nA check with no fix-it hint failed — a pod may still be starting.")
		fmt.Fprintln(os.Stderr, "Re-run with --watch to wait, or inspect with: kubectl get pods -A")
	}
}

// bindForScenario binds a read-only command to the app it describes: the one
// --app names, or else the app the scenario was activated for.
func bindForScenario(cmd *cobra.Command, name string) error {
	if f := cmd.Flags().Lookup("app"); f != nil && f.Changed {
		return nil
	}
	return rebindTo(scenes.ActiveApp(name))
}

var scenarioRenderCmd = &cobra.Command{
	Use:   "render <scenario-name> <file>",
	Short: "Print a scenario file with its template variables filled in",
	Long: `Prints a file from the scenario's directory as the engine would apply it:
{{.WorkloadName}}, {{.DomainSuffix}} and the scenario's parameters are filled in
for the app named by --app, or the app the scenario was activated for. Use it to
apply a manifest the scenario leaves to you:

  labctl scenario render node-drain-drill manifests/baseline.yaml --app go-api | kubectl apply -f -`,
	Args:         cobra.ExactArgs(2),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := scenes.Get(args[0])
		if err != nil {
			return err
		}
		if err := bindForScenario(cmd, s.Name); err != nil {
			return err
		}
		body, err := scenes.RenderFile(s, args[1])
		if err != nil {
			return err
		}
		_, err = fmt.Fprint(cmd.OutOrStdout(), body)
		return err
	},
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
		if err := bindForScenario(cmd, s.Name); err != nil {
			return err
		}

		// Resolve with the scenario's parameter defaults, exactly as the HTTP API
		// does for the UI. Without them the CLI printed a raw "{{.MinReplicas}}"
		// for the same snippet the UI rendered as a real number.
		defaults := scenes.ParamDefaults(s)
		resolve := func(in string) string { return scenes.ResolveTemplateWithParams(in, defaults) }

		fmt.Printf("Name:        %s\n", s.Name)
		fmt.Printf("Display:     %s\n", s.DisplayName)
		fmt.Printf("Category:    %s\n", s.Category)
		fmt.Printf("Description: %s\n", resolve(s.Description))

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
			for _, a := range scenes.ResolvedPrereqApps(s) {
				fmt.Printf("  - %s\n", a)
			}
		}
		// Show the requirement against the app actually bound, so a reader sees
		// whether THIS lab can run the scenario, not just what it asks for.
		if len(s.Prerequisites.Capabilities) > 0 {
			fmt.Printf("\nPrerequisites (workload capabilities), bound to %q:\n", scenes.Workload.Name)
			for _, name := range s.Prerequisites.Capabilities {
				mark := "missing"
				if c, err := workload.ParseCapability(name); err == nil && scenes.Contract.Has(c) {
					mark = "ok"
				}
				fmt.Printf("  - %-22s %s\n", name, mark)
			}
		}

		// Parameters are the knobs a learner is meant to turn — including the
		// SLO a scenario grades against. Left unlisted, a threshold reads as an
		// unexplained constant rather than a choice.
		if len(s.Parameters) > 0 {
			fmt.Printf("\nParameters (override with --set Name=value):\n")
			for _, p := range s.Parameters {
				fmt.Printf("  %-22s %s (default: %s)\n", p.Name, p.DisplayName, p.Default)
				if p.Description != "" {
					fmt.Printf("  %-22s   %s\n", "", p.Description)
				}
			}
		}

		if len(s.Objectives) > 0 {
			fmt.Printf("\nObjectives:\n")
			for _, o := range s.Objectives {
				fmt.Printf("  - %s\n", resolve(o))
			}
		}

		printComponent := func(c scenariopkg.Component, indent string) {
			renderComponent(os.Stdout, c, indent, resolve)
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
				fmt.Printf("  URL: %-25s %s\n", resolve(u.Label), resolve(u.URL))
			}
			for _, c := range s.Explore.Commands {
				fmt.Printf("  CMD: %s\n       %s\n", resolve(c.Label), resolve(c.Command))
			}
			for _, t := range s.Explore.Tips {
				fmt.Printf("  TIP: %s\n", resolve(t))
			}
		}

		renderReferences(os.Stdout, s.References, resolve)
		renderSnippets(os.Stdout, s.Snippets, s.Dir, resolve)

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
	scenarioUpCmd.Flags().StringToStringVar(&scenarioUpParams, "set", nil, "override a scenario parameter (repeatable): --set Name=value, matching the parameter name exactly (e.g. --set Threshold=15 --set MaxReplicas=4)")

	scenarioCmd.AddCommand(scenarioNewCmd)
	scenarioCmd.AddCommand(scenarioListCmd)
	scenarioCmd.AddCommand(scenarioUpCmd)
	scenarioCmd.AddCommand(scenarioDownCmd)
	scenarioCmd.AddCommand(scenarioResetCmd)
	scenarioCmd.AddCommand(scenarioStatusCmd)
	scenarioCmd.AddCommand(scenarioInfoCmd)
	scenarioCmd.AddCommand(scenarioRenderCmd)
	scenarioCmd.AddCommand(scenarioVerifyCmd)
	rootCmd.AddCommand(scenarioCmd)
}
