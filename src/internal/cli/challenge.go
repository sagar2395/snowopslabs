// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/snowops-oss/snowopslabs/internal/challenge"
	chalsvc "github.com/snowops-oss/snowopslabs/internal/service/challenge"
	"github.com/snowops-oss/snowopslabs/pkg/checks"
	"github.com/spf13/cobra"
)

func challengeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "challenge",
		Short: "Timed, auto-graded skills assessments",
		Long: `Work through timed challenges that assess your ability to diagnose
and fix real production problems. Your score reflects speed, correctness,
and how many hints you needed.`,
	}
	cmd.AddCommand(challengeListCmd())
	cmd.AddCommand(challengeInfoCmd())
	cmd.AddCommand(challengeStartCmd())
	cmd.AddCommand(challengeStatusCmd())
	cmd.AddCommand(challengeHintCmd())
	cmd.AddCommand(challengeSubmitCmd())
	cmd.AddCommand(challengeAbortCmd())
	cmd.AddCommand(challengeHistoryCmd())
	return cmd
}

var errNoActiveChallenge = errors.New("no active challenge - start one with `labctl challenge start <name>`")

func challengeEngine() *challenge.Engine {
	root := cfg.ProjectRoot
	return challenge.New(
		filepath.Join(root, "challenges"),
		filepath.Join(cfg.StateDir, "challenges"),
		filepath.Join(cfg.StateDir, "history"),
	)
}

// challengeGrader sets up, grades and cleans up challenges on the lab this
// process is bound to.
func challengeGrader() *chalsvc.Grader {
	return &chalsvc.Grader{
		Engine:    challengeEngine(),
		Scenarios: scenes,
		Incidents: incEng,
		Config:    cfg,
		Exec:      scriptExec,
	}
}

func challengeListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List available challenges",
		RunE: func(cmd *cobra.Command, _ []string) error {
			eng := challengeEngine()
			cs, err := eng.Challenges()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(cs) == 0 {
				fmt.Fprintln(out, "No challenges found.")
				return nil
			}
			fmt.Fprintf(out, "%-35s %-12s %-8s  %s\n", "CHALLENGE", "CATEGORY", "PAR", "DESCRIPTION")
			fmt.Fprintf(out, "%s\n", strings.Repeat("-", 76))
			for _, c := range cs {
				par := c.ParTime
				if par == "" {
					par = "-"
				}
				fmt.Fprintf(out, "%-35s %-12s %-8s  %s\n",
					c.Name, c.Category, par, truncate(c.DisplayName, 50))
			}
			return nil
		},
	}
}

func challengeInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info <name>",
		Short: "Show challenge details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng := challengeEngine()
			c, err := eng.Load(args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Name:        %s\n", c.Name)
			fmt.Fprintf(out, "Display:     %s\n", c.DisplayName)
			fmt.Fprintf(out, "Category:    %s\n", c.Category)
			fmt.Fprintf(out, "Par time:    %s\n", c.ParTime)
			fmt.Fprintf(out, "Hint cost:   %d%% per hint\n", hintPenaltyValue(c))
			fmt.Fprintf(out, "Setup:       %s\n", setupSummary(c))
			fmt.Fprintf(out, "\n%s\n", c.Description)
			return nil
		},
	}
}

func challengeStartCmd() *cobra.Command {
	var force bool
	var deployPrereqs bool
	cmd := &cobra.Command{
		Use:   "start <name>",
		Short: "Start a challenge (injects/activates + starts timer)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			eng := challengeEngine()
			c, err := eng.Load(name)
			if err != nil {
				return err
			}

			if err := requireCluster(cmd.Context()); err != nil {
				return err
			}
			// The setup breaks a running app, so check it and the platform it
			// needs are there first (or install them with --deploy-prereqs).
			if err := ensurePlatformPrereqs(cmd.Context(), cmd.OutOrStdout(), challengePlatformPrereqs(c), deployPrereqs); err != nil {
				return err
			}
			if err := ensureAppsDeployed(cmd.Context(), challengeRequiredApps(c), deployPrereqs); err != nil {
				return err
			}

			grader := challengeGrader()
			if err := grader.Setup(c); err != nil {
				// A failed setup may have changed the cluster, and no run is
				// recorded yet for `challenge abort` to clean up, so undo it
				// here.
				if cleanupErr := grader.Cleanup(c); cleanupErr != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "Warning: could not undo the partial setup: %v\n", cleanupErr)
				}
				return fmt.Errorf("setup failed: %w", err)
			}

			run, err := eng.Start(name, force)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Challenge started: %s\n", c.DisplayName)
			fmt.Fprintf(out, "Timer:    started at %s\n", run.StartedAt.Format(time.RFC3339))
			fmt.Fprintf(out, "Par time: %s\n", c.ParTime)
			fmt.Fprintf(out, "\n%s\n\n", c.Description)
			fmt.Fprintf(out, "When you think you've fixed it, run:\n  labctl challenge submit\n")
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Override an already-active challenge")
	cmd.Flags().BoolVar(&deployPrereqs, "deploy-prereqs", false, "install the platform components and app this challenge needs, if they are not already running")
	return cmd
}

// challengeRequiredApps returns the repo apps a challenge's setup depends on,
// so `start` can check they are deployed. Incident targets that are not repo
// apps, such as namespaces the fault script creates, are left out.
// challengePlatformPrereqs is the platform the challenge's setup scenario or
// fault needs.
func challengePlatformPrereqs(c *challenge.Challenge) []string {
	var out []string
	for _, ref := range c.Setup.All() {
		switch c.Setup.Type {
		case "scenario":
			if s, err := scenes.Get(ref); err == nil {
				out = append(out, s.Prerequisites.Platform...)
			}
		case "incident":
			if f, err := incEng.Get(ref); err == nil {
				out = append(out, f.Prerequisites.Platform...)
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func challengeRequiredApps(c *challenge.Challenge) []string {
	var out []string
	for _, ref := range c.Setup.All() {
		switch c.Setup.Type {
		case "scenario":
			if s, err := scenes.Get(ref); err == nil {
				out = append(out, scenes.ResolvedPrereqApps(s)...)
			}
		case "incident":
			if f, err := incEng.Get(ref); err == nil {
				if ns := incEng.ResolvedTarget(f).Namespace; appExists(ns) {
					out = append(out, ns)
				}
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// setupSummary says what a challenge sets up. A silent challenge gives only
// the count: naming its faults would hand over the answer.
func setupSummary(c *challenge.Challenge) string {
	refs := c.Setup.All()
	if c.Silent {
		return fmt.Sprintf("%d %s, not named: finding them is the exercise", len(refs), plural(len(refs), c.Setup.Type))
	}
	return c.Setup.Type + " " + strings.Join(refs, ", ")
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

func challengeStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the active challenge and elapsed time",
		RunE: func(cmd *cobra.Command, _ []string) error {
			eng := challengeEngine()
			active, err := eng.Active()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if active == nil {
				fmt.Fprintln(out, "No challenge is currently active.")
				return nil
			}
			c, _ := eng.Load(active.ChallengeName)
			elapsed := active.Elapsed()
			fmt.Fprintf(out, "Challenge: %s\n", active.ChallengeName)
			if c != nil {
				fmt.Fprintf(out, "Name:      %s\n", c.DisplayName)
			}
			fmt.Fprintf(out, "Elapsed:   %s\n", formatDuration(elapsed))
			if c != nil && c.ParDuration() > 0 {
				par := c.ParDuration()
				if elapsed > par {
					fmt.Fprintf(out, "Par:       %s (over by %s)\n", c.ParTime, formatDuration(elapsed-par))
				} else {
					fmt.Fprintf(out, "Par:       %s (under by %s)\n", c.ParTime, formatDuration(par-elapsed))
				}
			}
			fmt.Fprintf(out, "Hints used: %d\n", active.HintsUsed)
			return nil
		},
	}
}

func challengeHintCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "hint",
		Short: "Get the next hint (costs score)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			eng := challengeEngine()
			active, err := eng.Active()
			if err != nil {
				return err
			}
			if active == nil {
				return errNoActiveChallenge
			}
			c, err := eng.Load(active.ChallengeName)
			if err != nil {
				return err
			}
			// An incident challenge uses its faults' own hints.
			hint, err := challengeGrader().NextHint()
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "[Hint %d/%d - -%d%% score penalty]\n\n%s\n",
				hint.Index, hint.Total, hintPenaltyValue(c), hint.Text)
			return nil
		},
	}
}

func challengeSubmitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "submit",
		Short: "Run the grading checks and record your score",
		RunE: func(cmd *cobra.Command, _ []string) error {
			sub, err := challengeGrader().Submit(cmd.Context(), "")
			if errors.Is(err, challenge.ErrNoActive) {
				return errNoActiveChallenge
			}
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			printGradingResults(out, sub.Challenge, sub.Record, sub.Checks)

			// A failed submit does not end the run: the learner can fix the
			// failing checks and submit again, or abort.
			if !sub.Passed {
				fmt.Fprintln(out, "\nStill running. Fix what is listed above and submit again,")
				fmt.Fprintln(out, "or give up and clean the lab with:  labctl challenge abort")
				return nil
			}
			if sub.CleanupErr != nil {
				fmt.Fprintf(out, "Warning: cleanup failed: %v\n", sub.CleanupErr)
			}
			return nil
		},
	}
}

func challengeAbortCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "abort",
		Short: "Abort the active challenge (undoes setup, no score recorded)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			eng := challengeEngine()
			active, err := eng.Active()
			if err != nil {
				return err
			}
			if active == nil {
				fmt.Fprintln(cmd.OutOrStdout(), "No active challenge.")
				return nil
			}
			c, err := eng.Load(active.ChallengeName)
			if err != nil {
				return err
			}

			if err := challengeGrader().Cleanup(c); err != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Warning: cleanup failed: %v\n", err)
			}

			if _, err := eng.Complete(0, 0, "aborted", ""); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Challenge aborted: %s\n", active.ChallengeName)
			return nil
		},
	}
}

func challengeHistoryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "history",
		Short: "Show past challenge results",
		RunE: func(cmd *cobra.Command, _ []string) error {
			eng := challengeEngine()
			history, err := eng.History()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(history) == 0 {
				fmt.Fprintln(out, "No challenge history yet.")
				return nil
			}
			fmt.Fprintf(out, "%-35s %-10s %-8s %-8s %-6s  %s\n",
				"CHALLENGE", "DATE", "ELAPSED", "SCORE", "HINTS", "OUTCOME")
			fmt.Fprintf(out, "%s\n", strings.Repeat("-", 80))
			for _, r := range history {
				fmt.Fprintf(out, "%-35s %-10s %-8s %-8d %-6d  %s\n",
					r.ChallengeName,
					r.StartedAt.Format("2006-01-02"),
					formatDuration(r.Elapsed),
					r.Score,
					r.HintsUsed,
					r.Outcome)
			}
			return nil
		},
	}
}

func printGradingResults(out io.Writer, c *challenge.Challenge, rec *challenge.RunRecord, results []checks.Result) {
	fmt.Fprintf(out, "\n=== Challenge Result: %s ===\n\n", c.DisplayName)
	for _, r := range results {
		mark := "✗"
		if r.Pass {
			mark = "✓"
		}
		fmt.Fprintf(out, "  %s %s\n", mark, r.Name)
		if !r.Pass && r.Error != "" {
			fmt.Fprintf(out, "      %s\n", r.Error)
		}
	}
	fmt.Fprintf(out, "\nChecks: %d/%d passed\n", rec.ChecksPassed, rec.ChecksTotal)
	fmt.Fprintf(out, "Time:   %s (par: %s)\n", formatDuration(rec.Elapsed), c.ParTime)
	fmt.Fprintf(out, "Hints:  %d used\n", rec.HintsUsed)
	fmt.Fprintf(out, "Score:  %d/100\n", rec.Score)
	fmt.Fprintf(out, "Outcome: %s\n", rec.Outcome)
	if rec.Outcome == "passed" {
		printSignupNudge(out)
	}
}

func hintPenaltyValue(c *challenge.Challenge) int {
	if c.HintPenalty == 0 {
		return 5
	}
	return c.HintPenalty
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	if s == 0 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%dm%ds", m, s)
}
