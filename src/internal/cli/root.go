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

		scriptExec = executor.New(cfg.ProjectRoot)
		// Propagate every value declared in .env / runtime.env so child scripts
		// and Make targets see them. config.Load no longer mutates the process
		// environment, so this explicit hand-off replaces the old os.Setenv side
		// effect (e.g. METRICS_PROVIDER, REGISTRY_TYPE consumed by make targets).
		for k, v := range cfg.ScriptEnv {
			scriptExec.SetEnv(k, v)
		}
		// The core cluster knobs are set explicitly too, so they reach scripts
		// even when a checkout has no .env / runtime.env (defaults still apply).
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
			// Challenges and learning paths are graded against a fixed workload,
			// so they ignore the binding entirely — see pinsWorkload.
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

// bindWorkload resolves the app name to a workload binding and rebuilds
// everything that reads it: the script environment, the scenario engine and the
// incident engine. Binding is a step, not a one-off during start-up, because
// `labctl compare` runs the same scenario against several apps in turn and must
// rebind between them (ADR-0014).
//
// explicit says the name came from a deliberate choice (--app, or --apps in a
// comparison) rather than the ambient APP_NAME. A missing or malformed app.env
// must not stop unrelated commands from running, so an ambient name falls back
// to the conventional defaults and `labctl app verify` is where the problem is
// reported; an explicit one is a usage error.
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

	// Component and fault scripts act on the bound workload, so they need it
	// in their environment the same way they get DOMAIN_SUFFIX.
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

// rebindTo re-binds the whole lab to an app recorded in state — the workload a
// scenario was activated against, or the one a fault was injected into.
//
// It must go through bindWorkload rather than setting the engine field alone:
// the fault and component scripts read WORKLOAD_* from the executor's
// environment, so a binding that stopped at the engine graded one app while the
// scripts acted on another. A no-op when nothing was recorded (state written by
// an older build) or the app is already bound.
func rebindTo(app string) error {
	if app == "" || app == scenes.Workload.Name {
		return nil
	}
	return bindWorkload(app, true)
}

// pinsWorkload reports whether a command must run against the default workload
// regardless of --app or APP_NAME.
//
// Challenges and learning paths compose scenarios and incidents by reference, so
// the binding would otherwise flow straight through into them. They are scored
// and timed: a par time is calibrated against one workload, and a leaderboard
// comparing runs on different applications measures the language rather than the
// engineer. Nothing is lost by pinning them — a user who wants to see their own
// app under the same fault runs that scenario or incident directly with --app.
func pinsWorkload(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		switch c.Name() {
		case "challenge", "learn":
			return true
		}
	}
	return false
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
	rootCmd.PersistentFlags().StringVar(&appOverride, "app", "", "application to bind scenarios and faults to for this command (default: APP_NAME)")

	rootCmd.AddCommand(learnCmd())
	rootCmd.AddCommand(challengeCmd())
	rootCmd.AddCommand(validateCmd())
}
