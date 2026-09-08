// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sagar2395/snowopslabs/internal/config"
	"github.com/sagar2395/snowopslabs/internal/workload"
	"github.com/sagar2395/snowopslabs/pkg/checks"
)

// verifyStatic skips the cluster and grades the declaration alone.
var verifyStatic bool

var appVerifyCmd = &cobra.Command{
	Use:   "verify [app-name]",
	Short: "Check an app against the workload contract",
	Long: `Verify that an application honours the workload contract it declares.

Two phases. The declaration phase reads apps/<name>/app.env and reports the
contract it claims; it needs no cluster. The evidence phase runs the checks that
claim implies against the deployed workload — a claim on its own is only a
claim. Use --static for the declaration phase alone.

An app is only checked for what it claims, so it is never failed for lacking a
capability it never advertised.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		appCfg, err := config.LoadAppConfig(cfg.ProjectRoot, name)
		if err != nil {
			return err
		}
		c := appCfg.Contract
		bound := appCfg.Workload()

		fmt.Printf("Contract for %s\n", name)
		fmt.Printf("  port:           %s\n", c.Port)
		fmt.Printf("  health:         %s\n", c.HealthPath)
		fmt.Printf("  ready:          %s\n", c.ReadyPath)
		if c.Has(workload.CapPrometheusMetrics) {
			fmt.Printf("  metrics:        %s\n", c.MetricsPath)
			fmt.Printf("  request metric: %s\n", c.RequestMetric)
		}
		fmt.Printf("  namespace:      %s\n", bound.Namespace)
		fmt.Printf("  capabilities:   %s\n", orNoneCaps(c.Capabilities))
		fmt.Println("\n✓ declaration is valid")

		if verifyStatic {
			return nil
		}

		verifyChecks := c.Checks(bound)
		fmt.Printf("\nVerifying %d check(s) against the deployed workload...\n\n", len(verifyChecks))
		results := newCheckRunner().RunAll(cmd.Context(), verifyChecks)
		printCheckResults(results)

		// Naming what was NOT proven matters as much as the passes: an unverified
		// claim must never read as a verified one.
		if unproven := c.UnverifiableCapabilities(); len(unproven) > 0 {
			fmt.Println("\nDeclared, not machine-checked:")
			for _, u := range unproven {
				fmt.Printf("  %-20s %s\n", u, workload.WhyUnverifiable(u))
			}
		}

		if !checks.AllPass(results) {
			printVerifyRemediation(results)
			return fmt.Errorf("app %q does not honour its declared contract", name)
		}
		fmt.Printf("\n✓ %s honours its declared contract\n", name)
		return nil
	},
}

func orNoneCaps(caps []workload.Capability) string {
	if len(caps) == 0 {
		return "none"
	}
	parts := make([]string, len(caps))
	for i, c := range caps {
		parts[i] = string(c)
	}
	return strings.Join(parts, ", ")
}

// appCapabilitiesCmd documents the closed vocabulary, so an author writing a
// scenario's requires: does not have to read the source to learn the names.
var appCapabilitiesCmd = &cobra.Command{
	Use:   "capabilities",
	Short: "List the workload capabilities a scenario may require",
	RunE: func(_ *cobra.Command, _ []string) error {
		fmt.Fprintln(os.Stdout, "Workload capabilities:")
		for _, c := range workload.AllCapabilities() {
			fmt.Printf("  %-20s %s\n", c, c.Describe())
		}
		return nil
	},
}

func init() {
	appVerifyCmd.Flags().BoolVar(&verifyStatic, "static", false,
		"check the declaration only; do not touch the cluster")
}
