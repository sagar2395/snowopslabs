// SPDX-License-Identifier: Apache-2.0

// Package config finds the project root and loads the lab configuration from
// .env and the runtime profile's runtime.env, and per-app settings from
// apps/<name>/app.env.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"

	"github.com/sagar2395/snowopslabs/internal/workload"
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

	// Pack registry. The community index is a static, optionally
	// signed JSON artifact; these point the CLI at it. Empty key => no signature
	// verification (community default).
	RegistryIndexURL string
	RegistryIndexKey string

	// ScriptEnv holds every key defined in .env and runtime.env, with real
	// environment variables taking precedence over the files. Callers pass it
	// to child scripts and Make targets so they see the same values the CLI
	// resolved.
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
	// Contract is the runtime interface the app declares — the port, probe paths
	// and capabilities a scenario binds to instead of naming the app (ADR-0014).
	Contract workload.Contract
}

// Workload returns the binding for this app, with the port and metric taken
// from the app's declared contract.
func (a *AppConfig) Workload() workload.Workload {
	return a.Contract.Workload(a.AppName, a.Namespace)
}

// Load reads the project configuration from .env and the profile's runtime.env.
//
// Load never modifies the process environment, so it is safe to call more
// than once or concurrently. For every key the first non-empty value wins, in
// this order: real environment variable, .env, runtime.env, built-in default.
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

	// .env is merged first, and mergeEnvFile never overwrites a key, so .env
	// wins over runtime.env.
	fileVals := map[string]string{}
	mergeEnvFile(fileVals, filepath.Join(projectRoot, ".env"))

	// The profile selects which runtime.env to load and may itself be set in
	// .env or the real environment.
	profile := resolveEnv(fileVals, "PROFILE", "k3d")

	runtimeDir := filepath.Join(projectRoot, "runtimes", profile)
	if _, err := os.Stat(runtimeDir); errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("runtime profile %q not found in runtimes/; available profiles: %s",
			profile, availableProfiles(projectRoot))
	}

	mergeEnvFile(fileVals, filepath.Join(runtimeDir, "runtime.env"))

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

	cfg.AppName = resolveEnv(fileVals, "APP_NAME", workload.DefaultApp)
	cfg.HelmReleaseName = resolveEnv(fileVals, "HELM_RELEASE_NAME", "go-api")
	cfg.HelmValues = resolveEnv(fileVals, "HELM_VALUES", "values-dev.yaml")

	cfg.RegistryIndexURL = resolveEnv(fileVals, "PACK_REGISTRY_INDEX", "https://snowops.github.io/registry/index.json")
	cfg.RegistryIndexKey = resolveEnv(fileVals, "PACK_REGISTRY_KEY", "")

	// Resolve every key the files declare, so callers can pass them on to
	// child processes.
	cfg.ScriptEnv = make(map[string]string, len(fileVals))
	for k := range fileVals {
		cfg.ScriptEnv[k] = resolveEnv(fileVals, k, fileVals[k])
	}

	return cfg, nil
}

// LoadAppConfig reads app-specific config from apps/<name>/app.env.
func LoadAppConfig(projectRoot, appName string) (*AppConfig, error) {
	appDir := filepath.Join(projectRoot, "apps", appName)
	if _, err := os.Stat(appDir); errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("app %q not found in apps/; available apps: %s",
			appName, availableApps(projectRoot))
	}
	appEnv := filepath.Join(appDir, "app.env")
	if _, err := os.Stat(appEnv); errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("app %q exists but has no app.env", appName)
	}

	v := viper.New()
	v.SetConfigFile(appEnv)
	v.SetConfigType("env")
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("reading app config: %w", err)
	}

	contractVals := make(map[string]string, 6)
	for _, k := range []string{
		workload.KeyPort, workload.KeyHealthPath, workload.KeyReadyPath,
		workload.KeyMetricsPath, workload.KeyRequestMetric, workload.KeyCapabilities,
	} {
		contractVals[k] = v.GetString(k)
	}
	contract, err := workload.ParseContract(contractVals)
	if err != nil {
		return nil, fmt.Errorf("app %q: %s: %w", appName, appEnv, err)
	}

	return &AppConfig{
		AppName:        v.GetString("APP_NAME"),
		BuildStrategy:  v.GetString("BUILD_STRATEGY"),
		DeployStrategy: v.GetString("DEPLOY_STRATEGY"),
		HelmRelease:    v.GetString("HELM_RELEASE_NAME"),
		HelmValues:     v.GetString("HELM_VALUES"),
		Namespace:      v.GetString("NAMESPACE"),
		Contract:       contract,
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

	// The project root is the nearest directory with both scenarios/ and
	// runtimes/. Keying on content rather than a Makefile also finds a checkout
	// used with a downloaded binary. --project-dir overrides this search.
	for {
		if _, err := os.Stat(filepath.Join(dir, "scenarios")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "runtimes")); err == nil {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("could not find project root (looked for scenarios/ + runtimes/)")
		}
		dir = parent
	}
}

// mergeEnvFile parses a KEY=VALUE file into dst. Keys already in dst are kept,
// so the file merged first wins. A missing or unreadable file is ignored,
// because both env files are optional.
func mergeEnvFile(dst map[string]string, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for line := range strings.SplitSeq(string(data), "\n") {
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

// resolveEnv returns the real environment variable for key if it is non-empty,
// else the file value if non-empty, else defaultVal. A blank assignment
// therefore falls back to the default.
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
