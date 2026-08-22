// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var envCmd = &cobra.Command{
	Use:   "env",
	Short: "Manage simulation environments (dev / staging / prod)",
	Long: `Manage the three simulated release environments deployed by the env-promotion scenario.

Each environment runs go-api in its own Kubernetes namespace (env-dev,
env-staging, env-prod) with a declared image tag tracked in a ConfigMap.
This models a real release pipeline without requiring multiple clusters.

Activate the environments first:
  labctl scenario up env-promotion`,
}

var envListCmd = &cobra.Command{
	Use:   "list",
	Short: "List environments and their declared image tags",
	Long: `Print a table showing each environment's app, declared image tag, and readiness.

  ENV        APP          TAG          STATUS     PROMOTED_AT
  dev        go-api       v1.2.0       running    never
  staging    go-api       v1.1.0       running    never
  prod       go-api       v1.0.0       running    never`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return exec.RunScript("scenarios/env-promotion/scripts/list.sh")
	},
}

var envPromoteApp string

var envPromoteCmd = &cobra.Command{
	Use:   "promote <from-env> <to-env>",
	Short: "Promote the declared image tag from one environment to the next",
	Long: `Promote go-api's declared image tag from one environment to the next.

  labctl env promote dev staging    # advance staging to dev's current tag
  labctl env promote staging prod   # release staging's tag to production

Promotion updates the env-metadata ConfigMap in the destination environment,
which in a real GitOps pipeline would correspond to committing updated
image-tag values to the manifest repository for ArgoCD to reconcile.

Both environments must be active. Run 'labctl scenario up env-promotion' first.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		fromEnv := args[0]
		toEnv := args[1]
		fmt.Printf("Promoting %s: %s → %s\n", envPromoteApp, fromEnv, toEnv)
		return exec.RunScript(
			"scenarios/env-promotion/scripts/promote.sh",
			fromEnv, toEnv, envPromoteApp,
		)
	},
}

func init() {
	envPromoteCmd.Flags().StringVar(&envPromoteApp, "app", "go-api", "App to promote")

	envCmd.AddCommand(envListCmd)
	envCmd.AddCommand(envPromoteCmd)
	rootCmd.AddCommand(envCmd)
}
