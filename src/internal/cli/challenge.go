// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/sagar2395/snowopslabs/internal/challenge"
	"github.com/sagar2395/snowopslabs/internal/executor"
	"github.com/sagar2395/snowopslabs/pkg/checks"
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

func challengeEngine() *challenge.Engine {
	root := cfg.ProjectRoot
	return challenge.New(
		filepath.Join(root, "challenges"),
		filepath.Join(root, ".labctl", "challenges"),
		filepath.Join(root, ".labctl", "history"),
	)
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
			fmt.Fprintf(out, "Setup:       %s %s\n", c.Setup.Type, c.Setup.Ref)
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

			// The challenge's setup breaks a running app; make sure that app is
			// actually deployed first (or --deploy-prereqs deploys it), so start
			// fails fast with guidance instead of deep inside the setup action.
			if err := ensureAppsDeployed(cmd.Context(), challengeRequiredApps(c), deployPrereqs); err != nil {
				return err
			}

			// Run the setup action.
			if err := runChallengeSetup(cmd.Context(), c, exec); err != nil {
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
	cmd.Flags().BoolVar(&deployPrereqs, "deploy-prereqs", false, "build and deploy the app this challenge needs if it is not already running")
	return cmd
}

// challengeRequiredApps returns the repo apps a challenge's setup depends on, so
// `start` can verify they are deployed. Incident targets that are not repo apps
// (synthetic namespaces the fault script creates) are intentionally excluded.
func challengeRequiredApps(c *challenge.Challenge) []string {
	switch c.Setup.Type {
	case "scenario":
		if s, err := scenes.Get(c.Setup.Ref); err == nil {
			return s.Prerequisites.Apps
		}
	case "incident":
		if f, err := incEng.Get(c.Setup.Ref); err == nil && appExists(f.Target.Namespace) {
			return []string{f.Target.Namespace}
		}
	}
	return nil
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
				return fmt.Errorf("no active challenge — start one with `labctl challenge start <name>`")
			}
			c, err := eng.Load(active.ChallengeName)
			if err != nil {
				return err
			}
			penalty := hintPenaltyValue(c)

			// Delegate hint to the incident engine if setup is an incident.
			if c.Setup.Type == "incident" {
				hint, err := incEng.NextHint()
				if err != nil {
					return err
				}
				if err := eng.RecordHint(); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "[Hint %d/%d — -%d%% score penalty]\n\n%s\n",
					hint.Index, hint.Total, penalty, hint.Text)
				return nil
			}
			return fmt.Errorf("hints not available for %s-type challenges", c.Setup.Type)
		},
	}
}

func challengeSubmitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "submit",
		Short: "Run the grading checks and record your score",
		RunE: func(cmd *cobra.Command, _ []string) error {
			eng := challengeEngine()
			active, err := eng.Active()
			if err != nil {
				return err
			}
			if active == nil {
				return fmt.Errorf("no active challenge")
			}
			c, err := eng.Load(active.ChallengeName)
			if err != nil {
				return err
			}

			gradingChecks, scriptDir, err := resolveGradingChecks(cmd.Context(), c, cfg.ProjectRoot)
			if err != nil {
				return err
			}

			runner := checks.NewRunner()
			runner.ScriptDir = scriptDir
			results := runner.RunAll(cmd.Context(), gradingChecks)

			passed := 0
			for _, r := range results {
				if r.Pass {
					passed++
				}
			}
			total := len(results)
			outcome := "failed"
			if passed == total {
				outcome = "passed"
			}

			rec, err := eng.Complete(passed, total, outcome, "")
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			printGradingResults(out, c, rec, results)
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

			// Undo the setup action.
			if err := runChallengeCleanup(cmd.Context(), c, exec); err != nil {
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

// runChallengeSetup executes the challenge's setup action.
func runChallengeSetup(_ context.Context, c *challenge.Challenge, ex *executor.Executor) error {
	switch c.Setup.Type {
	case "incident":
		_, err := incEng.Inject(c.Setup.Ref, ex, false, false)
		return err
	case "scenario":
		return scenes.Up(c.Setup.Ref, ex, false)
	default:
		return fmt.Errorf("unknown setup type %q", c.Setup.Type)
	}
}

// runChallengeCleanup undoes the challenge's setup action.
func runChallengeCleanup(_ context.Context, c *challenge.Challenge, ex *executor.Executor) error {
	switch c.Setup.Type {
	case "incident":
		_, err := incEng.Resolve(c.Setup.Ref, ex, "")
		return err
	case "scenario":
		return scenes.Down(c.Setup.Ref, ex)
	default:
		return fmt.Errorf("unknown setup type %q", c.Setup.Type)
	}
}

// resolveGradingChecks returns the checks to run for grading, and the directory
// their scripts are relative to. A detection check's script lives in the fault's
// own directory, not the project root — grading it from anywhere else looks for
// a file that is not there and scores every submission zero.
func resolveGradingChecks(_ context.Context, c *challenge.Challenge, projectRoot string) ([]checks.Check, string, error) {
	if c.Grading.UseDetectionCheck && c.Setup.Type == "incident" {
		f, err := incEng.Get(c.Setup.Ref)
		if err != nil {
			return nil, "", fmt.Errorf("loading detection check for %s: %w", c.Setup.Ref, err)
		}
		return []checks.Check{incEng.ResolveCheck(f.Detection)}, f.Dir, nil
	}
	// Convert challenge GChecks to checks.Check.
	var cs []checks.Check
	for _, g := range c.Grading.Checks {
		cs = append(cs, resolveGradingCheck(c, checks.Check{
			Name:           g.Name,
			Type:           g.Type,
			URL:            g.URL,
			ExpectStatus:   g.ExpectStatus,
			Script:         g.Script,
			Query:          g.Query,
			Operator:       g.Operator,
			Value:          g.Value,
			TimeoutSeconds: g.TimeoutSeconds,
		}))
	}
	return cs, projectRoot, nil
}

// resolveGradingCheck expands template variables using the engine that owns the
// challenge's setup, so a grading check reads the same as the content it wraps.
func resolveGradingCheck(c *challenge.Challenge, k checks.Check) checks.Check {
	if c.Setup.Type == "scenario" {
		return scenes.ResolveCheck(k)
	}
	return incEng.ResolveCheck(k)
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
