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

	"github.com/snowops-oss/snowopslabs/internal/workload"
)

// Config holds the resolved project configuration.
type Config struct {
	// Project root directory
	ProjectRoot string

	// StateDir holds the lab state of this cluster; see StateDir.
	StateDir string

	// Cluster/Runtime
	Profile     string
	ClusterName string
	HTTPPort    string
	HTTPSPort   string
	// LabCPUs and LabMemory size the Docker VM labctl starts (colima). They
	// never shrink one the user already runs.
	LabCPUs   string
	LabMemory string
	// Agents is the number of agent nodes the cluster starts with.
	Agents string

	// Runtime-specific
	IngressClass        string
	StorageClass        string
	DomainSuffix        string
	RegistryType        string
	MonitoringNamespace string

	// DomainSuffixPinned reports that the user set DOMAIN_SUFFIX in the
	// environment or .env, so labctl never moves the lab to another suffix.
	DomainSuffixPinned bool

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
	// Contract is the runtime interface the app declares - the port, probe paths
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
	projectRoot, err := FindLab(projectRoot)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		ProjectRoot: projectRoot,
	}

	// .env is merged first, and mergeEnvFile never overwrites a key that has a
	// value, so .env wins over runtime.env.
	fileVals := map[string]string{}
	mergeEnvFile(fileVals, filepath.Join(projectRoot, ".env"))
	cfg.DomainSuffixPinned = resolveEnv(fileVals, "DOMAIN_SUFFIX", "") != ""

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
	stateDir, err := StateDir(cfg.ClusterName)
	if err != nil {
		return nil, fmt.Errorf("locating the lab state directory: %w", err)
	}
	cfg.StateDir = stateDir

	// The runtime records how this machine reaches the cluster's ingress: the
	// ports it bound, which differ from the configured ones when those are
	// busy, and the domain suffix its hostnames use, which a lab keeps for its
	// lifetime. The record overrides .env; only a real environment variable
	// overrides the record.
	clusterVals := map[string]string{}
	if path, err := ClusterStateFile(cfg.ClusterName); err == nil {
		mergeEnvFile(clusterVals, path)
	}
	for _, k := range []string{"HTTP_PORT", "HTTPS_PORT", "DOMAIN_SUFFIX"} {
		if v := clusterVals[k]; v != "" {
			fileVals[k] = v
		}
	}
	cfg.HTTPPort = resolveEnv(fileVals, "HTTP_PORT", "80")
	cfg.HTTPSPort = resolveEnv(fileVals, "HTTPS_PORT", "443")
	cfg.LabCPUs = resolveEnv(fileVals, "LAB_CPUS", "2")
	cfg.LabMemory = resolveEnv(fileVals, "LAB_MEMORY", "4")
	cfg.Agents = resolveEnv(fileVals, "AGENTS", "1")

	cfg.StorageClass = resolveEnv(fileVals, "STORAGE_CLASS", "local-path")
	// Every browser resolves *.localhost to this machine, and the cluster name
	// keeps two labs' hostnames (and so their cookies) apart.
	cfg.DomainSuffix = resolveEnv(fileVals, "DOMAIN_SUFFIX", cfg.ClusterName+".localhost")
	cfg.RegistryType = resolveEnv(fileVals, "REGISTRY_TYPE", "k3d-import")
	cfg.MonitoringNamespace = resolveEnv(fileVals, "MONITORING_NAMESPACE", "monitoring")

	cfg.IngressProvider = resolveEnv(fileVals, "INGRESS_PROVIDER", "traefik")
	// Each provider installs an IngressClass of its own name, so an Ingress
	// with any other class is never routed.
	cfg.IngressClass = resolveEnv(fileVals, "INGRESS_CLASS", cfg.IngressProvider)
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

	data, err := os.ReadFile(appEnv)
	if err != nil {
		return nil, fmt.Errorf("reading app config: %w", err)
	}
	vals := make(map[string]string, 16)
	parseEnvData(vals, data)

	contractVals := make(map[string]string, 6)
	for _, k := range []string{
		workload.KeyPort, workload.KeyHealthPath, workload.KeyReadyPath,
		workload.KeyMetricsPath, workload.KeyRequestMetric, workload.KeyCapabilities,
	} {
		contractVals[k] = vals[k]
	}
	contract, err := workload.ParseContract(contractVals)
	if err != nil {
		return nil, fmt.Errorf("app %q: %s: %w", appName, appEnv, err)
	}

	return &AppConfig{
		AppName:        vals["APP_NAME"],
		BuildStrategy:  vals["BUILD_STRATEGY"],
		DeployStrategy: vals["DEPLOY_STRATEGY"],
		HelmRelease:    vals["HELM_RELEASE_NAME"],
		HelmValues:     vals["HELM_VALUES"],
		Namespace:      vals["NAMESPACE"],
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

// ErrNoLab means no lab was found: the working directory is not inside a
// clone, SNOWOPS_LAB_DIR is unset and install.sh has recorded none.
var ErrNoLab = errors.New("could not find your lab (a snowopslabs clone, with scenarios/ and runtimes/).\n" +
	"Clone it and run its installer, which also lets labctl find it from any directory:\n" +
	"  git clone --branch stable https://github.com/snowops-oss/snowopslabs.git\n" +
	"  cd snowopslabs && ./install.sh\n" +
	"Or run labctl inside a clone, set SNOWOPS_LAB_DIR=<clone>, or pass --project-dir <clone>")

// FindLab returns projectDir when it is set, and otherwise locates the lab as
// findProjectRoot does.
func FindLab(projectDir string) (string, error) {
	if projectDir != "" {
		return projectDir, nil
	}
	return findProjectRoot()
}

// findProjectRoot locates the lab: the nearest clone at or above the working
// directory, else SNOWOPS_LAB_DIR, else the clone install.sh recorded in
// <Home>/lab-dir. --project-dir overrides all three.
func findProjectRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("reading the working directory: %w", err)
	}
	for {
		if isContentRoot(dir) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if env := os.Getenv("SNOWOPS_LAB_DIR"); env != "" {
		if !isContentRoot(env) {
			return "", fmt.Errorf("SNOWOPS_LAB_DIR=%s is not a snowopslabs clone (it has no scenarios/ and runtimes/); point it at your clone or unset it", env)
		}
		return env, nil
	}
	return recordedLab()
}

// recordedLab is the clone install.sh last recorded in <Home>/lab-dir.
func recordedLab() (string, error) {
	file, err := LabDirFile()
	if err != nil {
		return "", ErrNoLab
	}
	data, err := os.ReadFile(file) //nolint:gosec // file is labctl's own record under <Home>
	if err != nil {
		return "", ErrNoLab
	}
	dir := strings.TrimSpace(string(data))
	if !isContentRoot(dir) {
		return "", fmt.Errorf("the lab recorded in %s (%s) is gone.\n"+
			"cd into your snowopslabs clone and run ./install.sh to record it again", file, dir)
	}
	return dir, nil
}

// isContentRoot reports whether dir holds the lab content.
func isContentRoot(dir string) bool {
	for _, sub := range []string{"scenarios", "runtimes"} {
		//nolint:gosec // dir is the user's own lab location (working directory, SNOWOPS_LAB_DIR or their record)
		if info, err := os.Stat(filepath.Join(dir, sub)); err != nil || !info.IsDir() {
			return false
		}
	}
	return true
}

// LabDirFile is where install.sh records the clone labctl uses when run
// outside one: <Home>/lab-dir.
func LabDirFile() (string, error) {
	home, err := Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "lab-dir"), nil
}

// LabVersion is the release the lab at root belongs to, from its committed
// LAB_VERSION file, or "" when the file is missing.
func LabVersion(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "LAB_VERSION")) //nolint:gosec // root is the resolved lab directory
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// StateDir is where labctl keeps the lab state of one cluster (active
// scenarios and faults, platform intent, history, progress, snapshots):
// <Home>/state/<cluster>. It sits outside the clone, so every clone on the
// machine sees the same state for the one cluster, and re-cloning loses none.
func StateDir(cluster string) (string, error) {
	home, err := Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "state", cluster), nil
}

// MigrateState moves lab state from legacy (a clone's .labctl directory) to
// target once, and reports whether it moved anything. When both exist it
// moves nothing and returns an error naming them, so the user can merge.
func MigrateState(legacy, target string) (bool, error) {
	if _, err := os.Stat(legacy); err != nil {
		return false, nil //nolint:nilerr // no legacy state is the usual case
	}
	if _, err := os.Stat(target); err == nil {
		return false, fmt.Errorf("lab state is in both %s and %s; labctl uses %s, so merge what you need from %s and delete it",
			legacy, target, target, legacy)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return false, fmt.Errorf("creating %s: %w", filepath.Dir(target), err)
	}
	if err := os.Rename(legacy, target); err != nil {
		return false, fmt.Errorf("moving lab state from %s to %s: %w; move it by hand: mv %s %s",
			legacy, target, err, legacy, target)
	}
	return true, nil
}

// mergeEnvFile parses a KEY=VALUE file into dst. Keys already in dst with a
// value are kept, so the file merged first wins. A missing or unreadable file
// is ignored, because both env files are optional.
func mergeEnvFile(dst map[string]string, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	parseEnvData(dst, data)
}

// parseEnvData parses dotenv-style content into dst. The first non-empty value
// for a key wins; a blank one such as "KEY=" leaves the key open, so a later
// line or file can still set it.
func parseEnvData(dst map[string]string, data []byte) {
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := parseEnvLine(line)
		if !ok {
			continue
		}
		if dst[key] == "" {
			dst[key] = val
		}
	}
}

// parseEnvLine splits one "KEY=VALUE" line and normalises the value. ok is false
// for a line with no '='. The value is trimmed, unquoted if wrapped in a matching
// pair of single or double quotes, and - only when unquoted - has a trailing
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

// Home is labctl's own state directory: $SNOWOPS_HOME, or ~/.snowops.
func Home() (string, error) {
	if home := os.Getenv("SNOWOPS_HOME"); home != "" {
		return home, nil
	}
	dir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating home directory (set SNOWOPS_HOME to override): %w", err)
	}
	return filepath.Join(dir, ".snowops"), nil
}

// ClusterStateFile is where a local runtime records what it created for a
// cluster, such as the ingress ports it bound: <Home>/clusters/<name>.env.
// runtimes/_lib/docker.sh writes it and the runtime's down.sh removes it.
func ClusterStateFile(clusterName string) (string, error) {
	home, err := Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "clusters", clusterName+".env"), nil
}

// ResolvesLocally reports whether lab hostnames resolve to this machine without
// hosts-file entries: every browser and resolver maps *.localhost to loopback.
func (c *Config) ResolvesLocally() bool {
	return c.DomainSuffix == "localhost" || strings.HasSuffix(c.DomainSuffix, ".localhost")
}

// LocalIngress reports whether lab hostnames are served on this machine's
// loopback (k3d, kind) rather than by a real ingress (incluster).
func (c *Config) LocalIngress() bool { return c.Profile != "incluster" }

// IngressURLSuffix is what follows "<name>." in a lab URL: the domain suffix,
// plus the ingress port when it is not 80 (e.g. "snowops.localhost:8080").
func (c *Config) IngressURLSuffix() string {
	if c.HTTPPort == "" || c.HTTPPort == "80" {
		return c.DomainSuffix
	}
	return c.DomainSuffix + ":" + c.HTTPPort
}

// IngressURL is the browser URL for a lab hostname, e.g. "grafana" →
// "http://grafana.snowops.localhost" or "http://grafana.snowops.localhost:8080".
func (c *Config) IngressURL(name string) string {
	return "http://" + name + "." + c.IngressURLSuffix()
}

// availableProfiles lists valid profile directory names under runtimes/.
func availableProfiles(projectRoot string) string {
	entries, err := os.ReadDir(filepath.Join(projectRoot, "runtimes"))
	if err != nil {
		return "(none)"
	}
	var names []string
	for _, e := range entries {
		// runtimes/_lib holds shared helpers, not a profile.
		if e.IsDir() && !strings.HasPrefix(e.Name(), "_") {
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
