// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/sagar2395/snowopslabs/internal/config"
	"github.com/sagar2395/snowopslabs/internal/executor"
	"github.com/sagar2395/snowopslabs/internal/incident"
	"github.com/sagar2395/snowopslabs/internal/platform"
	"github.com/sagar2395/snowopslabs/internal/runtime"
	"github.com/sagar2395/snowopslabs/internal/scenario"
	"github.com/sagar2395/snowopslabs/internal/services"
)

var (
	projectDir string
	verbose    bool

	cfg    *config.Config
	exec   *executor.Executor
	reg    *platform.Registry
	scenes *scenario.Engine
	incEng *incident.Engine
	svcReg *services.Registry
	rtm    *runtime.Manager
)

var rootCmd = &cobra.Command{
	Use:   "labctl",
	Short: "SnowOps Labs — platform engineering simulator control plane",
	Long:  `labctl is the CLI, web UI, and API for SnowOps Labs, a Kubernetes platform engineering simulator.`,
	// A runtime failure (a helm error, a failing check) is not a usage mistake —
	// don't dump the full command help after it. Inherited by all subcommands, so
	// only genuine arg/flag errors still print usage. (Individual commands that
	// already set this are now redundant but harmless.)
	SilenceUsage: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Skip init for commands that must work when the environment is
		// broken. doctor in particular exists to diagnose exactly the
		// situations that would make this initialisation fail.
		if skipSharedInit(cmd) {
			return nil
		}

		// Configure log level before doing anything else so debug output is visible.
		logLevel := slog.LevelWarn
		if verbose {
			logLevel = slog.LevelDebug
		}
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})))

		slog.Debug("loading config", "projectDir", projectDir)
		var err error
		cfg, err = config.Load(projectDir)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		slog.Debug("config loaded", "root", cfg.ProjectRoot, "profile", cfg.Profile, "cluster", cfg.ClusterName)

		exec = executor.New(cfg.ProjectRoot)
		// Propagate every value declared in .env / runtime.env so child scripts
		// and Make targets see them. config.Load no longer mutates the process
		// environment, so this explicit hand-off replaces the old os.Setenv side
		// effect (e.g. METRICS_PROVIDER, REGISTRY_TYPE consumed by make targets).
		for k, v := range cfg.ScriptEnv {
			exec.SetEnv(k, v)
		}
		// The core cluster knobs are set explicitly too, so they reach scripts
		// even when a checkout has no .env / runtime.env (defaults still apply).
		exec.SetEnv("CLUSTER_NAME", cfg.ClusterName)
		exec.SetEnv("DOMAIN_SUFFIX", cfg.DomainSuffix)
		exec.SetEnv("HTTP_PORT", cfg.HTTPPort)
		exec.SetEnv("HTTPS_PORT", cfg.HTTPSPort)
		exec.SetEnv("INGRESS_CLASS", cfg.IngressClass)
		exec.SetEnv("INGRESS_PROVIDER", cfg.IngressProvider)
		exec.SetEnv("STORAGE_CLASS", cfg.StorageClass)
		exec.SetEnv("PROFILE", cfg.Profile)
		exec.SetEnv("MONITORING_NAMESPACE", cfg.MonitoringNamespace)
		reg = platform.NewRegistryWithNamespace(cfg.ProjectRoot, cfg.MonitoringNamespace)
		scenes = scenario.NewEngine(cfg.ProjectRoot, cfg.DomainSuffix, cfg.Profile)
		scenes.MonitoringNamespace = cfg.MonitoringNamespace
		scenes.IngressClass = cfg.IngressClass
		incEng = incident.NewEngine(cfg.ProjectRoot, cfg.DomainSuffix)
		incEng.AlertmanagerURL = os.Getenv("ALERTMANAGER_URL")
		if incEng.AlertmanagerURL == "" {
			incEng.AlertmanagerURL = "http://alertmanager." + cfg.DomainSuffix
		}
		svcReg = services.NewRegistry(cfg.ProjectRoot)
		rtm = runtime.NewManager(cfg.ProjectRoot, cfg.ClusterName)
		slog.Debug("registries initialised", "runtimes", rtm.Names())
		return nil
	},
}

// skipSharedInit reports whether a command should run without the shared
// cluster-config initialisation (config, executor, and the scenario/incident/…
// engines). These commands either diagnose a broken environment (doctor), need
// no config (help/completion/validate), or are served from the local run store
// (`runs list|logs|cancel`).
//
// It matches on the full command path, not the bare leaf name. Several
// unrelated commands share the leaf name "list" (scenario, app, service,
// incident, …); those DO need the engines, so only the `runs` subcommands may
// skip on those leaf names. Matching the leaf alone was a real bug: it made
// `labctl scenario list` nil-panic because `scenes` was never constructed.
func skipSharedInit(cmd *cobra.Command) bool {
	switch cmd.Name() {
	case "completion", "help", "doctor", "runs", "validate":
		return true
	case "list", "logs", "cancel":
		p := cmd.Parent()
		return p != nil && p.Name() == "runs"
	}
	return false
}

// SetVersion stamps the CLI version (from the build's -X main.version). Setting
// it makes `labctl --version` report the build, so a reviewer can say exactly
// which binary they are running.
func SetVersion(v string) {
	if v == "" {
		v = "dev"
	}
	rootCmd.Version = v
	rootCmd.SetVersionTemplate("labctl {{.Version}}\n")
}

// Execute stamps the version and runs the root command. version comes from the
// build-time -X main.version ldflag (see cmd/labctl/main.go).
func Execute(version string) {
	SetVersion(version)
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&projectDir, "project-dir", "", "project root directory (auto-detected if not set)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "enable debug-level logging (config load, script exec, API calls)")

	rootCmd.AddCommand(learnCmd())
	rootCmd.AddCommand(challengeCmd())
	rootCmd.AddCommand(validateCmd())
}
