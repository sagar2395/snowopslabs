// SPDX-License-Identifier: Apache-2.0
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

// Config holds the resolved project configuration.
type Config struct {
	// Project root directory
	ProjectRoot string

	// Cluster/Runtime
	Profile     string
	ClusterName string
	HTTPPort    string
	HTTPSPort   string

	// Runtime-specific
	IngressClass        string
	StorageClass        string
	DomainSuffix        string
	RegistryType        string
	MonitoringNamespace string

	// Provider selections
	IngressProvider     string
	MetricsProvider     string
	LoggingProvider     string
	TracingProvider     string
	GitOpsProvider      string
	ChaosProvider       string
	PolicyProvider      string
	SecretsProvider     string
	MeshProvider        string
	DataProvider        string
	AutoscalingProvider string
	CostProvider        string

	// App defaults
	AppName         string
	HelmReleaseName string
	HelmValues      string

	// Pack registry (task 069). The community index is a static, optionally
	// signed JSON artifact; these point the CLI at it. Empty key => no signature
	// verification (community default).
	RegistryIndexURL string
	RegistryIndexKey string

	// ScriptEnv holds every key defined in the project's .env / runtime.env,
	// resolved with real environment variables taking precedence over file
	// values. Callers propagate it to child scripts and Make targets (see
	// internal/cli.root) so those subprocesses observe the same values the CLI
	// resolved. This replaces the old side effect where Load mutated the global
	// process environment via os.Setenv — which leaked one project's values into
	// the next Load and was not safe to call more than once.
	ScriptEnv map[string]string
}

// AppConfig holds per-app configuration from app.env.
type AppConfig struct {
	AppName        string
	BuildStrategy  string
	DeployStrategy string
	HelmRelease    string
	HelmValues     string
	Namespace      string
}

// Load reads the project configuration from .env and the profile's runtime.env.
//
// Load is pure with respect to the process environment: it never calls
// os.Setenv, so it can be called repeatedly (a long-running server, an
// in-process profile switch) or concurrently without one call's values leaking
// into the next. Resolution precedence for every key is: real environment
// variable (if set and non-empty) > .env > runtime.env > built-in default.
func Load(projectRoot string) (*Config, error) {
	if projectRoot == "" {
		var err error
		projectRoot, err = findProjectRoot()
		if err != nil {
			return nil, err
		}
	}

	cfg := &Config{
		ProjectRoot: projectRoot,
	}

	// Parse the env files into a local map instead of the global environment.
	// .env is merged first and wins over runtime.env (mergeEnvFile does not
	// overwrite keys already present), matching the previous load order.
	fileVals := map[string]string{}
	mergeEnvFile(fileVals, filepath.Join(projectRoot, ".env"))

	// The profile selects which runtime.env to load and may itself be set in
	// .env or the real environment.
	profile := resolveEnv(fileVals, "PROFILE", "k3d")

	// Validate that the profile directory exists.
	runtimeDir := filepath.Join(projectRoot, "runtimes", profile)
	if _, err := os.Stat(runtimeDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("runtime profile %q not found in runtimes/; available profiles: %s",
			profile, availableProfiles(projectRoot))
	}

	mergeEnvFile(fileVals, filepath.Join(runtimeDir, "runtime.env"))

	// Populate config from the resolved values.
	cfg.Profile = profile
	cfg.ClusterName = resolveEnv(fileVals, "CLUSTER_NAME", "snowops")
	cfg.HTTPPort = resolveEnv(fileVals, "HTTP_PORT", "80")
	cfg.HTTPSPort = resolveEnv(fileVals, "HTTPS_PORT", "443")

	cfg.IngressClass = resolveEnv(fileVals, "INGRESS_CLASS", "traefik")
	cfg.StorageClass = resolveEnv(fileVals, "STORAGE_CLASS", "local-path")
	cfg.DomainSuffix = resolveEnv(fileVals, "DOMAIN_SUFFIX", "k3d.local")
	cfg.RegistryType = resolveEnv(fileVals, "REGISTRY_TYPE", "k3d-import")
	cfg.MonitoringNamespace = resolveEnv(fileVals, "MONITORING_NAMESPACE", "monitoring")

	cfg.IngressProvider = resolveEnv(fileVals, "INGRESS_PROVIDER", "traefik")
	cfg.MetricsProvider = resolveEnv(fileVals, "METRICS_PROVIDER", "prometheus")
	cfg.LoggingProvider = resolveEnv(fileVals, "LOGGING_PROVIDER", "")
	cfg.TracingProvider = resolveEnv(fileVals, "TRACING_PROVIDER", "")
	cfg.GitOpsProvider = resolveEnv(fileVals, "GITOPS_PROVIDER", "")
	cfg.ChaosProvider = resolveEnv(fileVals, "CHAOS_PROVIDER", "")
	cfg.PolicyProvider = resolveEnv(fileVals, "POLICY_PROVIDER", "")
	cfg.SecretsProvider = resolveEnv(fileVals, "SECRETS_PROVIDER", "")
	cfg.MeshProvider = resolveEnv(fileVals, "MESH_PROVIDER", "")
	cfg.DataProvider = resolveEnv(fileVals, "DATA_PROVIDER", "")
	cfg.AutoscalingProvider = resolveEnv(fileVals, "AUTOSCALING_PROVIDER", "")
	cfg.CostProvider = resolveEnv(fileVals, "COST_PROVIDER", "")

	cfg.AppName = resolveEnv(fileVals, "APP_NAME", "go-api")
	cfg.HelmReleaseName = resolveEnv(fileVals, "HELM_RELEASE_NAME", "go-api")
	cfg.HelmValues = resolveEnv(fileVals, "HELM_VALUES", "values-dev.yaml")

	cfg.RegistryIndexURL = resolveEnv(fileVals, "PACK_REGISTRY_INDEX", "https://snowops.github.io/registry/index.json")
	cfg.RegistryIndexKey = resolveEnv(fileVals, "PACK_REGISTRY_KEY", "")

	// Expose the resolved value of every file-declared key so callers can pass
	// them to child processes (scripts, Make) explicitly — the propagation the
	// old os.Setenv side effect used to provide, now without the global mutation.
	cfg.ScriptEnv = make(map[string]string, len(fileVals))
	for k := range fileVals {
		cfg.ScriptEnv[k] = resolveEnv(fileVals, k, fileVals[k])
	}

	return cfg, nil
}

// LoadAppConfig reads app-specific config from apps/<name>/app.env.
func LoadAppConfig(projectRoot, appName string) (*AppConfig, error) {
	appDir := filepath.Join(projectRoot, "apps", appName)
	if _, err := os.Stat(appDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("app %q not found in apps/; available apps: %s",
			appName, availableApps(projectRoot))
	}
	appEnv := filepath.Join(appDir, "app.env")
	if _, err := os.Stat(appEnv); os.IsNotExist(err) {
		return nil, fmt.Errorf("app %q exists but has no app.env", appName)
	}

	v := viper.New()
	v.SetConfigFile(appEnv)
	v.SetConfigType("env")
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("reading app config: %w", err)
	}

	return &AppConfig{
		AppName:        v.GetString("APP_NAME"),
		BuildStrategy:  v.GetString("BUILD_STRATEGY"),
		DeployStrategy: v.GetString("DEPLOY_STRATEGY"),
		HelmRelease:    v.GetString("HELM_RELEASE_NAME"),
		HelmValues:     v.GetString("HELM_VALUES"),
		Namespace:      v.GetString("NAMESPACE"),
	}, nil
}

// ListApps returns a list of app names from the apps/ directory.
func ListApps(projectRoot string) ([]string, error) {
	appsDir := filepath.Join(projectRoot, "apps")
	entries, err := os.ReadDir(appsDir)
	if err != nil {
		return nil, fmt.Errorf("reading apps directory: %w", err)
	}

	var apps []string
	for _, e := range entries {
		if e.IsDir() {
			appEnv := filepath.Join(appsDir, e.Name(), "app.env")
			if _, err := os.Stat(appEnv); err == nil {
				apps = append(apps, e.Name())
			}
		}
	}
	return apps, nil
}

func findProjectRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	// Identify the repo root by user-facing content the CLI depends on at
	// runtime: scenarios/ (declarative content) plus runtimes/ (cluster
	// profiles). Both live at the content root, unaffected by the Go source
	// moving under src/. We key on these rather than Makefile so a make-free
	// checkout — the release binary dropped next to a git clone — is still
	// auto-detected. --project-dir overrides.
	for {
		if _, err := os.Stat(filepath.Join(dir, "scenarios")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "runtimes")); err == nil {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find project root (looked for scenarios/ + runtimes/)")
		}
		dir = parent
	}
}

// mergeEnvFile parses a KEY=VALUE env file into dst without touching the global
// process environment. Keys already present in dst are left untouched, so an
// earlier merge (e.g. .env) wins over a later one (runtime.env). A missing or
// unreadable file is silently ignored — both files are optional.
func mergeEnvFile(dst map[string]string, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := parseEnvLine(line)
		if !ok {
			continue
		}
		if _, exists := dst[key]; !exists {
			dst[key] = val
		}
	}
}

// parseEnvLine splits one "KEY=VALUE" line and normalises the value. ok is false
// for a line with no '='. The value is trimmed, unquoted if wrapped in a matching
// pair of single or double quotes, and — only when unquoted — has a trailing
// " # inline comment" stripped. Quoting therefore preserves a literal '#' and
// surrounding whitespace, matching the common .env convention.
func parseEnvLine(line string) (key, val string, ok bool) {
	parts := strings.SplitN(line, "=", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	key = strings.TrimSpace(parts[0])
	if key == "" {
		return "", "", false
	}
	return key, parseEnvValue(parts[1]), true
}

func parseEnvValue(raw string) string {
	v := strings.TrimSpace(raw)
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') {
		if i := strings.IndexByte(v[1:], v[0]); i >= 0 {
			// Content between the opening quote and its matching close; anything
			// after the closing quote (e.g. a comment) is discarded.
			return v[1 : 1+i]
		}
		// Unterminated quote: fall through and treat as an unquoted value.
	}
	if idx := strings.Index(v, " #"); idx >= 0 {
		v = strings.TrimSpace(v[:idx])
	}
	return v
}

// resolveEnv returns the value for key with real environment variables taking
// precedence over the parsed file values, and defaultVal when neither supplies a
// non-empty value. An empty string (from either source) is treated as unset, so
// a blank assignment falls back to the default — the previous behaviour.
func resolveEnv(fileVals map[string]string, key, defaultVal string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	if v, ok := fileVals[key]; ok && v != "" {
		return v
	}
	return defaultVal
}

// availableProfiles lists valid profile directory names under runtimes/.
func availableProfiles(projectRoot string) string {
	entries, err := os.ReadDir(filepath.Join(projectRoot, "runtimes"))
	if err != nil {
		return "(none)"
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return "(none)"
	}
	return strings.Join(names, ", ")
}

// availableApps lists app names that have an app.env under apps/.
func availableApps(projectRoot string) string {
	entries, err := os.ReadDir(filepath.Join(projectRoot, "apps"))
	if err != nil {
		return "(none)"
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			if _, err := os.Stat(filepath.Join(projectRoot, "apps", e.Name(), "app.env")); err == nil {
				names = append(names, e.Name())
			}
		}
	}
	if len(names) == 0 {
		return "(none)"
	}
	return strings.Join(names, ", ")
}
