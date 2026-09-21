// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sagar2395/snowopslabs/internal/config"
	"github.com/sagar2395/snowopslabs/internal/k8s"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show overall lab status",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		fmt.Println("=== Cluster ===")
		info, err := k8s.GetClusterInfo(ctx)
		if err != nil || !info.Connected {
			fmt.Println("  Status: NOT CONNECTED")
			fmt.Printf("  Profile: %s\n", cfg.Profile)
			return nil
		}
		fmt.Printf("  Context:  %s\n", info.Context)
		fmt.Printf("  Server:   %s\n", info.Server)
		fmt.Printf("  Version:  %s\n", info.K8sVersion)
		fmt.Printf("  Nodes:    %d\n", info.NodeCount)
		fmt.Printf("  Profile:  %s\n", cfg.Profile)

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
