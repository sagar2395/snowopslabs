// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/snowops-oss/snowopslabs/internal/store"
)

// `labctl runs` lists recorded runs, prints their transcripts, and cancels
// them.

func runsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runs",
		Short: "Inspect and control recorded runs",
		Long: `Every operation that shells out is recorded: its status, timing,
exit code and full output. Records survive restarts, so a run can be read back
long after it finished.`,
	}
	cmd.AddCommand(runsListCmd(), runsLogsCmd(), runsCancelCmd(), runsRecoverCmd())
	return cmd
}

// openStore opens the run store. Tests replace it.
// logPollInterval is how long a followed log waits before reading new lines.
var logPollInterval = 250 * time.Millisecond

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
		Use:   "list",
		Short: "List recorded runs, newest first",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			st, err := openStore(ctx)
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()

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
		status := string(r.Status)
		switch r.RecoveryState {
		case store.RecoveryRequired:
			status = "Recovery required"
		case store.RecoveryReleased:
			status = "Key released by operator"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			r.ID, r.Kind, dash(r.Target), status, relativeTime(r.StartedAt), duration(r))
	}
	if err := w.Flush(); err != nil {
		return err
	}
	for _, r := range runs {
		if r.RecoveryState == store.RecoveryRequired || r.RecoveryState == store.RecoveryReleased {
			if _, err := fmt.Fprintf(out, "\nRun %s: %s", r.ID, summarise(r)); err != nil {
				return err
			}
		}
	}
	return nil
}

func runsLogsCmd() *cobra.Command {
	var follow bool

	cmd := &cobra.Command{
		Use:   "logs <run-id>",
		Short: "Show a run's full output",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			st, err := openStore(ctx)
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()

			rec, err := st.GetRun(ctx, args[0])
			if err != nil {
				return err
			}
			if rec.Hidden() {
				return fmt.Errorf("run %s injected a silent incident: its output is withheld so it cannot name the fault.\n"+
					"'labctl incident status' says when it is fixed, and 'labctl incident history' then names it", rec.ID)
			}
			return streamRunLogs(ctx, cmd.OutOrStdout(), st, args[0], follow)
		},
	}

	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "keep printing new output until the run ends")
	return cmd
}

// runLogSource is the part of the store a transcript is read from.
type runLogSource interface {
	GetRun(ctx context.Context, id string) (store.Run, error)
	ReadLogs(ctx context.Context, runID string, after int64, limit int) ([]store.LogLine, error)
}

// streamRunLogs prints a run's transcript, optionally following it.
//
// Following polls the store from a cursor, so no line is skipped or printed
// twice.
func streamRunLogs(ctx context.Context, out io.Writer, st runLogSource, runID string, follow bool) error {
	rec, err := st.GetRun(ctx, runID)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "Run %s - %s", rec.ID, rec.Kind)
	if rec.Target != "" {
		fmt.Fprintf(out, " %s", rec.Target)
	}
	fmt.Fprintf(out, " (%s)\n\n", rec.Status)

	cursor := int64(0)
	ended := false
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
		// Stop only on an empty read made after the run was seen to end: a
		// run can write its last lines between a read and the status check.
		if ended && len(lines) == 0 {
			break
		}

		rec, err = st.GetRun(ctx, runID)
		if err != nil {
			return err
		}
		ended = rec.Status.Terminal() || rec.RecoveryState == store.RecoveryRequired
		if !ended && len(lines) == 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(logPollInterval):
			}
		}
	}

	if rec.Status.Terminal() || rec.RecoveryState == store.RecoveryRequired {
		fmt.Fprintf(out, "\n%s", summarise(rec))
	}
	if follow && rec.RecoveryState == store.RecoveryRequired {
		return fmt.Errorf("%w: check the lab, then use labctl runs recover %s --force", store.ErrRecoveryRequired, rec.ID)
	}
	return nil
}

func summarise(r store.Run) string {
	if r.RecoveryState == store.RecoveryRequired {
		return fmt.Sprintf("Recovery required. The key remains reserved.\nCheck the lab, then use: labctl runs recover %s --force\n", r.ID)
	}
	if r.RecoveryState == store.RecoveryReleased {
		return fmt.Sprintf("Key released by operator %s at %s.\nThe key was released. The operation may still be running.\n", r.ReleasedBy, r.ReleasedAt.UTC().Format(time.RFC3339Nano))
	}
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
then SIGKILL after a grace period - and because it executes in its own process
group, its children go with it.

If labctl is not the process executing the run (for example the run belongs to
a server you are not attached to), use the API or the UI instead.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			st, err := openStore(ctx)
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()

			id := args[0]
			rec, err := st.GetRun(ctx, id)
			if err != nil {
				if errors.Is(err, store.ErrRunNotFound) {
					return fmt.Errorf("no run with ID %q - list them with: labctl runs list", id)
				}
				return err
			}
			if rec.Status.Terminal() {
				return fmt.Errorf("run %s already finished (%s)", id, rec.Status)
			}
			if rec.RecoveryState == store.RecoveryRequired {
				return fmt.Errorf("%w: check the lab, then use labctl runs recover %s --force", store.ErrRecoveryRequired, id)
			}

			// The run belongs to another process, so record the cancellation in
			// the store; that process sees it and stops the run.
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

func runsRecoverCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "recover <run-id>",
		Short: "Release an interrupted run's key after checking the lab",
		Long: `Shows interrupted work and its reserved key. With --force, records an
operator release and permits another operation on that key. The operation may
still be running. This command does not stop processes or verify cleanup.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openStore(cmd.Context())
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()
			rec, err := st.GetRun(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if rec.RecoveryState == store.RecoveryReleased {
				_, err = fmt.Fprint(cmd.OutOrStdout(), summarise(rec))
				return err
			}
			if rec.RecoveryState != store.RecoveryRequired {
				return fmt.Errorf("run %s does not require recovery; inspect it with labctl runs list", rec.ID)
			}
			warning := "The operation may still be running. Releasing this key allows another operation on the same key. Check the lab before continuing.\n"
			if rec.RecoveryReason == "owner_missing" {
				warning = "labctl can't check whether this work is still running because its owner file is missing. Check the lab before releasing it.\n" + warning
			}
			if !force {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Run %s: Recovery required\nKey: %s\n%sRetry with: labctl runs recover %s --force\n", rec.ID, dash(rec.LockKey), warning, rec.ID); err != nil {
					return err
				}
				return store.ErrRecoveryRequired
			}
			if _, err := fmt.Fprint(cmd.ErrOrStderr(), warning); err != nil {
				return err
			}
			actor := "uid=" + strconv.Itoa(os.Getuid())
			if u, err := user.LookupId(strconv.Itoa(os.Getuid())); err == nil && u.Username != "" {
				actor += " (" + u.Username + ")"
			}
			rec, err = st.ReleaseRecoveredRun(cmd.Context(), rec.ID, actor)
			if err != nil {
				return err
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), summarise(rec))
			return err
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "release the key even though the operation may still be running")
	return cmd
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func duration(r store.Run) string {
	if r.RecoveryState == store.RecoveryRequired {
		return "-"
	}
	if r.Duration > 0 {
		return r.Duration.Round(time.Millisecond).String()
	}
	if r.Status == store.StatusRunning && !r.StartedAt.IsZero() {
		return time.Since(r.StartedAt).Round(time.Second).String() + "…"
	}
	return "-"
}

// relativeTime renders an instant as, for example, "3m ago".
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
