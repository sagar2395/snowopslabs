// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"github.com/spf13/cobra"
)

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Run validation checks",
}

var checkToolsCmd = &cobra.Command{
	Use:   "tools",
	Short: "Check that required CLI tools are installed",
	RunE: func(cmd *cobra.Command, args []string) error {
		return exec.RunScript("engine/check.sh", "tools")
	},
}

var checkClusterCmd = &cobra.Command{
	Use:   "cluster",
	Short: "Check cluster connectivity",
	RunE: func(cmd *cobra.Command, args []string) error {
		return exec.RunScript("engine/check.sh", "cluster")
	},
}

var checkIngressCmd = &cobra.Command{
	Use:   "ingress",
	Short: "Check ingress controller status",
	RunE: func(cmd *cobra.Command, args []string) error {
		return exec.RunScript("engine/check.sh", "ingress")
	},
}

func init() {
	checkCmd.AddCommand(checkToolsCmd)
	checkCmd.AddCommand(checkClusterCmd)
	checkCmd.AddCommand(checkIngressCmd)
	rootCmd.AddCommand(checkCmd)
}
