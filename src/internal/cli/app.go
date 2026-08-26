// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sagar2395/snowopslabs/internal/config"
)

var appCmd = &cobra.Command{
	Use:   "app",
	Short: "Manage applications",
}

var appBuildCmd = &cobra.Command{
	Use:   "build [app-name]",
	Short: "Build an application",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		appName := args[0]
		fmt.Printf("Building %s...\n", appName)
		return exec.RunScript("src/engine/build.sh", appName)
	},
}

var appDeployCmd = &cobra.Command{
	Use:   "deploy [app-name]",
	Short: "Deploy an application",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		appName := args[0]
		fmt.Printf("Deploying %s...\n", appName)
		return exec.RunScript("src/engine/deploy.sh", "deploy", appName)
	},
}

var appDestroyCmd = &cobra.Command{
	Use:   "destroy [app-name]",
	Short: "Destroy an application",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		appName := args[0]
		fmt.Printf("Destroying %s...\n", appName)
		return exec.RunScript("src/engine/deploy.sh", "destroy", appName)
	},
}

var appListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all applications",
	RunE: func(cmd *cobra.Command, args []string) error {
		apps, err := config.ListApps(cfg.ProjectRoot)
		if err != nil {
			return err
		}
		if len(apps) == 0 {
			fmt.Println("No applications found in apps/")
			return nil
		}
		fmt.Println("Applications:")
		for _, app := range apps {
			appCfg, err := config.LoadAppConfig(cfg.ProjectRoot, app)
			if err != nil {
				fmt.Printf("  %-20s (error reading config)\n", app)
				continue
			}
			fmt.Printf("  %-20s build=%-10s deploy=%-10s\n",
				app, appCfg.BuildStrategy, appCfg.DeployStrategy)
		}
		return nil
	},
}

func init() {
	appCmd.AddCommand(appBuildCmd)
	appCmd.AddCommand(appDeployCmd)
	appCmd.AddCommand(appDestroyCmd)
	appCmd.AddCommand(appListCmd)
	rootCmd.AddCommand(appCmd)
}
