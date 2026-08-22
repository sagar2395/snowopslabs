// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"context"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/sagar2395/snowopslabs/internal/toolchain"
)

// doctorCmd tells the user what is wrong with their environment *before* they
// hit it forty seconds into a cluster build. Every failure line names the tool,
// what breaks without it, and the platform-specific fix.
func doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check that your environment can run SnowOps Labs",
		Long: `Verifies every external tool SnowOps Labs depends on: that it is
installed, that it is new enough, and that the cluster is reachable.

Each problem is reported with the reason it matters and how to fix it. Exits
non-zero if anything required is missing or out of date, so it is safe to use
as a gate in a script.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDoctor(cmd.Context(), cmd.OutOrStdout(), toolchain.NewExec())
		},
	}
}

// runDoctor is separated from the cobra plumbing so it can be tested against a
// fake runner without a terminal.
func runDoctor(ctx context.Context, out io.Writer, runner toolchain.Runner) error {
	if ctx == nil {
		ctx = context.Background()
	}

	results, err := toolchain.NewPreflight(runner).Check(ctx)
	if err != nil {
		return err
	}

	fmt.Fprintln(out, "SnowOps Labs environment check")
	fmt.Fprintln(out)

	w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "TOOL\tSTATUS\tVERSION\tREQUIRED")
	for _, r := range results {
		version := r.Version
		if version == "" {
			version = "-"
		}
		required := r.Required
		if required == "" {
			required = "any"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", r.Binary, statusLabel(r), version, required)
	}
	_ = w.Flush()

	var problems, warnings []toolchain.CheckResult
	for _, r := range results {
		if r.Detail == "" {
			continue
		}
		if r.OK() {
			if r.Status != toolchain.CheckOK {
				warnings = append(warnings, r)
			}
			continue
		}
		problems = append(problems, r)
	}

	if len(warnings) > 0 {
		fmt.Fprintln(out, "\nNotes:")
		for _, r := range warnings {
			fmt.Fprintf(out, "  - %s\n", r.Detail)
		}
	}

	if len(problems) > 0 {
		fmt.Fprintln(out, "\nProblems to fix:")
		for _, r := range problems {
			fmt.Fprintf(out, "  ✗ %s\n", r.Detail)
		}
		fmt.Fprintln(out)
		return fmt.Errorf("%d required tool(s) missing or out of date", len(problems))
	}

	fmt.Fprintln(out, "\n✓ Everything SnowOps Labs needs is installed and current.")
	return nil
}

func statusLabel(r toolchain.CheckResult) string {
	switch r.Status {
	case toolchain.CheckOK:
		return "ok"
	case toolchain.CheckMissing:
		if r.Optional {
			return "missing (optional)"
		}
		return "MISSING"
	case toolchain.CheckOutdated:
		if r.Optional {
			return "outdated (optional)"
		}
		return "OUTDATED"
	default:
		return "unknown"
	}
}

func init() {
	rootCmd.AddCommand(doctorCmd())
}
