// SPDX-License-Identifier: Apache-2.0

// Package cli implements the labctl command tree with cobra. Commands parse
// their input and call the services, engines and registries built in the root
// command's PersistentPreRunE.
package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/sagar2395/snowopslabs/internal/config"
	"github.com/sagar2395/snowopslabs/internal/display"
	"github.com/sagar2395/snowopslabs/internal/executor"
	"github.com/sagar2395/snowopslabs/internal/incident"
	"github.com/sagar2395/snowopslabs/internal/labcheck"
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
	Short: "SnowOps Labs - platform engineering simulator control plane",
	Long:  `labctl is the CLI, web UI, and API for SnowOps Labs, a Kubernetes platform engineering simulator.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Arguments and flags are valid by the time this runs, so an error
		// from here on is a runtime failure (a helm error, a failing check)
		// and is printed without the command's usage text.
		cmd.SilenceUsage = true

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
		if msg := versionSkew(cmd.Root().Version, config.LabVersion(cfg.ProjectRoot), cfg.ProjectRoot); msg != "" {
			fmt.Fprintln(cmd.ErrOrStderr(), msg)
		}
		legacyState := filepath.Join(cfg.ProjectRoot, ".labctl")
		switch moved, err := config.MigrateState(legacyState, cfg.StateDir); {
		case err != nil:
			fmt.Fprintf(cmd.ErrOrStderr(), "Warning: %v\n", err)
		case moved:
			fmt.Fprintf(cmd.ErrOrStderr(), "Moved the lab state from %s to %s.\n",
				display.Text(legacyState, cfg.ProjectRoot, cfg.StateDir, cliCurrentDir()),
				display.Text(cfg.StateDir, cfg.ProjectRoot, cfg.StateDir, cliCurrentDir()))
		}

		scriptExec = executor.New(cfg.ProjectRoot)
		// Pass every value from .env and runtime.env to child scripts and Make
		// targets; config.Load does not set them in the process environment.
		for k, v := range cfg.ScriptEnv {
			scriptExec.SetEnv(k, v)
		}
		// Set the core cluster settings explicitly, so scripts get them (or
		// their defaults) even without .env or runtime.env.
		scriptExec.SetEnv("CLUSTER_NAME", cfg.ClusterName)
		scriptExec.SetEnv("PROJECT_ROOT", cfg.ProjectRoot)
		scriptExec.SetEnv("LAB_STATE_DIR", cfg.StateDir)
		scriptExec.SetEnv("DOMAIN_SUFFIX", cfg.DomainSuffix)
		// Scripts build lab URLs from this so a fallback ingress port is kept.
		scriptExec.SetEnv("INGRESS_URL_SUFFIX", cfg.IngressURLSuffix())
		scriptExec.SetEnv("HTTP_PORT", cfg.HTTPPort)
		scriptExec.SetEnv("HTTPS_PORT", cfg.HTTPSPort)
		scriptExec.SetEnv("LAB_CPUS", cfg.LabCPUs)
		scriptExec.SetEnv("LAB_MEMORY", cfg.LabMemory)
		scriptExec.SetEnv("INGRESS_CLASS", cfg.IngressClass)
		scriptExec.SetEnv("INGRESS_PROVIDER", cfg.IngressProvider)
		scriptExec.SetEnv("STORAGE_CLASS", cfg.StorageClass)
		scriptExec.SetEnv("PROFILE", cfg.Profile)
		scriptExec.SetEnv("MONITORING_NAMESPACE", cfg.MonitoringNamespace)
		// --verbose keeps the output scripts trim by default, such as the
		// client-go discovery errors platform/_lib/helm.sh filters.
		if verbose {
			scriptExec.SetEnv("LABCTL_VERBOSE", "1")
		}
		reg = platform.NewRegistryWithNamespace(cfg.ProjectRoot, cfg.MonitoringNamespace)
		reg.StateRoot = cfg.StateDir
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
	scenes.StateRoot = cfg.StateDir
	scenes.MonitoringNamespace = cfg.MonitoringNamespace
	scenes.IngressClass = cfg.IngressClass
	scenes.IngressURLSuffix = cfg.IngressURLSuffix()
	scenes.Workload = bound
	scenes.Contract = boundContract

	incEng = incident.NewEngine(cfg.ProjectRoot, cfg.DomainSuffix)
	incEng.StateRoot = cfg.StateDir
	incEng.IngressURLSuffix = cfg.IngressURLSuffix()
	incEng.MonitoringNamespace = cfg.MonitoringNamespace
	incEng.Workload = bound
	incEng.AlertmanagerURL = labcheck.AlertmanagerURL(cfg)
	attachAdmissionGate()
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
// validate), or operate directly on the run store.
//
// Run-store commands are skipped only under `runs`: other commands
// with those names, such as `scenario list`, need the engines.
func skipSharedInit(cmd *cobra.Command) bool {
	switch cmd.Name() {
	case "completion", "help", "doctor", "runs", "validate":
		return true
	case "list", "logs", "cancel", "recover":
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
	if err := addUserBinToPath(); err != nil {
		slog.Warn("could not add ~/.local/bin to PATH; tools installed there may not be found", "err", err)
	}
	// Ctrl-C and SIGTERM cancel the command's context instead of killing the
	// process, so the run engine stops its scripts' process groups and records
	// the runs as cancelled.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := rootCmd.ExecuteContext(ctx)
	stop()
	if err != nil {
		os.Exit(1)
	}
}

// versionSkew warns when the labctl binary and the lab at root (its
// LAB_VERSION) belong to different releases, and says how to bring them in
// step. It says nothing for a development build of labctl, or for a lab
// without LAB_VERSION.
func versionSkew(binary, lab, root string) string {
	binary = strings.TrimPrefix(binary, "v")
	lab = strings.TrimPrefix(lab, "v")
	if lab == "" || binary == "dev" || strings.ContainsAny(binary, "-+") || binary == lab {
		return ""
	}
	if strings.Contains(lab, "-") {
		return fmt.Sprintf("Warning: labctl is %s but %s is development content (%s). Use a release:\n"+
			"  cd %s && git switch stable && ./install.sh\n"+
			"or build labctl from this checkout: make cli-build", binary, root, lab, root)
	}
	return fmt.Sprintf("Warning: labctl is %s but the lab in %s is %s. Install the labctl that matches it:\n"+
		"  cd %s && ./install.sh", binary, root, lab, root)
}

// addUserBinToPath puts ~/.local/bin, where setup-tools installs kubectl, helm
// and k3d on Linux and WSL, at the front of labctl's own PATH, so a tool it has
// just installed works before the user's shell has that directory. The user's
// PATH is kept in SNOWOPS_ORIGINAL_PATH so setup-tools can tell them to add it.
// Without a home directory there is nothing to add.
func addUserBinToPath() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil //nolint:nilerr // no home directory means no ~/.local/bin to add
	}
	path := os.Getenv("PATH")
	if err := os.Setenv("SNOWOPS_ORIGINAL_PATH", path); err != nil {
		return fmt.Errorf("setting SNOWOPS_ORIGINAL_PATH: %w", err)
	}
	if err := os.Setenv("PATH", withUserBin(path, filepath.Join(home, ".local", "bin"))); err != nil {
		return fmt.Errorf("setting PATH: %w", err)
	}
	return nil
}

// withUserBin prepends dir to path unless it is already there.
func withUserBin(path, dir string) string {
	if slices.Contains(filepath.SplitList(path), dir) {
		return path
	}
	if path == "" {
		return dir
	}
	return dir + string(os.PathListSeparator) + path
}

func init() {
	rootCmd.PersistentFlags().StringVar(&projectDir, "project-dir", "", "project root directory (auto-detected if not set)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "enable debug-level logging (config load, script exec, API calls)")
	rootCmd.PersistentFlags().StringVar(&appOverride, "app", "", "application to bind scenarios and faults to for this command (default: APP_NAME)")

	rootCmd.AddCommand(learnCmd())
	rootCmd.AddCommand(challengeCmd())
	rootCmd.AddCommand(validateCmd())
}
