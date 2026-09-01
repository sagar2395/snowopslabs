// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"fmt"
	"strings"

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
		if err := platformUpRun(cmd, args); err != nil {
			return err
		}
		printPostInitHints()
		return nil
	},
}

func printPostInitHints() {
	suffix := cfg.DomainSuffix
	if suffix == "" {
		suffix = "k3d.local"
	}
	fmt.Println("\n=== Lab is up ===")
	if !hostsBlockPresent() {
		fmt.Printf("\nTo open ingress URLs like http://grafana.%s in your browser, add the\n", suffix)
		fmt.Println("hostnames to /etc/hosts (one-time, needs sudo):")
		fmt.Println("  labctl hosts add")
		fmt.Println("(The labctl UI itself needs no hosts entry: http://localhost:3939)")
	}
	fmt.Println("\nNext: deploy an app, then run a scenario:")
	fmt.Println("  labctl app build go-api && labctl app deploy go-api")
	fmt.Println("  labctl scenario up observability-sre   (add --deploy-prereqs to auto-deploy its apps)")
}

var teardownCmd = &cobra.Command{
	Use:   "teardown",
	Short: "Tear down the lab (deactivate scenarios/incidents + destroy apps + platform + cluster)",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Deactivate scenarios and incidents FIRST — before their platform/app prerequisites are destroyed.
		fmt.Println("=== Deactivating scenarios and incidents ===")
		if cleared := scenes.DeactivateAll(); len(cleared) > 0 {
			fmt.Printf("Deactivated scenarios: %s\n", strings.Join(cleared, ", "))
		}
		if incEng != nil {
			if fault := incEng.ClearActiveState(); fault != "" {
				fmt.Printf("Deactivated incident: %s\n", fault)
			}
		}

		fmt.Println("\n=== Destroying all apps ===")
		apps, _ := config.ListApps(cfg.ProjectRoot)
		for _, app := range apps {
			fmt.Printf("Destroying %s...\n", app)
			_ = exec.RunScript("src/engine/deploy.sh", "destroy", app)
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

var setupToolsCmd = &cobra.Command{
	Use:   "setup-tools",
	Short: "Install the required CLI tools (kubectl, helm, k3d/kind, container runtime)",
	Long: `Install the tools SnowOps Labs needs, version-pinned from versions.env, for the
active PROFILE. This is the same step 'labctl init' runs first; use it on its own
to prepare a machine without creating a cluster. Equivalent to 'make setup-tools'.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return exec.RunScript("bootstrap/setup-tools.sh", cfg.Profile)
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(teardownCmd)
	rootCmd.AddCommand(resetCmd)
	rootCmd.AddCommand(setupToolsCmd)
}
