// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var runtimeCmd = &cobra.Command{
	Use:   "runtime",
	Short: "Manage the cluster runtime",
}

var runtimeUpCmd = &cobra.Command{
	Use:   "up",
	Short: "Create the cluster",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("Creating %s cluster '%s'...\n", cfg.Profile, cfg.ClusterName)
		return exec.RunScript(
			fmt.Sprintf("runtimes/%s/up.sh", cfg.Profile),
			cfg.ClusterName,
		)
	},
}

var runtimeDownCmd = &cobra.Command{
	Use:   "down",
	Short: "Destroy the cluster",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("Destroying %s cluster...\n", cfg.Profile)
		return exec.RunScript(fmt.Sprintf("runtimes/%s/down.sh", cfg.Profile))
	},
}

var runtimeStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show cluster status",
	RunE: func(cmd *cobra.Command, args []string) error {
		return exec.RunCommand("kubectl", "cluster-info")
	},
}

func init() {
	runtimeCmd.AddCommand(runtimeUpCmd)
	runtimeCmd.AddCommand(runtimeDownCmd)
	runtimeCmd.AddCommand(runtimeStatusCmd)
	rootCmd.AddCommand(runtimeCmd)
}
