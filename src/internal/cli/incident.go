// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
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

var incidentListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available faults",
	RunE: func(cmd *cobra.Command, args []string) error {
		faults := incEng.List()
		if len(faults) == 0 {
			fmt.Println("No faults found in incidents/.")
			return nil
		}
		active, _ := incEng.Active()

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "NAME\tCATEGORY\tSEVERITY\tTARGET\tSTATUS")
		for _, f := range faults {
			status := ""
			if active != nil && active.Fault == f.Name {
				status = "ACTIVE"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s/%s\t%s\n",
				f.Name, f.Category, f.Severity, f.Target.Namespace, f.Target.Workload, status)
		}
		_ = w.Flush()

		for key, err := range incEng.LoadErrors() {
			fmt.Fprintf(os.Stderr, "Warning: fault %s failed to load: %v\n", key, err)
		}
		return nil
	},
}

var incidentInjectCmd = &cobra.Command{
	Use:   "inject [fault-name]",
	Short: "Inject a fault (or --random for a surprise)",
	Long: `Breaks the lab on purpose. With --random the engine picks an eligible
fault; add --silent to hide which one (real game-day mode) and --seed N for
a reproducible pick across a team.`,
	Args:         cobra.MaximumNArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		name := ""
		if len(args) == 1 {
			name = args[0]
		}
		if (name == "") == !injectRandom {
			return fmt.Errorf("pass exactly one of a fault name or --random")
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

		// A fault breaks a running workload; if its target is one of our apps and
		// it isn't deployed, inject would fail cryptically. Check first (or deploy
		// it with --deploy-prereqs).
		if appExists(f.Target.Namespace) {
			if err := ensureAppsDeployed(cmd.Context(), []string{f.Target.Namespace}, injectDeployPrereqs); err != nil {
				return err
			}
		}

		// Guard with the same checks the legacy path applied, then run inject.sh
		// through the durable engine and, on success, record the active incident.
		if active, aerr := incEng.Active(); aerr != nil {
			return aerr
		} else if active != nil && !injectForce {
			return fmt.Errorf("%w: %s (resolve it first, or use --force)", incident.ErrIncidentActive, active.Fault)
		}
		if err := incEng.Preflight(f); err != nil {
			return err
		}
		target := incsvc.Target{Namespace: f.Target.Namespace, Workload: f.Target.Workload}
		if err := runIncidentOp(cmd, "inject", name, target, func(ctx context.Context, svc *incsvc.Service) (string, error) {
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
		fmt.Println("  labctl incident resolve   # escape hatch — undo the fault")
		return nil
	},
}

var incidentStatusCmd = &cobra.Command{
	Use:          "status",
	Short:        "Run the active incident's detection check",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
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
			name = "(hidden — silent mode)"
		}
		fmt.Printf("Incident:  %s\n", name)
		fmt.Printf("Injected:  %s (%s ago)\n",
			res.Active.InjectedAt.Format(time.RFC3339),
			time.Since(res.Active.InjectedAt).Round(time.Second))

		if res.Alert != nil {
			switch {
			case res.Alert.Error != "":
				fmt.Printf("Alert:     %s — could not query Alertmanager (%s)\n", res.Alert.Expected, res.Alert.Error)
			case res.Alert.Fired:
				fmt.Printf("Alert:     %s FIRING since %s — the page went out\n",
					res.Alert.Expected, res.Alert.Since.Format(time.RFC3339))
			default:
				fmt.Printf("Alert:     %s not firing (rules evaluate within ~1-2 minutes of injection)\n", res.Alert.Expected)
			}
		}

		if res.Resolved {
			fmt.Printf("\nRESOLVED — detection check %q passes. Nice work.\n", res.Check.Name)
			fmt.Printf("It was: %s — %s\n", res.Fault.Name, res.Fault.DisplayName)
			return nil
		}
		detail := res.Check.Error
		if detail == "" {
			detail = fmt.Sprintf("got: %s, want: %s", orDash(res.Check.Got), orDash(res.Check.Want))
		}
		fmt.Printf("\nNOT RESOLVED — detection check %q fails (%s)\n", res.Check.Name, detail)
		fmt.Println("Keep digging, or: labctl incident hint")
		return nil
	},
}

var incidentResolveCmd = &cobra.Command{
	Use:          "resolve [fault-name]",
	Short:        "Escape hatch: undo the active fault (or a named one)",
	Args:         cobra.MaximumNArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		name := ""
		if len(args) == 1 {
			name = args[0]
		}
		// With no name, resolve the active incident (an explicit name works even
		// if the active state was lost — the escape hatch).
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
		target := incsvc.Target{Namespace: f.Target.Namespace, Workload: f.Target.Workload}
		if err := runIncidentOp(cmd, "resolve", name, target, func(ctx context.Context, svc *incsvc.Service) (string, error) {
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
	Use:          "hint",
	Short:        "Reveal the next hint for the active incident (recorded — costs score in challenges)",
	SilenceUsage: true,
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
		return nil
	},
}

var solutionYes bool

var incidentSolutionCmd = &cobra.Command{
	Use:          "solution [fault-name]",
	Short:        "Show the full walkthrough (spoiler!)",
	Args:         cobra.MaximumNArgs(1),
	SilenceUsage: true,
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
		fmt.Printf("Name:        %s\n", f.Name)
		fmt.Printf("Display:     %s\n", f.DisplayName)
		fmt.Printf("Category:    %s\n", f.Category)
		fmt.Printf("Severity:    %s\n", f.Severity)
		fmt.Printf("Target:      %s/%s\n", f.Target.Namespace, f.Target.Workload)
		if f.Description != "" {
			fmt.Printf("Description: %s\n", f.Description)
		}
		renderReferences(os.Stdout, f.References)
		renderSnippets(os.Stdout, f.Snippets, f.Dir, incEng.ResolveTemplate)
		return nil
	},
}

func init() {
	incidentSolutionCmd.Flags().BoolVar(&solutionYes, "yes", false, "skip the spoiler confirmation")
	incidentInjectCmd.Flags().BoolVar(&injectDeployPrereqs, "deploy-prereqs", false, "build and deploy the target app if it is not already running")
	incidentInjectCmd.Flags().BoolVar(&injectRandom, "random", false, "pick a random eligible fault")
	incidentInjectCmd.Flags().BoolVar(&injectSilent, "silent", false, "don't reveal which fault was injected")
	incidentInjectCmd.Flags().BoolVar(&injectForce, "force", false, "inject even if another incident is active")
	incidentInjectCmd.Flags().Int64Var(&injectSeed, "seed", 0, "seed for --random (reproducible team exercises)")
	incidentInjectCmd.Flags().StringVar(&injectCategory, "category", "", "restrict --random to a category (workload|network|resources|storage|config)")

	incidentCmd.AddCommand(incidentListCmd)
	incidentCmd.AddCommand(incidentInfoCmd)
	incidentCmd.AddCommand(incidentInjectCmd)
	incidentCmd.AddCommand(incidentStatusCmd)
	incidentCmd.AddCommand(incidentResolveCmd)
	incidentCmd.AddCommand(incidentHintCmd)
	incidentCmd.AddCommand(incidentSolutionCmd)
	incidentCmd.AddCommand(incidentHistoryCmd)
	rootCmd.AddCommand(incidentCmd)
}
