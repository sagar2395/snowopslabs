// SPDX-License-Identifier: Apache-2.0

// Package cli implements the labctl command tree with cobra. Commands parse
// their input and call the services, engines and registries built in the root
// command's PersistentPreRunE.
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
	"github.com/sagar2395/snowopslabs/internal/workload"
)

var (
	projectDir string
	verbose    bool
	// appOverride binds scenarios and faults to a different application for one
	// command, without editing .env. Empty means "use APP_NAME".
	appOverride string

	cfg        *config.Config
	scriptExec *executor.Executor
	reg        *platform.Registry
	scenes     *scenario.Engine
	incEng     *incident.Engine
	svcReg     *services.Registry
	rtm        *runtime.Manager
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
		// Some commands, such as doctor, must work even when this setup
		// would fail.
		if skipSharedInit(cmd) {
			return nil
		}

		// Set the log level first, so the debug output below is shown.
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

		scriptExec = executor.New(cfg.ProjectRoot)
		// Pass every value from .env and runtime.env to child scripts and Make
		// targets; config.Load does not set them in the process environment.
		for k, v := range cfg.ScriptEnv {
			scriptExec.SetEnv(k, v)
		}
		// Set the core cluster settings explicitly, so scripts get them (or
		// their defaults) even without .env or runtime.env.
		scriptExec.SetEnv("CLUSTER_NAME", cfg.ClusterName)
		scriptExec.SetEnv("DOMAIN_SUFFIX", cfg.DomainSuffix)
		scriptExec.SetEnv("HTTP_PORT", cfg.HTTPPort)
		scriptExec.SetEnv("HTTPS_PORT", cfg.HTTPSPort)
		scriptExec.SetEnv("INGRESS_CLASS", cfg.IngressClass)
		scriptExec.SetEnv("INGRESS_PROVIDER", cfg.IngressProvider)
		scriptExec.SetEnv("STORAGE_CLASS", cfg.StorageClass)
		scriptExec.SetEnv("PROFILE", cfg.Profile)
		scriptExec.SetEnv("MONITORING_NAMESPACE", cfg.MonitoringNamespace)
		reg = platform.NewRegistryWithNamespace(cfg.ProjectRoot, cfg.MonitoringNamespace)
		// The workload binding both engines resolve {{.Workload*}} against.
		// APP_NAME selects it (ADR-0014); --app overrides it for one command,
		// and bindWorkload treats that deliberate choice more strictly.
		appName := cfg.AppName
		if pinsWorkload(cmd) {
			// Challenges and learning paths always use the default app; see
			// pinsWorkload.
			if appOverride != "" {
				return fmt.Errorf(
					"--app does not apply to %s: challenges and learning paths are graded against the default workload (%s) so par times and scores stay comparable.\n"+
						"To try your own application against the same fault, run the underlying scenario or incident directly:\n"+
						"  labctl incident inject <name> --app %s",
					cmd.CommandPath(), workload.DefaultApp, appOverride)
			}
			appName = workload.DefaultApp
		} else if appOverride != "" {
			appName = appOverride
		}
		if err := bindWorkload(appName, appOverride != ""); err != nil {
			return err
		}
		svcReg = services.NewRegistry(cfg.ProjectRoot)
		rtm = runtime.NewManager(cfg.ProjectRoot, cfg.ClusterName)
		slog.Debug("registries initialised", "runtimes", rtm.Names())
		return nil
	},
}

// bindWorkload binds the lab to the named app: it sets WORKLOAD_* on
// scriptExec and rebuilds the scenario and incident engines (ADR-0014).
// `labctl compare` calls it again for each app it measures.
//
// explicit is true when the user named the app (--app, or --apps in compare)
// rather than it coming from APP_NAME. For an explicit name, a missing or
// malformed app.env is an error. Otherwise the defaults are used, so other
// commands still work; `labctl app verify` reports the problem.
func bindWorkload(appName string, explicit bool) error {
	bound := workload.Default(appName)
	var boundContract workload.Contract
	appCfg, appErr := config.LoadAppConfig(cfg.ProjectRoot, appName)
	switch {
	case appErr != nil && explicit:
		return fmt.Errorf("app %s: %w", appName, appErr)
	case appErr != nil:
		slog.Debug("workload binding fell back to defaults", "app", appName, "err", appErr)
	default:
		bound = appCfg.Workload()
		boundContract = appCfg.Contract
	}
	slog.Debug("workload bound", "app", bound.Name, "namespace", bound.Namespace,
		"port", bound.Port, "capabilities", boundContract.Capabilities)

	// Component and fault scripts read the bound app from these.
	scriptExec.SetEnv("WORKLOAD_NAME", bound.Name)
	scriptExec.SetEnv("WORKLOAD_NAMESPACE", bound.Namespace)
	scriptExec.SetEnv("WORKLOAD_PORT", bound.Port)
	scriptExec.SetEnv("WORKLOAD_METRIC", bound.Metric)

	scenes = scenario.NewEngine(cfg.ProjectRoot, cfg.DomainSuffix, cfg.Profile)
	scenes.MonitoringNamespace = cfg.MonitoringNamespace
	scenes.IngressClass = cfg.IngressClass
	scenes.Workload = bound
	scenes.Contract = boundContract

	incEng = incident.NewEngine(cfg.ProjectRoot, cfg.DomainSuffix)
	incEng.MonitoringNamespace = cfg.MonitoringNamespace
	incEng.Workload = bound
	incEng.AlertmanagerURL = os.Getenv("ALERTMANAGER_URL")
	if incEng.AlertmanagerURL == "" {
		incEng.AlertmanagerURL = "http://alertmanager." + cfg.DomainSuffix
	}
	return nil
}

// rebindTo binds the lab to an app recorded in state: the app a scenario was
// activated against, or a fault injected into. It uses bindWorkload so the
// scripts' WORKLOAD_* change too. It does nothing when app is empty or already
// bound.
func rebindTo(app string) error {
	if app == "" || app == scenes.Workload.Name {
		return nil
	}
	return bindWorkload(app, true)
}

// pinsWorkload reports whether a command must run against the default workload
// regardless of --app or APP_NAME.
//
// Challenges and learning paths are scored and timed against the default app,
// so results stay comparable. To try another app, run the underlying scenario
// or incident with --app.
func pinsWorkload(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		switch c.Name() {
		case "challenge", "learn":
			return true
		}
	}
	return false
}

// skipSharedInit reports whether a command runs without the setup in
// PersistentPreRunE (config, scriptExec and the engines). These commands
// diagnose a broken environment (doctor), need no config (help, completion,
// validate), or only read the run store (runs list|logs|cancel).
//
// "list", "logs" and "cancel" are skipped only under `runs`: other commands
// with those names, such as `scenario list`, need the engines.
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

// SetVersion sets the version `labctl --version` prints. An empty v becomes
// "dev".
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
	rootCmd.PersistentFlags().StringVar(&appOverride, "app", "", "application to bind scenarios and faults to for this command (default: APP_NAME)")

	rootCmd.AddCommand(learnCmd())
	rootCmd.AddCommand(challengeCmd())
	rootCmd.AddCommand(validateCmd())
}
