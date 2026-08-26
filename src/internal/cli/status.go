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

		// Cluster info
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

		// Platform
		fmt.Println("\n=== Platform ===")
		fmt.Printf("  Ingress:  %s", cfg.IngressProvider)
		if cfg.IngressProvider != "" {
			if p, err := reg.GetProvider("ingress", cfg.IngressProvider); err == nil {
				if k8s.NamespaceExists(ctx, p.Namespace()) {
					fmt.Print("  [running]")
				}
			}
		}
		fmt.Println()
		fmt.Printf("  Metrics:  %s", cfg.MetricsProvider)
		if cfg.MetricsProvider != "" {
			if p, err := reg.GetProvider("monitoring/metrics", cfg.MetricsProvider); err == nil {
				if k8s.NamespaceExists(ctx, p.Namespace()) {
					fmt.Print("  [running]")
				}
			}
		}
		fmt.Println()

		// Apps
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

func init() {
	rootCmd.AddCommand(statusCmd)
}
