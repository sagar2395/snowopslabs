// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/sagar2395/snowopslabs/internal/capacity"
	"github.com/sagar2395/snowopslabs/internal/config"
	"github.com/sagar2395/snowopslabs/internal/k8s"
	"github.com/sagar2395/snowopslabs/internal/toolchain"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show overall lab status",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		fmt.Println("=== Cluster ===")
		info, err := k8s.GetClusterInfo(ctx)
		if err != nil || !info.Connected {
			printUnreachable(cmd.OutOrStdout(), info)
			return nil
		}
		fmt.Printf("  Context:  %s\n", info.Context)
		fmt.Printf("  Server:   %s\n", info.Server)
		fmt.Printf("  Version:  %s\n", info.K8sVersion)
		fmt.Printf("  Nodes:    %d\n", info.NodeCount)
		fmt.Printf("  Profile:  %s\n", cfg.Profile)
		if cfg.Profile == "k3d" {
			notReady, _ := k8s.NotReadyNodes(ctx)
			printNodeTrouble(ctx, cmd.OutOrStdout(), toolchain.NewExec(), notReady)
		}

		fmt.Println("\n=== Platform ===")
		fmt.Printf("  Ingress:  %s%s\n", cfg.IngressProvider,
			providerState(ctx, "ingress", cfg.IngressProvider))
		fmt.Printf("  Metrics:  %s%s\n", cfg.MetricsProvider,
			providerState(ctx, "monitoring/metrics", cfg.MetricsProvider))

		fmt.Println("\n=== Apps ===")
		apps, _ := config.ListApps(cfg.ProjectRoot)
		for _, app := range apps {
			appCfg, _ := config.LoadAppConfig(cfg.ProjectRoot, app)
			ns := app
			if appCfg != nil && appCfg.Namespace != "" {
				ns = appCfg.Namespace
			}
			status, _ := k8s.GetAppStatus(ctx, app, ns)
			if status != nil && status.Deployed {
				fmt.Printf("  %-20s replicas=%s ready=%s\n", app, status.Replicas, status.Ready)
			} else {
				fmt.Printf("  %-20s [not deployed]\n", app)
			}
		}

		return nil
	},
}

// printUnreachable explains a cluster that cannot be reached. Platform and app
// state are left out because without the API every component would read "not
// installed".
func printUnreachable(out io.Writer, info *k8s.ClusterInfo) {
	if info == nil || info.Context == "" {
		fmt.Fprintln(out, "  Status:   NO CLUSTER (no kubectl context is set)")
		fmt.Fprintf(out, "  Profile:  %s\n", cfg.Profile)
		fmt.Fprintln(out, "\nCreate the lab with: labctl init")
		return
	}
	fmt.Fprintf(out, "  Status:   UNREACHABLE (%s)\n", info.Error)
	fmt.Fprintf(out, "  Context:  %s\n", info.Context)
	fmt.Fprintf(out, "  Profile:  %s\n", cfg.Profile)
	fmt.Fprintln(out, "\nThe cluster is configured but not answering. Usually Docker is stopped (after a")
	fmt.Fprintln(out, "reboot) or short of memory. 'labctl doctor' checks Docker; 'labctl init' brings")
	fmt.Fprintln(out, "the lab back without losing it.")
}

// printNodeTrouble reports what a Docker VM restart leaves on a k3d lab: an
// inotify limit back at the VM's default, and nodes among notReady whose k3s
// agent died of it while their containers stay up. It only reports; the
// recovery is printed for the user to run.
func printNodeTrouble(ctx context.Context, out io.Writer, runner toolchain.Runner, notReady []string) {
	label, _ := capacity.NodeLabel("k3d", cfg.ClusterName)
	limit, err := capacity.InotifyLimit(ctx, runner, label)
	low := err == nil && limit > 0 && limit < capacity.InotifyInstances
	var stuck []capacity.StuckNode
	if len(notReady) > 0 {
		stuck, _ = capacity.StuckNodes(ctx, runner, label, notReady)
	}
	if !low && len(stuck) == 0 {
		return
	}

	fmt.Fprintln(out)
	if low {
		fmt.Fprintf(out, "  ! fs.inotify.max_user_instances is %d; the lab needs %d. A Docker VM restart\n"+
			"    resets it. The next lab, platform, scenario, incident or traffic command\n"+
			"    raises it.\n",
			limit, capacity.InotifyInstances)
	}
	for _, n := range stuck {
		cause := ""
		if n.TooManyFiles {
			cause = "\n    k3s logged \"too many open files\"."
		}
		fmt.Fprintf(out, "  ! %s is NotReady while its container is up.%s\n", n.Name, cause)
	}
	fmt.Fprintln(out, "\n  Recover with:")
	if low {
		fmt.Fprintf(out, "    for n in $(docker ps --filter label=%s --format '{{.Names}}'); do\n", label)
		fmt.Fprintf(out, "      docker exec \"$n\" sysctl -w fs.inotify.max_user_instances=%d\n", capacity.InotifyInstances)
		fmt.Fprintln(out, "    done")
	}
	for _, n := range stuck {
		fmt.Fprintf(out, "    docker restart %s\n", n.Name)
	}
}

// providerState reports a platform component's state from its pods'
// readiness. A namespace can exist while its pods are crash-looping.
func providerState(ctx context.Context, kind, provider string) string {
	if provider == "" {
		return ""
	}
	p, err := reg.GetProvider(kind, provider)
	if err != nil {
		return ""
	}
	ready, total, exists := k8s.NamespaceHealth(ctx, p.Namespace())
	return platformState(ready, total, exists)
}

func platformState(ready, total int, exists bool) string {
	switch {
	case !exists:
		return "  [not installed]"
	case total == 0:
		return "  [no workloads]"
	case ready < total:
		return fmt.Sprintf("  [degraded %d/%d ready]", ready, total)
	default:
		return "  [running]"
	}
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
