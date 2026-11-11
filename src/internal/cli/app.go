// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/snowops-oss/snowopslabs/internal/config"
)

var appCmd = &cobra.Command{
	Use:   "app",
	Short: "Manage applications",
}

// requireApp fails, naming the apps that exist, when apps/<name>/app.env does
// not load. The engine scripts would otherwise run for a name that is a typo
// and fail on its missing build settings.
func requireApp(name string) error {
	_, err := config.LoadAppConfig(cfg.ProjectRoot, name)
	return err
}

var appBuildCmd = &cobra.Command{
	Use:   "build [app-name]",
	Short: "Build an application",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		appName := args[0]
		if err := requireApp(appName); err != nil {
			return err
		}
		fmt.Printf("Building %s...\n", appName)
		return scriptExec.RunScript("src/engine/build.sh", appName)
	},
}

var appDeployCmd = &cobra.Command{
	Use:   "deploy [app-name]",
	Short: "Deploy an application",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		appName := args[0]
		if err := requireApp(appName); err != nil {
			return err
		}
		fmt.Printf("Deploying %s...\n", appName)
		if err := scriptExec.RunScript("src/engine/deploy.sh", "deploy", appName); err != nil {
			return err
		}
		warnMissingHosts(cmd.Context(), os.Stderr, cfg.DomainSuffix)
		return nil
	},
}

var appDestroyCmd = &cobra.Command{
	Use:   "destroy [app-name]",
	Short: "Destroy an application",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		appName := args[0]
		if err := requireApp(appName); err != nil {
			return err
		}
		fmt.Printf("Destroying %s...\n", appName)
		return scriptExec.RunScript("src/engine/deploy.sh", "destroy", appName)
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
			fmt.Printf("  %-20s build=%-8s deploy=%-6s port=%-6s capabilities=%s\n",
				app, appCfg.BuildStrategy, appCfg.DeployStrategy,
				appCfg.Contract.Port, orNoneCaps(appCfg.Contract.Capabilities))
		}
		return nil
	},
}

func init() {
	appCmd.AddCommand(appBuildCmd)
	appCmd.AddCommand(appDeployCmd)
	appCmd.AddCommand(appDestroyCmd)
	appCmd.AddCommand(appListCmd)
	appCmd.AddCommand(appVerifyCmd)
	appCmd.AddCommand(appCapabilitiesCmd)
	rootCmd.AddCommand(appCmd)
}
