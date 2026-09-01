// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/sagar2395/snowopslabs/internal/lab"
	"github.com/spf13/cobra"
)

var labResetYes bool

var labCmd = &cobra.Command{
	Use:   "lab",
	Short: "Snapshot, restore, and reset the whole lab",
	Long: `Lab-level state operations. A snapshot records intent — which
platform components, apps, and scenarios are active — as a small YAML file
in .labctl/snapshots/. Restore replays the idempotent install paths to
converge back to it; reset tears everything down to post-init (cluster +
ingress only).`,
}

func labDeps(continueOnError bool) lab.Deps {
	return lab.Deps{
		Exec:            exec,
		Registry:        reg,
		Scenes:          scenes,
		ContinueOnError: continueOnError,
		Progress: func(i, total int, a lab.Action) {
			fmt.Printf("[%d/%d] %s\n", i, total, a)
		},
	}
}

func collectCurrent(name string) (*lab.Snapshot, []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return lab.Collect(ctx, name, cfg.Profile, cfg.DomainSuffix, cfg.ProjectRoot, reg, scenes)
}

func printSnapshot(s *lab.Snapshot) {
	fmt.Printf("Snapshot:  %s (taken %s)\n", s.Name, s.TakenAt.Format(time.RFC3339))
	fmt.Printf("  profile:   %s\n", s.Profile)
	fmt.Printf("  platform:  %s\n", orNone(s.Platform))
	fmt.Printf("  apps:      %s\n", orNone(s.Apps))
	fmt.Printf("  scenarios: %s\n", orNone(s.Scenarios))
}

func orNone(items []string) string {
	if len(items) == 0 {
		return "(none)"
	}
	return strings.Join(items, ", ")
}

var labSnapshotCmd = &cobra.Command{
	Use:   "snapshot [name]",
	Short: "Record the lab's current state as a named snapshot",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		snap, warnings := collectCurrent(args[0])
		for _, w := range warnings {
			fmt.Fprintf(os.Stderr, "Warning: %s\n", w)
		}
		if err := lab.NewStore(cfg.ProjectRoot).Save(snap); err != nil {
			return err
		}
		printSnapshot(snap)
		fmt.Printf("\nSaved. Restore later with: labctl lab restore %s\n", snap.Name)
		return nil
	},
}

var labSnapshotsCmd = &cobra.Command{
	Use:   "snapshots",
	Short: "List saved snapshots",
	RunE: func(cmd *cobra.Command, args []string) error {
		snaps, err := lab.NewStore(cfg.ProjectRoot).List()
		if err != nil {
			return err
		}
		if len(snaps) == 0 {
			fmt.Println("No snapshots. Take one with: labctl lab snapshot <name>")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "NAME\tTAKEN\tPLATFORM\tAPPS\tSCENARIOS")
		for _, s := range snaps {
			fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%d\n",
				s.Name, s.TakenAt.Format("2006-01-02 15:04"), len(s.Platform), len(s.Apps), len(s.Scenarios))
		}
		_ = w.Flush()
		return nil
	},
}

var labRestoreCmd = &cobra.Command{
	Use:   "restore [name]",
	Short: "Re-converge the lab to a snapshot's state",
	Long: `Replays the snapshot through the normal install paths: platform
components (ingress first), then apps, then scenarios. Everything is
idempotent — restoring over a half-converged lab is safe, and already-active
pieces are skipped.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		snap, err := lab.NewStore(cfg.ProjectRoot).Load(args[0])
		if err != nil {
			return err
		}
		printSnapshot(snap)
		plan := lab.RestorePlan(snap)
		if len(plan) == 0 {
			fmt.Println("Snapshot is empty — nothing to restore.")
			return nil
		}
		fmt.Printf("\nRestoring %d steps...\n", len(plan))
		if err := lab.Execute(plan, labDeps(false)); err != nil {
			return err
		}
		fmt.Println("\nRestore complete. Verify scenarios with: labctl scenario verify <name> --watch")
		return nil
	},
}

var labDeleteCmd = &cobra.Command{
	Use:   "delete [name]",
	Short: "Delete a saved snapshot",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return lab.NewStore(cfg.ProjectRoot).Delete(args[0])
	},
}

var labResetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Tear the lab back to post-init (cluster + ingress only)",
	Long: `Stops traffic, deactivates every scenario, destroys deployed apps,
and uninstalls platform components (keeping ingress). The cluster itself
stays up. Tip: take a snapshot first — labctl lab snapshot before-reset.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		current, warnings := collectCurrent("current")
		for _, w := range warnings {
			fmt.Fprintf(os.Stderr, "Warning: %s\n", w)
		}
		plan := lab.ResetPlan(current.Platform, current.Apps, current.Scenarios)

		fmt.Println("Reset will run:")
		for _, a := range plan {
			fmt.Printf("  - %s\n", a)
		}
		if !labResetYes {
			fmt.Print("\nThis tears down the lab (the cluster stays). Type 'yes' to continue: ")
			line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
			if strings.TrimSpace(line) != "yes" {
				fmt.Println("Aborted.")
				return nil
			}
		}

		// Continue on error: tear down as much as possible, report what stuck.
		execErr := lab.Execute(plan, labDeps(true))

		// Force-clear scenario and incident activation state
		if cleared := scenes.DeactivateAll(); len(cleared) > 0 {
			fmt.Printf("Deactivated scenarios: %s\n", strings.Join(cleared, ", "))
		}
		if incEng != nil {
			if fault := incEng.ClearActiveState(); fault != "" {
				fmt.Printf("Deactivated incident: %s\n", fault)
			}
		}

		if execErr != nil {
			return execErr
		}
		fmt.Println("\nLab reset to post-init state.")
		return nil
	},
}

func init() {
	labResetCmd.Flags().BoolVar(&labResetYes, "yes", false, "skip the confirmation prompt (non-interactive use)")

	labCmd.AddCommand(labSnapshotCmd)
	labCmd.AddCommand(labSnapshotsCmd)
	labCmd.AddCommand(labRestoreCmd)
	labCmd.AddCommand(labDeleteCmd)
	labCmd.AddCommand(labResetCmd)
	rootCmd.AddCommand(labCmd)
}
