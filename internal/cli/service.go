// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var serviceCmd = &cobra.Command{
	Use:   "service",
	Short: "Manage shared services (redis, postgres, etc.)",
}

var serviceListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available shared services",
	RunE: func(cmd *cobra.Command, args []string) error {
		svcs := svcReg.List()
		if len(svcs) == 0 {
			fmt.Println("No shared services found.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintf(w, "NAME\tPATH\n")
		for _, s := range svcs {
			fmt.Fprintf(w, "%s\t%s\n", s.Name, s.Path)
		}
		w.Flush()
		return nil
	},
}

var serviceUpCmd = &cobra.Command{
	Use:   "up <name>",
	Short: "Install a shared service",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		fmt.Printf("Installing service %s...\n", name)
		if err := svcReg.Install(name, exec); err != nil {
			return fmt.Errorf("service install failed: %w", err)
		}
		fmt.Printf("Service %s installed.\n", name)
		return nil
	},
}

var serviceDownCmd = &cobra.Command{
	Use:   "down <name>",
	Short: "Uninstall a shared service",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		fmt.Printf("Uninstalling service %s...\n", name)
		if err := svcReg.Uninstall(name, exec); err != nil {
			return fmt.Errorf("service uninstall failed: %w", err)
		}
		fmt.Printf("Service %s uninstalled.\n", name)
		return nil
	},
}

var serviceStatusCmd = &cobra.Command{
	Use:   "status [name]",
	Short: "Show service status",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 1 {
			return svcReg.Status(args[0], exec)
		}

		svcs := svcReg.List()
		if len(svcs) == 0 {
			fmt.Println("No shared services found.")
			return nil
		}

		for _, s := range svcs {
			if s.HasScript("status.sh") {
				fmt.Printf("--- %s ---\n", s.Name)
				_ = svcReg.Status(s.Name, exec)
				fmt.Println()
			}
		}
		return nil
	},
}

func init() {
	serviceCmd.AddCommand(serviceListCmd)
	serviceCmd.AddCommand(serviceUpCmd)
	serviceCmd.AddCommand(serviceDownCmd)
	serviceCmd.AddCommand(serviceStatusCmd)
	rootCmd.AddCommand(serviceCmd)
}
