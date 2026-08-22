// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sagar2395/snowopslabs/internal/config"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize the lab (setup tools + create cluster + install platform)",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("=== Setting up tools ===")
		if err := exec.RunScript("bootstrap/setup-tools.sh", cfg.Profile); err != nil {
			return fmt.Errorf("setup-tools failed: %w", err)
		}

		fmt.Println("\n=== Creating runtime ===")
		if err := exec.RunScript(
			fmt.Sprintf("runtimes/%s/up.sh", cfg.Profile),
			cfg.ClusterName,
		); err != nil {
			return fmt.Errorf("runtime-up failed: %w", err)
		}

		fmt.Println("\n=== Installing platform ===")
		return platformUpRun(cmd, args)
	},
}

var teardownCmd = &cobra.Command{
	Use:   "teardown",
	Short: "Tear down the lab (destroy apps + platform + cluster)",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("=== Destroying all apps ===")
		apps, _ := config.ListApps(cfg.ProjectRoot)
		for _, app := range apps {
			fmt.Printf("Destroying %s...\n", app)
			_ = exec.RunScript("engine/deploy.sh", "destroy", app)
		}

		fmt.Println("\n=== Removing platform ===")
		_ = platformDownRun(cmd, args)

		fmt.Println("\n=== Destroying runtime ===")
		return exec.RunScript(fmt.Sprintf("runtimes/%s/down.sh", cfg.Profile))
	},
}

var resetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Reset the lab (teardown + init)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := teardownCmd.RunE(cmd, args); err != nil {
			fmt.Printf("Warning during teardown: %v\n", err)
		}
		return initCmd.RunE(cmd, args)
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(teardownCmd)
	rootCmd.AddCommand(resetCmd)
}
