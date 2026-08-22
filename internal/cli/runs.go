// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/sagar2395/snowopslabs/internal/store"
)

// `labctl runs` makes the durable run history usable from a terminal. In v1
// this information did not exist: job state lived in a 100-entry in-memory map
// that vanished on restart, so "what happened when I ran that yesterday?" had
// no answer. Now it does.

func runsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runs",
		Short: "Inspect and control recorded runs",
		Long: `Every operation that shells out is recorded: its status, timing,
exit code and full output. Records survive restarts, so a run can be read back
long after it finished.`,
	}
	cmd.AddCommand(runsListCmd(), runsLogsCmd(), runsCancelCmd())
	return cmd
}

// openStore opens the database read-only-ish for CLI inspection. Separated so
// tests can supply their own store.
var openStore = func(ctx context.Context) (*store.Store, error) {
	path, err := store.DefaultPath()
	if err != nil {
		return nil, err
	}
	return store.Open(ctx, path)
}

func runsListCmd() *cobra.Command {
	var (
		limit      int
		statusFlag string
		kindFlag   string
	)

	cmd := &cobra.Command{
		Use:          "list",
		Short:        "List recorded runs, newest first",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			st, err := openStore(ctx)
			if err != nil {
				return err
			}
			defer st.Close()

			filter := store.RunFilter{Limit: limit, Kind: kindFlag}
			if statusFlag != "" {
				s := store.Status(statusFlag)
				if !s.Valid() {
					return fmt.Errorf("unknown status %q (queued, running, succeeded, failed, cancelled, timed_out)", statusFlag)
				}
				filter.Status = []store.Status{s}
			}

			runs, err := st.ListRuns(ctx, filter)
			if err != nil {
				return err
			}
			return writeRunTable(cmd.OutOrStdout(), runs)
		},
	}

	cmd.Flags().IntVar(&limit, "limit", 20, "maximum runs to show")
	cmd.Flags().StringVar(&statusFlag, "status", "", "filter by status")
	cmd.Flags().StringVar(&kindFlag, "kind", "", "filter by kind, e.g. platform.install")
	return cmd
}

func writeRunTable(out io.Writer, runs []store.Run) error {
	if len(runs) == 0 {
		fmt.Fprintln(out, "No runs recorded yet.")
		return nil
	}

	w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "ID\tKIND\tTARGET\tSTATUS\tSTARTED\tDURATION")
	for _, r := range runs {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			r.ID, r.Kind, dash(r.Target), r.Status, relativeTime(r.StartedAt), duration(r))
	}
	return w.Flush()
}

func runsLogsCmd() *cobra.Command {
	var follow bool

	cmd := &cobra.Command{
		Use:          "logs <run-id>",
		Short:        "Show a run's full output",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			st, err := openStore(ctx)
			if err != nil {
				return err
			}
			defer st.Close()

			return streamRunLogs(ctx, cmd.OutOrStdout(), st, args[0], follow)
		},
	}

	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "keep printing new output until the run ends")
	return cmd
}

// streamRunLogs prints a run's transcript, optionally following it.
//
// Following reads forward from a cursor rather than subscribing to a live
// stream, which is what makes it correct: whatever happens to timing, the
// reader cannot skip a line or print one twice.
func streamRunLogs(ctx context.Context, out io.Writer, st *store.Store, runID string, follow bool) error {
	rec, err := st.GetRun(ctx, runID)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "Run %s — %s", rec.ID, rec.Kind)
	if rec.Target != "" {
		fmt.Fprintf(out, " %s", rec.Target)
	}
	fmt.Fprintf(out, " (%s)\n\n", rec.Status)

	cursor := int64(0)
	for {
		lines, err := st.ReadLogs(ctx, runID, cursor, 500)
		if err != nil {
			return err
		}
		for _, l := range lines {
			prefix := ""
			switch l.Stream {
			case store.StreamStderr:
				prefix = "! "
			case store.StreamSystem:
				prefix = "* "
			}
			fmt.Fprintf(out, "%s%s\n", prefix, l.Text)
			cursor = l.Seq
		}

		if !follow {
			break
		}

		rec, err = st.GetRun(ctx, runID)
		if err != nil {
			return err
		}
		// Re-read once more after the run ends, so the final lines — written
		// between the last poll and the terminal transition — are not missed.
		if rec.Status.Terminal() && len(lines) == 0 {
			break
		}
		if len(lines) == 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(250 * time.Millisecond):
			}
		}
	}

	if rec.Status.Terminal() {
		fmt.Fprintf(out, "\n%s", summarise(rec))
	}
	return nil
}

func summarise(r store.Run) string {
	switch r.Status {
	case store.StatusSucceeded:
		return fmt.Sprintf("Succeeded in %s.\n", r.Duration.Round(time.Millisecond))
	case store.StatusCancelled:
		return "Cancelled.\n"
	case store.StatusTimedOut:
		return fmt.Sprintf("Timed out: %s\n", r.Error)
	default:
		msg := fmt.Sprintf("Failed after %s", r.Duration.Round(time.Millisecond))
		if r.ExitCode != nil {
			msg += fmt.Sprintf(" (exit %d)", *r.ExitCode)
		}
		if r.Error != "" {
			msg += ": " + r.Error
		}
		return msg + "\n"
	}
}

func runsCancelCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cancel <run-id>",
		Short: "Cancel a run that is queued or in progress",
		Long: `Cancels a run. A run that has already started is sent SIGTERM,
then SIGKILL after a grace period — and because it executes in its own process
group, its children go with it.

If labctl is not the process executing the run (for example the run belongs to
a server you are not attached to), use the API or the UI instead.`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			st, err := openStore(ctx)
			if err != nil {
				return err
			}
			defer st.Close()

			id := args[0]
			rec, err := st.GetRun(ctx, id)
			if err != nil {
				if errors.Is(err, store.ErrRunNotFound) {
					return fmt.Errorf("no run with ID %q — list them with: labctl runs list", id)
				}
				return err
			}
			if rec.Status.Terminal() {
				return fmt.Errorf("run %s already finished (%s)", id, rec.Status)
			}

			// A CLI invocation does not own the engine that is executing the
			// run, so it records the intent; the owning process observes the
			// terminal state and stops. Being explicit beats pretending we
			// killed a process in another address space.
			if _, err := st.AppendLogs(ctx, id, []store.LogLine{{
				Stream: store.StreamSystem,
				Text:   "cancellation requested via labctl runs cancel",
			}}); err != nil {
				return err
			}
			if err := st.FinishRun(ctx, id, store.StatusCancelled, nil,
				"cancelled by user", time.Now(), rec.Duration); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Run %s cancelled.\n", id)
			return nil
		},
	}
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func duration(r store.Run) string {
	if r.Duration > 0 {
		return r.Duration.Round(time.Millisecond).String()
	}
	if r.Status == store.StatusRunning && !r.StartedAt.IsZero() {
		return time.Since(r.StartedAt).Round(time.Second).String() + "…"
	}
	return "-"
}

// relativeTime renders an instant as "3m ago", which is what a human scanning
// a run list actually wants to know.
func relativeTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func init() {
	rootCmd.AddCommand(runsCmd())
}
