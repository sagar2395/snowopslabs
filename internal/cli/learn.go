// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/sagar2395/snowopslabs/internal/learn"
	"github.com/sagar2395/snowopslabs/pkg/checks"
	"github.com/spf13/cobra"
)

func learnCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "learn",
		Short: "Guided learning paths",
		Long: `Work through step-by-step learning paths that combine lab setup,
scenarios, and incidents into structured modules with verifiable completion checks.`,
	}
	cmd.AddCommand(learnListCmd())
	cmd.AddCommand(learnStartCmd())
	cmd.AddCommand(learnNextCmd())
	cmd.AddCommand(learnProgressCmd())
	return cmd
}

func learnEngine() *learn.Engine {
	root := cfg.ProjectRoot
	return learn.New(
		filepath.Join(root, "learn"),
		filepath.Join(root, ".labctl", "learn"),
		filepath.Join(root, ".labctl", "history"),
	)
}

func learnListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List available learning paths",
		RunE: func(cmd *cobra.Command, _ []string) error {
			eng := learnEngine()
			paths, err := eng.Paths()
			if err != nil {
				return err
			}
			if len(paths) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No learning paths found.")
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%-30s %-8s  %s\n", "PATH", "MODULES", "DESCRIPTION")
			fmt.Fprintf(cmd.OutOrStdout(), "%s\n", strings.Repeat("-", 72))
			for _, p := range paths {
				prog, _ := eng.Progress(p.Name)
				status := progressSummary(p, prog)
				fmt.Fprintf(cmd.OutOrStdout(), "%-30s %-8s  %s\n",
					p.Name, status, truncate(p.DisplayName+" — "+p.Description, 60))
			}
			return nil
		},
	}
}

func learnStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start <path>",
		Short: "Start (or restart) a learning path",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng := learnEngine()
			prog, err := eng.StartPath(args[0])
			if err != nil {
				return err
			}
			p, _ := eng.LoadPath(args[0])
			fmt.Fprintf(cmd.OutOrStdout(), "Started: %s  (%d modules)\n",
				p.DisplayName, len(p.Modules))
			fmt.Fprintf(cmd.OutOrStdout(),
				"Run `labctl learn next %s` to begin.\n", args[0])
			_ = prog
			return nil
		},
	}
}

func learnNextCmd() *cobra.Command {
	var skipCheck bool
	cmd := &cobra.Command{
		Use:   "next [path]",
		Short: "Show the next module and verify completion",
		Long: `Show the next incomplete module's intro and objective.
After completing the task, run this again to verify the check and advance.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng := learnEngine()
			name, err := resolvePathArg(eng, args)
			if err != nil {
				return err
			}
			p, err := eng.LoadPath(name)
			if err != nil {
				return err
			}
			prog, err := eng.Progress(name)
			if err != nil {
				return err
			}
			if prog == nil {
				return fmt.Errorf("path %q not started — run `labctl learn start %s` first", name, name)
			}

			idx := learn.NextModuleIdx(p, prog)
			if idx == -1 {
				total := len(p.Modules)
				fmt.Fprintf(cmd.OutOrStdout(),
					"All %d modules complete! Path %q finished.\n", total, name)
				return nil
			}

			m := p.Modules[idx]
			out := cmd.OutOrStdout()

			// Print intro if present.
			intro, err := eng.IntroText(p, m)
			if err == nil && intro != "" {
				fmt.Fprintln(out, intro)
				fmt.Fprintln(out, strings.Repeat("─", 60))
			}

			fmt.Fprintf(out, "Module %d/%d: %s\n", idx+1, len(p.Modules), m.DisplayName)
			fmt.Fprintf(out, "Action (%s): %s\n\n", m.Action.Type, m.Action.Ref)

			if skipCheck {
				return nil
			}

			// Run the completion check.
			fmt.Fprintf(out, "Verifying check %q...\n", m.Check.Name)
			c := checksCheck(m.Check, p.Dir())
			runner := checks.NewRunner()
			runner.ScriptDir = cfg.ProjectRoot
			res := runner.Run(cmd.Context(), c)
			if res.Pass {
				if err := eng.MarkCompleteModule(p, prog, idx, ""); err != nil {
					return err
				}
				remaining := learn.NextModuleIdx(p, prog)
				fmt.Fprintf(out, "✓ Check passed. Module complete!\n")
				if remaining == -1 {
					fmt.Fprintf(out, "All %d modules complete! Path %q finished.\n",
						len(p.Modules), name)
				} else {
					next := p.Modules[remaining]
					fmt.Fprintf(out, "Next: module %d — %s\n",
						remaining+1, next.DisplayName)
				}
			} else {
				fmt.Fprintf(out, "✗ Check not passing yet: %s\n", res.Error)
				fmt.Fprintf(out,
					"Complete the task above, then run `labctl learn next %s` again.\n", name)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&skipCheck, "show-only", false, "Print the module intro without running the check")
	return cmd
}

func learnProgressCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "progress [path]",
		Short: "Show learning path progress",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng := learnEngine()
			w := cmd.OutOrStdout()

			if len(args) == 1 {
				return printPathProgress(eng, args[0], w)
			}

			// Show all paths.
			paths, err := eng.Paths()
			if err != nil {
				return err
			}
			if len(paths) == 0 {
				fmt.Fprintln(w, "No learning paths found.")
				return nil
			}
			for _, p := range paths {
				_ = printPathProgress(eng, p.Name, w)
				fmt.Fprintln(w)
			}
			return nil
		},
	}
}

// resolvePathArg returns a path name from the args slice, or the single
// available path if there is exactly one and args is empty.
func resolvePathArg(eng *learn.Engine, args []string) (string, error) {
	if len(args) == 1 {
		return args[0], nil
	}
	paths, err := eng.Paths()
	if err != nil {
		return "", err
	}
	if len(paths) == 1 {
		return paths[0].Name, nil
	}
	return "", fmt.Errorf("multiple paths available — specify one: %s",
		strings.Join(pathNames(paths), ", "))
}

func pathNames(paths []*learn.Path) []string {
	names := make([]string, len(paths))
	for i, p := range paths {
		names[i] = p.Name
	}
	return names
}

func printPathProgress(eng *learn.Engine, name string, out io.Writer) error {
	p, err := eng.LoadPath(name)
	if err != nil {
		return err
	}
	prog, _ := eng.Progress(name)
	done := 0
	if prog != nil {
		done = len(prog.CompletedIdxs)
	}
	total := len(p.Modules)
	fmt.Fprintf(out, "%s  %d/%d modules\n", p.DisplayName, done, total)
	if prog == nil {
		fmt.Fprintf(out, "  Not started. Run `labctl learn start %s`.\n", name)
		return nil
	}
	completed := map[int]bool{}
	for _, i := range prog.CompletedIdxs {
		completed[i] = true
	}
	for i, m := range p.Modules {
		mark := "  [ ]"
		if completed[i] {
			mark = "  [✓]"
		}
		fmt.Fprintf(out, "%s %d. %s\n", mark, i+1, m.DisplayName)
	}
	return nil
}

func progressSummary(p *learn.Path, prog *learn.Progress) string {
	if prog == nil {
		return fmt.Sprintf("0/%d", len(p.Modules))
	}
	return fmt.Sprintf("%d/%d", len(prog.CompletedIdxs), len(p.Modules))
}

// checksCheck converts a learn.Check to a checks.Check for the runner.
func checksCheck(c learn.Check, pathDir string) checks.Check {
	result := checks.Check{
		Name:           c.Name,
		Type:           c.Type,
		TimeoutSeconds: c.TimeoutSeconds,
	}
	switch c.Type {
	case "http":
		result.URL = c.URL
		result.ExpectStatus = c.ExpectedStatus
	case "promql":
		result.Query = c.Query
	case "script":
		script := c.Script
		if script != "" && !filepath.IsAbs(script) {
			script = filepath.Join(pathDir, script)
		}
		result.Script = script
	}
	return result
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
