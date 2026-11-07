// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/sagar2395/snowopslabs/internal/checkfixture"
	"github.com/sagar2395/snowopslabs/internal/display"
	"github.com/sagar2395/snowopslabs/internal/labcheck"
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

var cliGetwd = os.Getwd

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
		displayDir := display.Text(dir, cfg.ProjectRoot, cfg.StateDir, cliCurrentDir())
		fmt.Printf("Created %s\n", displayDir)
		fmt.Printf("\nNext:\n  labctl scenario verify %s\n  $EDITOR %s\n", args[0], display.Text(dir+"/scenario.yaml", cfg.ProjectRoot, cfg.StateDir, cliCurrentDir()))
		return nil
	},
}

var scenarioCmd = &cobra.Command{
	Use:   "scenario",
	Short: "Manage lab scenarios",
}

func scenarioListCmd() *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List available scenarios",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runScenarioList(cmd.OutOrStdout(), cmd.ErrOrStderr(), output)
		},
	}
	addOutputFlag(cmd, &output, outputTable, outputJSON)
	return cmd
}

// runScenarioList prints the scenario catalog as a table, or with -o json as
// the array GET /api/v2/scenarios serves. Load warnings go to errW, so the
// JSON on out stays parseable.
func runScenarioList(out, errW io.Writer, output string) error {
	if err := checkOutput(output, outputTable, outputJSON); err != nil {
		return err
	}
	defer func() {
		if errs := scenes.LoadErrors(); len(errs) > 0 {
			fmt.Fprintf(errW, "\nWarning: %d scenario(s) failed to load (run with -v for details):\n", len(errs))
			for key, err := range errs {
				fmt.Fprintf(errW, "  %s: %v\n", key, err)
			}
		}
	}()
	if output == outputJSON {
		items := scenes.Status()
		if items == nil {
			items = []scenariopkg.ScenarioStatus{}
		}
		return writeJSON(out, items)
	}

	scenarios := scenes.List()
	if len(scenarios) == 0 {
		fmt.Fprintln(out, "No scenarios found in scenarios/ directory.")
		return nil
	}
	w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
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

	fmt.Fprintln(out, "\nSee what a scenario installs, its objectives and checks:")
	fmt.Fprintln(out, "  labctl scenario info <name>")
	return nil
}

var scenarioUpCmd = &cobra.Command{
	Use:   "up [scenario-name]",
	Short: "Activate a scenario",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		// Check the prerequisite apps are running, or deploy them with
		// --deploy-prereqs.
		s, err := scenes.Get(name)
		if err != nil {
			return err
		}
		if err := requireCluster(cmd.Context()); err != nil {
			return err
		}
		// Check the lab has room before installing any prerequisite for it.
		// Warnings are left to the activation itself, which checks again.
		if scenes.Admit != nil && !s.Active {
			if d, err := scenes.Demand(s); err == nil {
				if err := scenes.Admit(cmd.Context(), io.Discard, d); err != nil {
					return fmt.Errorf("scenario %s cannot start: %w", name, err)
				}
			}
		}
		if err := ensurePlatformPrereqs(cmd.Context(), os.Stdout, s.Prerequisites.Platform, scenarioDeployPrereqs); err != nil {
			return err
		}
		if err := ensureAppsDeployed(cmd.Context(), scenes.ResolvedPrereqApps(s), scenarioDeployPrereqs); err != nil {
			return err
		}
		// An already-active scenario is left alone unless --force is given.
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
		// Tear down using the app the scenario was activated against.
		if err := rebindTo(scenes.ActiveApp(name)); err != nil {
			return err
		}
		return runScenarioOp(cmd, "deactivate", name, func(ctx context.Context, svc *scnsvc.Service) (string, error) {
			if scenarioDownForce {
				return svc.ForceDeactivate(ctx, name)
			}
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
	Args: cobra.ExactArgs(1),
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

		// With a name, report only that scenario.
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
	verifyRecord       string
)

var scenarioVerifyCmd = &cobra.Command{
	Use:   "verify [scenario-name]",
	Short: "Run a scenario's checks and report pass/fail",
	Long: `Runs the machine-verifiable checks declared in the scenario's
checks block (scenario format v2) and reports each result. Exits non-zero
if any check fails, so it is safe to use in CI and scripts.

With --watch, checks are re-run every --interval until they all pass or
--timeout elapses - useful right after 'scenario up' while pods settle.`,
	Args: cobra.ExactArgs(1), // a failing check is a result, not a usage error
	RunE: func(cmd *cobra.Command, args []string) error {
		// Check the app the scenario was activated against; check scripts read
		// WORKLOAD_* from scriptExec.
		if err := rebindTo(scenes.ActiveApp(args[0])); err != nil {
			return err
		}
		ctx := cmd.Context()
		startedAt := time.Now()
		deadline := startedAt.Add(verifyTimeout)

		for {
			runner := newCheckRunner()
			fixture := checkfixture.New()
			if verifyRecord != "" {
				checkfixture.Record(runner, fixture)
			}
			results, err := scenes.Verify(ctx, args[0], runner)
			if err != nil {
				return err
			}
			printCheckResults(results)
			if checks.AllPass(results) {
				fmt.Printf("All %d checks passed.\n", len(results))
				printSignupNudge(os.Stdout)
				recordScenarioVerify(args[0], results, startedAt)
				return saveFixture(args[0], fixture, results)
			}
			// Count failures separately from pending checks, which are waiting
			// for the learner's next step.
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
				if err := saveFixture(args[0], fixture, results); err != nil {
					return err
				}
				if failed == 0 && pending > 0 {
					// Nothing failed; the learner has steps left to do.
					return fmt.Errorf("%d step(s) still pending - complete the drill above, then re-verify", pending)
				}
				return fmt.Errorf("%d of %d checks failed", failed, len(results))
			}
			fmt.Printf("\n%d of %d checks not yet passing - retrying in %s (until %s)...\n\n",
				failed+pending, len(results), verifyInterval, deadline.Format("15:04:05"))
			select {
			case <-ctx.Done():
				return fmt.Errorf("stopped before the checks passed: %w", ctx.Err())
			case <-time.After(verifyInterval):
			}
		}
	},
}

// saveFixture writes what the lab answered this verify's declared checks to
// --record, for the scenario's replay tests. It does nothing without --record.
func saveFixture(name string, f *checkfixture.Fixture, results []checks.Result) error {
	if verifyRecord == "" {
		return nil
	}
	vars, err := scenes.CheckVars(name)
	if err != nil {
		return err
	}
	// Paths on this machine name no lab state, and a fixture is committed.
	delete(vars, "ProjectRoot")
	delete(vars, "StateDir")
	f.Vars = vars
	f.SetResults(results)
	if err := f.Save(verifyRecord); err != nil {
		return fmt.Errorf("writing the check fixture: %w", err)
	}
	fmt.Printf("Recorded the lab's answers to %s.\n", verifyRecord)
	return nil
}

// recordScenarioVerify appends a scenario verification record, with the
// objectives and each check's result, to the results history. Errors are
// ignored so they never fail the verify.
func recordScenarioVerify(name string, results []checks.Result, startedAt time.Time) {
	var objectives []string
	if s, err := scenes.Get(name); err == nil {
		objectives = s.Objectives
	}
	rec := resultspkg.NewScenarioRecord(name, "", objectives, checkOutcomes(results), startedAt, time.Now())
	_ = resultspkg.NewStore(filepath.Join(cfg.StateDir, "history")).Append(rec)
}

// checkOutcomes converts check results to the form the results store records.
func checkOutcomes(results []checks.Result) []resultspkg.CheckOutcome {
	out := make([]resultspkg.CheckOutcome, 0, len(results))
	for _, r := range results {
		r.Error = display.Text(r.Error, cfg.ProjectRoot, cfg.StateDir, cliCurrentDir())
		detail := r.Error
		if detail == "" && !r.Pass {
			detail = fmt.Sprintf("got %s, want %s", orDash(r.Got), orDash(r.Want))
		}
		out = append(out, resultspkg.CheckOutcome{Name: r.Name, Pass: r.Pass, Detail: detail})
	}
	return out
}

// newCheckRunner is the lab's check runner (see labcheck) with the verify
// per-check timeout.
func newCheckRunner() *checks.Runner {
	r := labcheck.NewRunner(cfg, scenes.Workload, "")
	r.DefaultTimeout = verifyCheckTimeout
	return r
}

// printCheckResults prints one table row per check. A script check's error
// often spans several lines (the diagnosis and the commands to run); inside the
// table those lines would break its columns, so a row shows the first line and
// the rest follows the table under the check's name.
func printCheckResults(results []checks.Result) {
	printCheckResultsTo(os.Stdout, results)
}

func printCheckResultsTo(out io.Writer, results []checks.Result) {
	type more struct {
		name  string
		lines string
	}
	var details []more
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, r := range results {
		mark := "PASS"
		if !r.Pass {
			// A pending check fails until the learner does the matching step,
			// so show it as PENDING rather than FAIL.
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
			first, rest, _ := strings.Cut(strings.TrimRight(r.Error, "\n"), "\n")
			detail = "error: " + first
			if rest != "" {
				details = append(details, more{r.Name, rest})
			}
		}
		fmt.Fprintf(w, "%s\t%s\t(%s)\t%s\t%dms\n", mark, r.Name, r.Type, detail, r.DurationMS)
	}
	_ = w.Flush()
	for _, d := range details {
		fmt.Fprintf(out, "\n%s:\n%s\n", d.name, d.lines)
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// printVerifyRemediation prints the next step for each failed check that
// declares a remediation. The general "pods may still be starting" hint is
// printed only if a check failed without one.
func printVerifyRemediation(results []checks.Result) {
	var steps []string
	genericFailure := false
	for _, r := range results {
		if r.Pass {
			continue
		}
		if r.Remediation != "" {
			steps = append(steps, fmt.Sprintf("  → %s: %s", r.Name, display.Text(r.Remediation, cfg.ProjectRoot, cfg.StateDir, cliCurrentDir())))
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
		fmt.Fprintln(os.Stderr, "\nA check with no fix-it hint failed - a pod may still be starting.")
		fmt.Fprintln(os.Stderr, "Re-run with --watch to wait, or inspect with: kubectl get pods -A")
	}
}

func cliCurrentDir() string {
	dir, err := cliGetwd()
	if err != nil {
		return ""
	}
	return dir
}

// bindForScenario binds a read-only command to the app it describes: the one
// --app names, or else the app the scenario was activated against.
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
	Args: cobra.ExactArgs(2),
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

		// Expand with the scenario's parameter defaults, as the API does.
		defaults := scenes.ParamDefaults(s)
		resolve := func(in string) string {
			return display.Text(scenes.ResolveTemplateWithParams(in, defaults), cfg.ProjectRoot, cfg.StateDir, cliCurrentDir())
		}

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
		// Show whether the bound app meets each requirement.
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

		// List the parameters, which the learner can change with --set.
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
			fmt.Printf("\nChecks (%d) - run 'labctl scenario verify %s':\n", len(s.Checks), s.Name)
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

var scenarioDownForce bool

func init() {
	scenarioDownCmd.Flags().BoolVar(&scenarioDownForce, "force", false, "mark the scenario inactive even when a component will not uninstall; remove what is left by hand")
	scenarioVerifyCmd.Flags().BoolVar(&verifyWatch, "watch", false, "re-run checks until they all pass or --timeout elapses")
	scenarioVerifyCmd.Flags().DurationVar(&verifyInterval, "interval", 10*time.Second, "delay between re-runs in --watch mode")
	scenarioVerifyCmd.Flags().StringVar(&verifyRecord, "record", "", "write what the lab answered the declared checks to this file, for the scenario's replay tests")
	scenarioVerifyCmd.Flags().DurationVar(&verifyTimeout, "timeout", 5*time.Minute, "overall deadline in --watch mode")
	scenarioVerifyCmd.Flags().DurationVar(&verifyCheckTimeout, "check-timeout", 30*time.Second, "per-check timeout")

	scenarioNewCmd.Flags().BoolVar(&scenarioNewForce, "force", false, "overwrite the scenario if it already exists")
	scenarioUpCmd.Flags().BoolVar(&scenarioUpForce, "force", false, "reinstall even if the scenario is already active")
	scenarioUpCmd.Flags().BoolVar(&scenarioDeployPrereqs, "deploy-prereqs", false, "install any prerequisite platform components and apps that are not yet running")
	scenarioUpCmd.Flags().StringToStringVar(&scenarioUpParams, "set", nil, "override a scenario parameter (repeatable): --set Name=value, matching the parameter name exactly (e.g. --set Threshold=15 --set MaxReplicas=4)")

	scenarioCmd.AddCommand(scenarioNewCmd)
	scenarioCmd.AddCommand(scenarioListCmd())
	scenarioCmd.AddCommand(scenarioUpCmd)
	scenarioCmd.AddCommand(scenarioDownCmd)
	scenarioCmd.AddCommand(scenarioResetCmd)
	scenarioCmd.AddCommand(scenarioStatusCmd)
	scenarioCmd.AddCommand(scenarioInfoCmd)
	scenarioCmd.AddCommand(scenarioRenderCmd)
	scenarioCmd.AddCommand(scenarioVerifyCmd)
	rootCmd.AddCommand(scenarioCmd)
}
