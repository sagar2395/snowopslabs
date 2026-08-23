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

// Load reads the project configuration from .env, versions.env, and runtime.env.
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

	// Load .env (optional)
	loadEnvFile(filepath.Join(projectRoot, ".env"))

	// Load runtime.env based on profile
	profile := getEnvOrDefault("PROFILE", "k3d")

	// Validate that the profile directory exists.
	runtimeDir := filepath.Join(projectRoot, "runtimes", profile)
	if _, err := os.Stat(runtimeDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("runtime profile %q not found in runtimes/; available profiles: %s",
			profile, availableProfiles(projectRoot))
	}

	runtimeEnv := filepath.Join(runtimeDir, "runtime.env")
	loadEnvFile(runtimeEnv)

	// Populate config from environment
	cfg.Profile = profile
	cfg.ClusterName = getEnvOrDefault("CLUSTER_NAME", "snowops")
	cfg.HTTPPort = getEnvOrDefault("HTTP_PORT", "80")
	cfg.HTTPSPort = getEnvOrDefault("HTTPS_PORT", "443")

	cfg.IngressClass = getEnvOrDefault("INGRESS_CLASS", "traefik")
	cfg.StorageClass = getEnvOrDefault("STORAGE_CLASS", "local-path")
	cfg.DomainSuffix = getEnvOrDefault("DOMAIN_SUFFIX", "k3d.local")
	cfg.RegistryType = getEnvOrDefault("REGISTRY_TYPE", "k3d-import")
	cfg.MonitoringNamespace = getEnvOrDefault("MONITORING_NAMESPACE", "monitoring")

	cfg.IngressProvider = getEnvOrDefault("INGRESS_PROVIDER", "traefik")
	cfg.MetricsProvider = getEnvOrDefault("METRICS_PROVIDER", "prometheus")
	cfg.LoggingProvider = getEnvOrDefault("LOGGING_PROVIDER", "")
	cfg.TracingProvider = getEnvOrDefault("TRACING_PROVIDER", "")
	cfg.GitOpsProvider = getEnvOrDefault("GITOPS_PROVIDER", "")
	cfg.ChaosProvider = getEnvOrDefault("CHAOS_PROVIDER", "")
	cfg.PolicyProvider = getEnvOrDefault("POLICY_PROVIDER", "")
	cfg.SecretsProvider = getEnvOrDefault("SECRETS_PROVIDER", "")
	cfg.MeshProvider = getEnvOrDefault("MESH_PROVIDER", "")
	cfg.DataProvider = getEnvOrDefault("DATA_PROVIDER", "")
	cfg.AutoscalingProvider = getEnvOrDefault("AUTOSCALING_PROVIDER", "")
	cfg.CostProvider = getEnvOrDefault("COST_PROVIDER", "")

	cfg.AppName = getEnvOrDefault("APP_NAME", "go-api")
	cfg.HelmReleaseName = getEnvOrDefault("HELM_RELEASE_NAME", "go-api")
	cfg.HelmValues = getEnvOrDefault("HELM_VALUES", "values-dev.yaml")

	cfg.RegistryIndexURL = getEnvOrDefault("PACK_REGISTRY_INDEX", "https://snowops.github.io/registry/index.json")
	cfg.RegistryIndexKey = getEnvOrDefault("PACK_REGISTRY_KEY", "")

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

	// Identify the repo root by content the CLI actually depends on at runtime:
	// engine/ (build/deploy scripts) plus scenarios/ (declarative content). We key
	// on these rather than Makefile so a make-free checkout — the release binary
	// dropped next to a git clone — is still auto-detected. --project-dir overrides.
	for {
		if _, err := os.Stat(filepath.Join(dir, "engine")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "scenarios")); err == nil {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find project root (looked for engine/ + scenarios/)")
		}
		dir = parent
	}
}

func loadEnvFile(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		// Strip inline comments
		if idx := strings.Index(val, " #"); idx >= 0 {
			val = strings.TrimSpace(val[:idx])
		}
		// Don't override existing env vars
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, val)
		}
	}
}

func getEnvOrDefault(key, defaultVal string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
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
