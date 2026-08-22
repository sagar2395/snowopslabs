// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"fmt"
	"strings"

	"github.com/sagar2395/snowopslabs/internal/platform"
	"github.com/spf13/cobra"
)

var platformCmd = &cobra.Command{
	Use:   "platform",
	Short: "Manage platform components",
}

// providerForCategory returns the configured provider name for a platform
// category (matching the env-var selection), or "" if none is selected.
func providerForCategory(category string) string {
	switch category {
	case "ingress":
		return cfg.IngressProvider
	case "monitoring/metrics":
		return cfg.MetricsProvider
	case "logging":
		return cfg.LoggingProvider
	case "tracing":
		return cfg.TracingProvider
	case "gitops":
		return cfg.GitOpsProvider
	case "chaos":
		return cfg.ChaosProvider
	case "security/policy":
		return cfg.PolicyProvider
	case "secrets":
		return cfg.SecretsProvider
	case "mesh":
		return cfg.MeshProvider
	case "data":
		return cfg.DataProvider
	case "autoscaling":
		return cfg.AutoscalingProvider
	default:
		return ""
	}
}

// resolveProvider picks the provider to act on for a single category. It prefers
// the configured selection; if none is set it falls back to the registry — using
// the sole provider when a category has exactly one, or erroring with the choices
// when it has several (e.g. mesh: istio|linkerd).
func resolveProvider(category string) (string, error) {
	if p := providerForCategory(category); p != "" {
		return p, nil
	}
	providers := reg.GetProviders(category)
	if len(providers) == 0 {
		return "", fmt.Errorf("unknown platform category %q", category)
	}
	if len(providers) == 1 {
		return providers[0].Name, nil
	}
	var names []string
	for _, p := range providers {
		names = append(names, p.Name)
	}
	envVar := strings.ToUpper(strings.ReplaceAll(category, "/", "_")) + "_PROVIDER"
	return "", fmt.Errorf("category %q has multiple providers (%s); select one with %s",
		category, strings.Join(names, ", "), envVar)
}

// resolveTarget interprets a platform target argument as either a category
// (whose provider is selected via env, or is the sole/only one) or an explicit
// "category/provider" spec (e.g. data/kafka), returning the concrete
// category + provider to act on.
func resolveTarget(arg string) (category, provider string, err error) {
	// 1. The whole arg is a category (possibly nested, e.g. monitoring/metrics).
	if len(reg.GetProviders(arg)) > 0 {
		p, e := resolveProvider(arg)
		return arg, p, e
	}
	// 2. The arg is "category/provider" — split at the last slash.
	if i := strings.LastIndex(arg, "/"); i > 0 {
		cat, prov := arg[:i], arg[i+1:]
		if _, e := reg.GetProvider(cat, prov); e == nil {
			return cat, prov, nil
		}
	}
	return "", "", fmt.Errorf("unknown platform target %q", arg)
}

func platformUpRun(cmd *cobra.Command, args []string) error {
	// Per-target install: `labctl platform up <category|category/provider>`.
	if len(args) == 1 {
		category, provider, err := resolveTarget(args[0])
		if err != nil {
			return err
		}
		fmt.Printf("Installing %s/%s...\n", category, provider)
		if err := reg.Install(category, provider, exec); err != nil {
			return fmt.Errorf("%s/%s install failed: %w", category, provider, err)
		}
		fmt.Printf("\n%s/%s installed successfully.\n", category, provider)
		return nil
	}

	// Install ingress
	if cfg.IngressProvider != "" {
		fmt.Printf("Installing ingress (%s)...\n", cfg.IngressProvider)
		if err := reg.Install("ingress", cfg.IngressProvider, exec); err != nil {
			return fmt.Errorf("ingress install failed: %w", err)
		}
	}

	// Install monitoring (metrics)
	if cfg.MetricsProvider != "" {
		fmt.Printf("Installing metrics (%s)...\n", cfg.MetricsProvider)
		if err := reg.Install("monitoring/metrics", cfg.MetricsProvider, exec); err != nil {
			fmt.Printf("Warning: metrics install: %v\n", err)
		}
	}

	// Install grafana (visualization)
	fmt.Println("Installing grafana...")
	if err := reg.Install("monitoring", "grafana", exec); err != nil {
		fmt.Printf("Warning: grafana install: %v\n", err)
	}

	fmt.Println("\nPlatform installed successfully.")
	return nil
}

func platformDownRun(cmd *cobra.Command, args []string) error {
	// Per-target uninstall: `labctl platform down <category|category/provider>`.
	if len(args) == 1 {
		category, provider, err := resolveTarget(args[0])
		if err != nil {
			return err
		}
		fmt.Printf("Uninstalling %s/%s...\n", category, provider)
		if err := reg.Uninstall(category, provider, exec); err != nil {
			return fmt.Errorf("%s/%s uninstall failed: %w", category, provider, err)
		}
		fmt.Printf("\n%s/%s uninstalled.\n", category, provider)
		return nil
	}

	// Uninstall in reverse order
	fmt.Println("Uninstalling grafana...")
	_ = reg.Uninstall("monitoring", "grafana", exec)

	if cfg.MetricsProvider != "" {
		fmt.Printf("Uninstalling metrics (%s)...\n", cfg.MetricsProvider)
		_ = reg.Uninstall("monitoring/metrics", cfg.MetricsProvider, exec)
	}

	if cfg.IngressProvider != "" {
		fmt.Printf("Uninstalling ingress (%s)...\n", cfg.IngressProvider)
		_ = reg.Uninstall("ingress", cfg.IngressProvider, exec)
	}

	fmt.Println("\nPlatform uninstalled.")
	return nil
}

var platformUpCmd = &cobra.Command{
	Use:   "up [category|category/provider]",
	Short: "Install all platform components, or one (e.g. mesh, data/kafka)",
	Args:  cobra.MaximumNArgs(1),
	RunE:  platformUpRun,
}

var platformDownCmd = &cobra.Command{
	Use:   "down [category|category/provider]",
	Short: "Uninstall all platform components, or one (e.g. mesh, data/kafka)",
	Args:  cobra.MaximumNArgs(1),
	RunE:  platformDownRun,
}

var platformStatusCmd = &cobra.Command{
	Use:   "status [category|category/provider]",
	Short: "Show platform component status (all, or one target)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		categories := reg.Categories()
		if len(categories) == 0 {
			fmt.Println("No platform components found.")
			return nil
		}

		// Per-target status: `labctl platform status <category|category/provider>`.
		if len(args) == 1 {
			cat := args[0]
			providers := reg.GetProviders(cat)
			// Allow a single "category/provider" target (e.g. data/kafka).
			if len(providers) == 0 {
				if c, p, err := resolveTarget(cat); err == nil {
					if pr, e := reg.GetProvider(c, p); e == nil {
						cat = c
						providers = []platform.Provider{*pr}
					}
				}
			}
			if len(providers) == 0 {
				return fmt.Errorf("unknown platform target %q", cat)
			}
			for _, p := range providers {
				if p.HasScript("status.sh") {
					fmt.Printf("--- %s/%s ---\n", cat, p.Name)
					_ = reg.Status(cat, p.Name, exec)
					fmt.Println()
				}
			}
			return nil
		}

		for _, cat := range categories {
			providers := reg.GetProviders(cat)
			for _, p := range providers {
				if p.HasScript("status.sh") {
					fmt.Printf("--- %s/%s ---\n", cat, p.Name)
					_ = reg.Status(cat, p.Name, exec)
					fmt.Println()
				}
			}
		}
		return nil
	},
}

func init() {
	platformCmd.AddCommand(platformUpCmd)
	platformCmd.AddCommand(platformDownCmd)
	platformCmd.AddCommand(platformStatusCmd)
	rootCmd.AddCommand(platformCmd)
}
