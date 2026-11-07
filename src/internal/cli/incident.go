// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/sagar2395/snowopslabs/internal/incident"
	incsvc "github.com/sagar2395/snowopslabs/internal/service/incident"
	"github.com/spf13/cobra"
)

var (
	injectRandom        bool
	injectSilent        bool
	injectForce         bool
	injectDeployPrereqs bool
	injectSeed          int64
	injectCategory      string
)

var incidentCmd = &cobra.Command{
	Use:   "incident",
	Short: "Inject and resolve realistic production faults",
	Long: `Game-day practice: inject a reversible fault from incidents/, diagnose
it with real tooling, fix it, and let the detection check confirm the fix.
'labctl incident resolve' is the escape hatch that always restores the lab.`,
}

func incidentListCmd() *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List available faults",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runIncidentList(cmd.OutOrStdout(), cmd.ErrOrStderr(), output)
		},
	}
	addOutputFlag(cmd, &output, outputTable, outputJSON)
	return cmd
}

// faultListItem is one fault in `incident list -o json`: the fault as the API
// serves it, and whether it is the active incident.
type faultListItem struct {
	*incident.Fault
	Active bool `json:"active"`
}

// runIncidentList prints the fault catalog as a table, or with -o json as an
// array. A silent incident is never marked active, since that would name it.
func runIncidentList(out, errW io.Writer, output string) error {
	if err := checkOutput(output, outputTable, outputJSON); err != nil {
		return err
	}
	defer func() {
		for key, err := range incEng.LoadErrors() {
			fmt.Fprintf(errW, "Warning: fault %s failed to load: %v\n", key, err)
		}
	}()
	faults := incEng.List()
	active, _ := incEng.Active()
	hidden := active != nil && active.Silent
	isActive := func(f *incident.Fault) bool { return active != nil && !hidden && active.Fault == f.Name }

	if output == outputJSON {
		items := make([]faultListItem, 0, len(faults))
		for _, f := range faults {
			items = append(items, faultListItem{Fault: f, Active: isActive(f)})
		}
		return writeJSON(out, items)
	}

	if len(faults) == 0 {
		fmt.Fprintln(out, "No faults found in incidents/.")
		return nil
	}
	w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "NAME\tCATEGORY\tSEVERITY\tTARGET\tSTATUS")
	for _, f := range faults {
		status := ""
		if isActive(f) {
			status = "ACTIVE"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s/%s\t%s\n",
			f.Name, f.Category, f.Severity, f.Target.Namespace, f.Target.Workload, status)
	}
	_ = w.Flush()
	if hidden {
		fmt.Fprintln(out, "\nAn incident is active (hidden - silent mode). Check it with: labctl incident status")
	}
	return nil
}

var incidentInjectCmd = &cobra.Command{
	Use:   "inject [fault-name]",
	Short: "Inject a fault (or --random for a surprise)",
	Long: `Breaks the lab on purpose. With --random the engine picks an eligible
fault; add --silent to hide which one (real game-day mode) and --seed N for
a reproducible pick across a team.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := ""
		if len(args) == 1 {
			name = args[0]
		}
		if (name == "") == !injectRandom {
			return errors.New("pass exactly one of a fault name or --random")
		}
		if name != "" {
			if _, err := incEng.Get(name); err != nil {
				return err
			}
		}
		if err := requireCluster(cmd.Context()); err != nil {
			return err
		}
		if injectRandom {
			f, err := incEng.PickRandom(injectSeed, injectCategory)
			if err != nil {
				return err
			}
			name = f.Name
		}

		f, err := incEng.Get(name)
		if err != nil {
			return err
		}

		if err := ensurePlatformPrereqs(cmd.Context(), os.Stdout, f.Prerequisites.Platform, injectDeployPrereqs); err != nil {
			return err
		}
		// The apps the fault needs, and its target when that is a repo app,
		// must be running first (or deployed with --deploy-prereqs).
		if err := ensureAppsDeployed(cmd.Context(), faultApps(f), injectDeployPrereqs); err != nil {
			return err
		}

		// Check preconditions, run inject.sh through the run engine, and on
		// success record the active incident.
		if active, aerr := incEng.Active(); aerr != nil {
			return aerr
		} else if active != nil && !injectForce {
			return incident.ActiveError(active)
		}
		if err := incEng.Preflight(f); err != nil {
			return err
		}
		target := incsvc.Target(incEng.ResolvedTarget(f))
		// In silent mode neither the run's name nor what inject.sh prints may
		// reach the terminal: both say which fault this is.
		out, label := cmd.OutOrStdout(), "inject "+name
		if injectSilent {
			out, label = io.Discard, "inject (hidden fault)"
		}
		if err := runIncidentOp(cmd, out, label, func(ctx context.Context, svc *incsvc.Service) (string, error) {
			if injectSilent {
				return svc.InjectSilently(ctx, name, target)
			}
			return svc.Inject(ctx, name, target)
		}); err != nil {
			return err
		}
		if err := incEng.MarkInjected(name, injectSilent); err != nil {
			return fmt.Errorf("recording active incident: %w", err)
		}

		fmt.Println()
		if injectSilent {
			fmt.Println("An incident has been injected. Good luck.")
		} else {
			fmt.Printf("Incident injected: %s (%s, severity %s)\n", f.Name, f.Category, f.Severity)
			fmt.Printf("  %s\n", f.Description)
		}
		fmt.Println("\nNext steps:")
		fmt.Println("  labctl incident status    # check whether you've fixed it")
		fmt.Println("  labctl incident hint      # progressive hints (recorded)")
		fmt.Println("  labctl incident resolve   # escape hatch - undo the fault")
		return nil
	},
}

var incidentStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Run the active incident's detection check",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := rebindToActiveIncident(); err != nil {
			return err
		}
		res, err := incEng.Status(context.Background(), newCheckRunner(), "")
		if errors.Is(err, incident.ErrNoActive) {
			fmt.Println("No incident is active. Inject one with: labctl incident inject --random")
			return nil
		}
		if err != nil {
			return err
		}

		name := res.Fault.Name
		if res.Active.Silent && !res.Resolved {
			name = "(hidden - silent mode)"
		}
		fmt.Printf("Incident:  %s\n", name)
		fmt.Printf("Injected:  %s (%s ago)\n",
			res.Active.InjectedAt.Format(time.RFC3339),
			time.Since(res.Active.InjectedAt).Round(time.Second))

		if res.Alert != nil {
			printIncidentAlertStatus(cmd.OutOrStdout(), res.Alert, res.Active.Silent && !res.Resolved)
		}

		if res.Resolved {
			fmt.Printf("\nRESOLVED - detection check %q passes. Nice work.\n", res.Check.Name)
			fmt.Printf("It was: %s - %s\n", res.Fault.Name, res.Fault.DisplayName)
			printSignupNudge(os.Stdout)
			tidyAfterFix(cmd, res.Fault.Name, incsvc.Target(incEng.ResolvedTarget(res.Fault)))
			return nil
		}
		if res.Active.Silent {
			fmt.Println("\nNOT RESOLVED - keep digging, or: labctl incident hint")
			return nil
		}
		detail := res.Check.Error
		if detail == "" {
			detail = fmt.Sprintf("got: %s, want: %s", orDash(res.Check.Got), orDash(res.Check.Want))
		}
		fmt.Printf("\nNOT RESOLVED - detection check %q fails (%s)\n", res.Check.Name, detail)
		fmt.Println("Keep digging, or: labctl incident hint")
		return nil
	},
}

func printIncidentAlertStatus(w io.Writer, alert *incident.AlertStatus, hidden bool) {
	if hidden && !alert.Fired {
		if alert.Error != "" {
			fmt.Fprintf(w, "Alert:     not available yet - could not query Alertmanager (%s)\n", alert.Error)
			return
		}
		fmt.Fprintln(w, "Alert:     not firing yet (rules evaluate within ~1-2 minutes of injection)")
		return
	}

	switch {
	case alert.Error != "":
		fmt.Fprintf(w, "Alert:     %s - could not query Alertmanager (%s)\n", alert.Expected, alert.Error)
	case alert.Fired:
		fmt.Fprintf(w, "Alert:     %s FIRING since %s - the page went out\n",
			alert.Expected, alert.Since.Format(time.RFC3339))
	default:
		fmt.Fprintf(w, "Alert:     %s not firing (rules evaluate within ~1-2 minutes of injection)\n", alert.Expected)
	}
}

// tidyAfterFix runs a fault's resolve.sh once a hand fix passes, removing the armed
// alert rule and labfault-* marks; resolve.sh leaves what is already fixed alone.
func tidyAfterFix(cmd *cobra.Command, name string, target incsvc.Target) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "\nTidying up: running %s's resolve.sh to remove its alert rule and bookkeeping.\n", name)
	err := runIncidentOp(cmd, out, "resolve "+name, func(ctx context.Context, svc *incsvc.Service) (string, error) {
		return svc.Resolve(ctx, name, target)
	})
	if err != nil {
		fmt.Fprintf(out, "The tidy-up did not finish (%v). Your fix still counts; run 'labctl incident resolve %s' to finish it.\n", err, name)
	}
}

var incidentResolveCmd = &cobra.Command{
	Use:   "resolve [fault-name]",
	Short: "Escape hatch: undo the active fault (or a named one)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := ""
		if len(args) == 1 {
			name = args[0]
		}
		// Bind to the app the fault was injected into.
		if err := rebindToActiveIncident(); err != nil {
			return err
		}
		// With no name, resolve the active incident. A named fault can be
		// resolved even when no incident is recorded as active.
		if name == "" {
			active, err := incEng.Active()
			if err != nil {
				return err
			}
			if active == nil {
				return fmt.Errorf("%w (pass a fault name to force-resolve anyway)", incident.ErrNoActive)
			}
			name = active.Fault
		}
		f, err := incEng.Get(name)
		if err != nil {
			return err
		}
		target := incsvc.Target(incEng.ResolvedTarget(f))
		if err := runIncidentOp(cmd, cmd.OutOrStdout(), "resolve "+name, func(ctx context.Context, svc *incsvc.Service) (string, error) {
			return svc.Resolve(ctx, name, target)
		}); err != nil {
			return err
		}
		incEng.RecordScriptResolved(name, "")
		fmt.Printf("\nIncident %s resolved via its resolve.sh. The lab should be healthy again.\n", f.Name)
		return nil
	},
}

var incidentHintCmd = &cobra.Command{
	Use:   "hint",
	Short: "Reveal the next hint for the active incident (recorded - costs score in challenges)",
	RunE: func(cmd *cobra.Command, args []string) error {
		h, err := incEng.NextHint()
		if errors.Is(err, incident.ErrNoMoreHints) {
			fmt.Println(err.Error())
			fmt.Println("Still stuck? labctl incident solution shows the full walkthrough.")
			return nil
		}
		if err != nil {
			return err
		}
		fmt.Printf("Hint %d of %d:\n\n%s\n", h.Index, h.Total, h.Text)
		chargeHints(1)
		return nil
	},
}

// chargeHints records n hints against the active challenge, if there is one,
// so they count towards its hint penalty. Errors are ignored.
func chargeHints(n int) {
	eng := challengeEngine()
	if active, err := eng.Active(); err != nil || active == nil {
		return
	}
	for range n {
		if err := eng.RecordHint(); err != nil {
			return
		}
	}
}

var solutionYes bool

var incidentSolutionCmd = &cobra.Command{
	Use:   "solution [fault-name]",
	Short: "Show the full walkthrough (spoiler!)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := ""
		if len(args) == 1 {
			name = args[0]
		}
		if !solutionYes {
			fmt.Print("This spoils the exercise. Type 'yes' to continue: ")
			line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
			if strings.TrimSpace(line) != "yes" {
				fmt.Println("Aborted. Try 'labctl incident hint' instead.")
				return nil
			}
		}
		text, err := incEng.Solution(name)
		if err != nil {
			return err
		}
		fmt.Println(text)
		// Reading the solution costs as much as reading every hint.
		chargeHints(incEng.HintCount(name))
		return nil
	},
}

var incidentHistoryCmd = &cobra.Command{
	Use:   "history",
	Short: "Show past incident runs with MTTR and hints used",
	RunE: func(cmd *cobra.Command, args []string) error {
		recs, err := incEng.History()
		if err != nil {
			return err
		}
		if len(recs) == 0 {
			fmt.Println("No incident history yet. Run one: labctl incident inject --random")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "WHEN\tFAULT\tSEVERITY\tTIME-TO-CHECK\tMTTR\tHINTS\tRESOLVED BY")
		for _, r := range recs {
			detect := "-"
			if !r.FirstCheckedAt.IsZero() {
				detect = (time.Duration(r.DetectSeconds) * time.Second).String()
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\t%s\n",
				r.InjectedAt.Format("2006-01-02 15:04"), r.Fault, r.Severity,
				detect, (time.Duration(r.ResolveSeconds) * time.Second).String(),
				r.HintsUsed, r.ResolvedBy)
		}
		_ = w.Flush()
		return nil
	},
}

var incidentInfoCmd = &cobra.Command{
	Use:   "info [fault-name]",
	Short: "Show a fault's details, upstream references, and applyable snippets",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		f, err := incEng.Get(args[0])
		if err != nil {
			return err
		}
		// The active fault is described for the app it was injected into.
		if a, err := incEng.Active(); err == nil && a != nil && a.Fault == f.Name {
			if err := rebindTo(a.App); err != nil {
				return err
			}
		}
		fmt.Printf("Name:        %s\n", f.Name)
		fmt.Printf("Display:     %s\n", f.DisplayName)
		fmt.Printf("Category:    %s\n", f.Category)
		fmt.Printf("Severity:    %s\n", f.Severity)
		// Print the target with templates expanded.
		rt := incEng.ResolvedTarget(f)
		fmt.Printf("Target:      %s/%s\n", rt.Namespace, rt.Workload)
		if f.Description != "" {
			fmt.Printf("Description: %s\n", f.Description)
		}
		renderReferences(os.Stdout, f.References, incEng.ResolveTemplate)
		renderSnippets(os.Stdout, f.Snippets, f.Dir, incEng.ResolveTemplate)
		return nil
	},
}

func init() {
	incidentSolutionCmd.Flags().BoolVar(&solutionYes, "yes", false, "skip the spoiler confirmation")
	incidentInjectCmd.Flags().BoolVar(&injectDeployPrereqs, "deploy-prereqs", false, "install the platform components the fault needs and build and deploy its apps, if they are not already running")
	incidentInjectCmd.Flags().BoolVar(&injectRandom, "random", false, "pick a random eligible fault")
	incidentInjectCmd.Flags().BoolVar(&injectSilent, "silent", false, "don't reveal which fault was injected")
	incidentInjectCmd.Flags().BoolVar(&injectForce, "force", false, "inject even if another incident is active")
	incidentInjectCmd.Flags().Int64Var(&injectSeed, "seed", 0, "seed for --random (reproducible team exercises)")
	incidentInjectCmd.Flags().StringVar(&injectCategory, "category", "", "restrict --random to a category (workload|network|resources|storage|config)")

	incidentCmd.AddCommand(incidentListCmd())
	incidentCmd.AddCommand(incidentInfoCmd)
	incidentCmd.AddCommand(incidentInjectCmd)
	incidentCmd.AddCommand(incidentStatusCmd)
	incidentCmd.AddCommand(incidentResolveCmd)
	incidentCmd.AddCommand(incidentHintCmd)
	incidentCmd.AddCommand(incidentSolutionCmd)
	incidentCmd.AddCommand(incidentHistoryCmd)
	rootCmd.AddCommand(incidentCmd)
}

// rebindToActiveIncident binds the lab to the app the active fault was
// injected into, so status, hints and resolve act on it.
func rebindToActiveIncident() error {
	// Ignore an unreadable state file here; the command reports it, and resolve
	// must still work.
	a, err := incEng.Active()
	if err != nil {
		slog.Debug("could not read the active incident to rebind", "err", err)
		return nil
	}
	if a == nil {
		return nil
	}
	return rebindTo(a.App)
}

// faultApps returns the apps a fault needs running: its prerequisite apps,
// and its target when that is a repo app, each once.
func faultApps(f *incident.Fault) []string {
	apps := append([]string{}, f.Prerequisites.Apps...)
	if target := incEng.ResolvedTarget(f).Namespace; appExists(target) && !slices.Contains(apps, target) {
		apps = append(apps, target)
	}
	return apps
}
