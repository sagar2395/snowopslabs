// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snowops-oss/snowopslabs/internal/workload"
)

// findProjectRoot must find a checkout that has no Makefile, by looking for
// scenarios/ and runtimes/.
func TestFindProjectRoot_NoMakefile(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"scenarios", "runtimes"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)

	got, err := findProjectRoot()
	if err != nil {
		t.Fatalf("findProjectRoot (no Makefile): %v", err)
	}
	// t.TempDir under /var is a symlink to /private/var on macOS; compare resolved.
	gotResolved, _ := filepath.EvalSymlinks(got)
	wantResolved, _ := filepath.EvalSymlinks(root)
	if gotResolved != wantResolved {
		t.Errorf("findProjectRoot = %q, want %q", gotResolved, wantResolved)
	}
}

// The Go module lives under src/ while the content (scenarios/, runtimes/) is
// at the repo root. Starting from inside src/ must still find the repo root.
func TestFindProjectRoot_FromSrc(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"scenarios", "runtimes"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A src/ tree with its own go.mod but no content dirs must not be mistaken
	// for the project root.
	src := filepath.Join(root, "src")
	if err := os.MkdirAll(filepath.Join(src, "internal", "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(filepath.Join(src, "internal", "config"))

	got, err := findProjectRoot()
	if err != nil {
		t.Fatalf("findProjectRoot (from src): %v", err)
	}
	gotResolved, _ := filepath.EvalSymlinks(got)
	wantResolved, _ := filepath.EvalSymlinks(root)
	if gotResolved != wantResolved {
		t.Errorf("findProjectRoot = %q, want %q (content root, not src/)", gotResolved, wantResolved)
	}
}

func TestListApps(t *testing.T) {
	// Create temp project structure
	root := t.TempDir()
	appsDir := filepath.Join(root, "apps")

	// Create app directories with app.env
	for _, name := range []string{"go-api", "echo-server"} {
		dir := filepath.Join(appsDir, name)
		os.MkdirAll(dir, 0755)
		os.WriteFile(filepath.Join(dir, "app.env"), []byte("APP_NAME="+name), 0644)
	}

	// Create a directory without app.env (should be excluded)
	os.MkdirAll(filepath.Join(appsDir, "not-an-app"), 0755)

	apps, err := ListApps(root)
	if err != nil {
		t.Fatalf("ListApps: %v", err)
	}

	if len(apps) != 2 {
		t.Errorf("expected 2 apps, got %d: %v", len(apps), apps)
	}

	found := map[string]bool{}
	for _, a := range apps {
		found[a] = true
	}
	if !found["go-api"] {
		t.Error("expected go-api in app list")
	}
	if !found["echo-server"] {
		t.Error("expected echo-server in app list")
	}
}

func TestListApps_EmptyDir(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "apps"), 0755)

	apps, err := ListApps(root)
	if err != nil {
		t.Fatalf("ListApps: %v", err)
	}
	if len(apps) != 0 {
		t.Errorf("expected 0 apps, got %d", len(apps))
	}
}

func TestListApps_MissingDir(t *testing.T) {
	root := t.TempDir()
	_, err := ListApps(root)
	if err == nil {
		t.Error("expected error for missing apps directory")
	}
}

func TestLoadAppConfig(t *testing.T) {
	root := t.TempDir()
	appDir := filepath.Join(root, "apps", "test-app")
	os.MkdirAll(appDir, 0755)

	content := `APP_NAME=test-app
BUILD_STRATEGY=docker
DEPLOY_STRATEGY=helm
HELM_RELEASE_NAME=test-app
HELM_VALUES=values-dev.yaml
NAMESPACE=test-ns`

	os.WriteFile(filepath.Join(appDir, "app.env"), []byte(content), 0644)

	cfg, err := LoadAppConfig(root, "test-app")
	if err != nil {
		t.Fatalf("LoadAppConfig: %v", err)
	}

	if cfg.AppName != "test-app" {
		t.Errorf("AppName: got %q, want %q", cfg.AppName, "test-app")
	}
	if cfg.BuildStrategy != "docker" {
		t.Errorf("BuildStrategy: got %q, want %q", cfg.BuildStrategy, "docker")
	}
	if cfg.DeployStrategy != "helm" {
		t.Errorf("DeployStrategy: got %q, want %q", cfg.DeployStrategy, "helm")
	}
	if cfg.HelmRelease != "test-app" {
		t.Errorf("HelmRelease: got %q, want %q", cfg.HelmRelease, "test-app")
	}
	if cfg.HelmValues != "values-dev.yaml" {
		t.Errorf("HelmValues: got %q, want %q", cfg.HelmValues, "values-dev.yaml")
	}
	if cfg.Namespace != "test-ns" {
		t.Errorf("Namespace: got %q, want %q", cfg.Namespace, "test-ns")
	}
}

func TestLoadAppConfig_NotFound(t *testing.T) {
	root := t.TempDir()
	_, err := LoadAppConfig(root, "nonexistent")
	if err == nil {
		t.Error("expected error for missing app config")
	}
}

// LoadAppConfig reads app.env with the same dotenv parser the rest of the
// package uses (quoted values, inline "# comments", missing keys).
func TestLoadAppConfig_EnvParsing(t *testing.T) {
	tests := []struct {
		name   string
		appEnv string
		check  func(t *testing.T, cfg *AppConfig)
	}{
		{
			name: "quoted values",
			appEnv: `APP_NAME="quoted-app"
BUILD_STRATEGY='docker'
DEPLOY_STRATEGY="helm"
HELM_RELEASE_NAME=quoted-app
HELM_VALUES="values-dev.yaml" # trailing comment after quotes
`,
			check: func(t *testing.T, cfg *AppConfig) {
				t.Helper()
				if cfg.AppName != "quoted-app" {
					t.Errorf("AppName: got %q, want %q", cfg.AppName, "quoted-app")
				}
				if cfg.BuildStrategy != "docker" {
					t.Errorf("BuildStrategy: got %q, want %q", cfg.BuildStrategy, "docker")
				}
				if cfg.DeployStrategy != "helm" {
					t.Errorf("DeployStrategy: got %q, want %q", cfg.DeployStrategy, "helm")
				}
				if cfg.HelmValues != "values-dev.yaml" {
					t.Errorf("HelmValues: got %q, want %q", cfg.HelmValues, "values-dev.yaml")
				}
			},
		},
		{
			name: "quoted value keeps a literal hash",
			appEnv: `APP_NAME=test-app
BUILD_STRATEGY=docker
DEPLOY_STRATEGY=helm
HELM_RELEASE_NAME="rel#1"
`,
			check: func(t *testing.T, cfg *AppConfig) {
				t.Helper()
				if cfg.HelmRelease != "rel#1" {
					t.Errorf("HelmRelease: got %q, want %q", cfg.HelmRelease, "rel#1")
				}
			},
		},
		{
			name: "inline comments are stripped",
			appEnv: `# leading comment
APP_NAME=go-api                    # Must match parent directory name
BUILD_STRATEGY=docker              # docker, golang, binary, lambda-zip, etc.
DEPLOY_STRATEGY=helm               # helm, kustomize, lambda, vm, etc.
HELM_RELEASE_NAME=go-api           # Release name in cluster
HELM_VALUES=values-dev.yaml        # Values file
# NAMESPACE=go-api                 # K8s namespace (defaults to APP_NAME if unset)
`,
			check: func(t *testing.T, cfg *AppConfig) {
				t.Helper()
				if cfg.AppName != "go-api" {
					t.Errorf("AppName: got %q, want %q", cfg.AppName, "go-api")
				}
				if cfg.BuildStrategy != "docker" {
					t.Errorf("BuildStrategy: got %q, want %q", cfg.BuildStrategy, "docker")
				}
				if cfg.HelmValues != "values-dev.yaml" {
					t.Errorf("HelmValues: got %q, want %q", cfg.HelmValues, "values-dev.yaml")
				}
			},
		},
		{
			name: "missing keys are empty",
			appEnv: `APP_NAME=bare-app
`,
			check: func(t *testing.T, cfg *AppConfig) {
				t.Helper()
				if cfg.AppName != "bare-app" {
					t.Errorf("AppName: got %q, want %q", cfg.AppName, "bare-app")
				}
				for _, kv := range []struct {
					key, val string
				}{
					{"BuildStrategy", cfg.BuildStrategy},
					{"DeployStrategy", cfg.DeployStrategy},
					{"HelmRelease", cfg.HelmRelease},
					{"HelmValues", cfg.HelmValues},
					{"Namespace", cfg.Namespace},
				} {
					if kv.val != "" {
						t.Errorf("%s: got %q, want empty", kv.key, kv.val)
					}
				}
			},
		},
		{
			name: "contract fields",
			appEnv: `APP_NAME=contract-app
BUILD_STRATEGY=docker
DEPLOY_STRATEGY=helm
APP_PORT=9090
APP_HEALTH_PATH=/live
APP_READY_PATH=/readyz
APP_METRICS_PATH=/prom
APP_REQUEST_METRIC=my_request_seconds
APP_CAPABILITIES=prometheus-metrics,readiness-toggle
`,
			check: func(t *testing.T, cfg *AppConfig) {
				t.Helper()
				c := cfg.Contract
				if c.Port != "9090" {
					t.Errorf("Contract.Port: got %q, want %q", c.Port, "9090")
				}
				if c.HealthPath != "/live" {
					t.Errorf("Contract.HealthPath: got %q, want %q", c.HealthPath, "/live")
				}
				if c.ReadyPath != "/readyz" {
					t.Errorf("Contract.ReadyPath: got %q, want %q", c.ReadyPath, "/readyz")
				}
				if c.MetricsPath != "/prom" {
					t.Errorf("Contract.MetricsPath: got %q, want %q", c.MetricsPath, "/prom")
				}
				if c.RequestMetric != "my_request_seconds" {
					t.Errorf("Contract.RequestMetric: got %q, want %q", c.RequestMetric, "my_request_seconds")
				}
				if len(c.Capabilities) != 2 {
					t.Errorf("Contract.Capabilities: got %v, want 2 entries", c.Capabilities)
				}
			},
		},
		{
			name: "contract fields fall back to defaults when absent",
			appEnv: `APP_NAME=defaults-app
`,
			check: func(t *testing.T, cfg *AppConfig) {
				t.Helper()
				c := cfg.Contract
				if c.Port != workload.DefaultPort {
					t.Errorf("Contract.Port: got %q, want default %q", c.Port, workload.DefaultPort)
				}
				if c.HealthPath != workload.DefaultHealthPath {
					t.Errorf("Contract.HealthPath: got %q, want default %q", c.HealthPath, workload.DefaultHealthPath)
				}
				if len(c.Capabilities) != 0 {
					t.Errorf("Contract.Capabilities: got %v, want none", c.Capabilities)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			appDir := filepath.Join(root, "apps", "test-app")
			if err := os.MkdirAll(appDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(appDir, "app.env"), []byte(tt.appEnv), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadAppConfig(root, "test-app")
			if err != nil {
				t.Fatalf("LoadAppConfig: %v", err)
			}
			tt.check(t, cfg)
		})
	}
}

// Every app.env shipped in the repo must load to the config its fields
// declare, including keys that are missing entirely (e.g. HELM_VALUES in
// java-api, NAMESPACE everywhere).
func TestLoadAppConfig_ShippedApps(t *testing.T) {
	repoRoot, err := findProjectRoot()
	if err != nil {
		t.Fatalf("findProjectRoot: %v", err)
	}

	apps, err := ListApps(repoRoot)
	if err != nil {
		t.Fatalf("ListApps: %v", err)
	}
	if len(apps) == 0 {
		t.Fatal("no apps found under apps/")
	}

	for _, name := range apps {
		cfg, err := LoadAppConfig(repoRoot, name)
		if err != nil {
			t.Errorf("LoadAppConfig(%s): %v", name, err)
			continue
		}
		if cfg.AppName != name {
			t.Errorf("LoadAppConfig(%s).AppName = %q, want the app directory name", name, cfg.AppName)
		}
	}
}

// ---------------------------------------------------------------------------
// Profile / app validation tests
// ---------------------------------------------------------------------------

func TestLoad_InvalidProfile_ReturnsError(t *testing.T) {
	saved, ok := os.LookupEnv("PROFILE")
	os.Setenv("PROFILE", "bogus-runtime")
	defer func() {
		if ok {
			os.Setenv("PROFILE", saved)
		} else {
			os.Unsetenv("PROFILE")
		}
	}()

	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "runtimes", "k3d"), 0755) // only k3d exists

	_, err := Load(root)
	if err == nil {
		t.Fatal("expected error for invalid profile, got nil")
	}
	if !containsAll(err.Error(), "bogus-runtime", "k3d") {
		t.Errorf("error should name the bad profile and list valid ones: %v", err)
	}
}

func TestLoad_ValidProfile_Succeeds(t *testing.T) {
	saved, ok := os.LookupEnv("PROFILE")
	os.Setenv("PROFILE", "k3d")
	defer func() {
		if ok {
			os.Setenv("PROFILE", saved)
		} else {
			os.Unsetenv("PROFILE")
		}
	}()

	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "runtimes", "k3d"), 0755)

	_, err := Load(root)
	if err != nil {
		t.Fatalf("Load with valid profile: %v", err)
	}
}

func TestLoadAppConfig_InvalidApp_ReturnsError(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "apps", "go-api"), 0755)
	os.WriteFile(filepath.Join(root, "apps", "go-api", "app.env"), []byte("APP_NAME=go-api"), 0644)

	_, err := LoadAppConfig(root, "nonexistent-app")
	if err == nil {
		t.Fatal("expected error for invalid app, got nil")
	}
	if !containsAll(err.Error(), "nonexistent-app", "go-api") {
		t.Errorf("error should name the bad app and list valid ones: %v", err)
	}
}

// containsAll returns true if s contains all of the substrings.
func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

func TestLoad_Defaults(t *testing.T) {
	// Clear environment to test defaults
	envVars := []string{
		"PROFILE", "CLUSTER_NAME", "HTTP_PORT", "HTTPS_PORT",
		"INGRESS_CLASS", "STORAGE_CLASS", "DOMAIN_SUFFIX", "REGISTRY_TYPE",
		"INGRESS_PROVIDER", "METRICS_PROVIDER", "MONITORING_NAMESPACE", "APP_NAME",
	}
	saved := map[string]string{}
	for _, k := range envVars {
		saved[k], _ = os.LookupEnv(k)
		os.Unsetenv(k)
	}
	defer func() {
		for k, v := range saved {
			if v != "" {
				os.Setenv(k, v)
			}
		}
	}()

	root := t.TempDir()
	// Create minimal project structure for Load
	os.MkdirAll(filepath.Join(root, "runtimes", "k3d"), 0755)

	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Profile != "k3d" {
		t.Errorf("Profile: got %q, want %q", cfg.Profile, "k3d")
	}
	if cfg.ClusterName != "snowops" {
		t.Errorf("ClusterName: got %q, want %q", cfg.ClusterName, "snowops")
	}
	if cfg.DomainSuffix != "snowops.localhost" {
		t.Errorf("DomainSuffix: got %q, want %q", cfg.DomainSuffix, "snowops.localhost")
	}
	if cfg.MonitoringNamespace != "monitoring" {
		t.Errorf("MonitoringNamespace: got %q, want %q", cfg.MonitoringNamespace, "monitoring")
	}
	if cfg.IngressProvider != "traefik" {
		t.Errorf("IngressProvider: got %q, want %q", cfg.IngressProvider, "traefik")
	}
}

// TestLoad_Isolation checks that Load does not write file values into the
// process environment, so loading a second project returns its own values.
func TestLoad_Isolation(t *testing.T) {
	clearConfigEnv(t)

	mkProject := func(cluster, domain string) string {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "runtimes", "k3d"), 0o755); err != nil {
			t.Fatal(err)
		}
		body := "CLUSTER_NAME=" + cluster + "\nDOMAIN_SUFFIX=" + domain + "\n"
		if err := os.WriteFile(filepath.Join(root, "runtimes", "k3d", "runtime.env"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return root
	}

	c1, err := Load(mkProject("cluster-one", "one.local"))
	if err != nil {
		t.Fatalf("first Load: %v", err)
	}
	if c1.ClusterName != "cluster-one" || c1.DomainSuffix != "one.local" {
		t.Fatalf("first Load: got %q/%q, want cluster-one/one.local", c1.ClusterName, c1.DomainSuffix)
	}

	c2, err := Load(mkProject("cluster-two", "two.local"))
	if err != nil {
		t.Fatalf("second Load: %v", err)
	}
	if c2.ClusterName != "cluster-two" || c2.DomainSuffix != "two.local" {
		t.Errorf("second Load leaked first project's values: got %q/%q, want cluster-two/two.local",
			c2.ClusterName, c2.DomainSuffix)
	}

	// Load must not have mutated the process environment.
	if v, ok := os.LookupEnv("CLUSTER_NAME"); ok {
		t.Errorf("Load polluted the process environment: CLUSTER_NAME=%q", v)
	}
}

// TestLoad_ScriptEnv verifies file-declared keys are exposed for propagation to
// child processes, with real environment variables still taking precedence.
func TestLoad_ScriptEnv(t *testing.T) {
	clearConfigEnv(t)

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "runtimes", "k3d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"),
		[]byte("METRICS_PROVIDER=victoria\nREGISTRY_TYPE=k3d-import\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "runtimes", "k3d", "runtime.env"),
		[]byte("CLUSTER_NAME=fromfile\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ScriptEnv["METRICS_PROVIDER"] != "victoria" {
		t.Errorf("ScriptEnv[METRICS_PROVIDER]: got %q, want victoria", cfg.ScriptEnv["METRICS_PROVIDER"])
	}
	if cfg.ScriptEnv["CLUSTER_NAME"] != "fromfile" {
		t.Errorf("ScriptEnv[CLUSTER_NAME]: got %q, want fromfile", cfg.ScriptEnv["CLUSTER_NAME"])
	}

	// A real env var overrides the file value in ScriptEnv too.
	t.Setenv("METRICS_PROVIDER", "from-real-env")
	cfg, err = Load(root)
	if err != nil {
		t.Fatalf("Load (with env override): %v", err)
	}
	if cfg.ScriptEnv["METRICS_PROVIDER"] != "from-real-env" {
		t.Errorf("ScriptEnv override: got %q, want from-real-env", cfg.ScriptEnv["METRICS_PROVIDER"])
	}
}

// TestParseEnvValue covers quoting and inline-comment handling.
func TestParseEnvValue(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"plain", "traefik", "traefik"},
		{"surrounding spaces", "  traefik  ", "traefik"},
		{"double quoted", `"k3d.local"`, "k3d.local"},
		{"single quoted", `'k3d.local'`, "k3d.local"},
		{"quoted preserves spaces", `"a b"`, "a b"},
		{"quoted preserves hash", `"a # b"`, "a # b"},
		{"quoted then comment", `"prod" # env`, "prod"},
		{"unquoted inline comment", "traefik # default", "traefik"},
		{"hash without leading space kept", "abc#def", "abc#def"},
		{"empty", "", ""},
		{"unterminated quote", `"oops`, `"oops`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseEnvValue(tt.raw); got != tt.want {
				t.Errorf("parseEnvValue(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

// TestMergeEnvFile_QuotedValueReachesConfig checks that a quoted runtime.env
// value reaches the Config without its quotes.
func TestMergeEnvFile_QuotedValueReachesConfig(t *testing.T) {
	clearConfigEnv(t)

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "runtimes", "k3d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "runtimes", "k3d", "runtime.env"),
		[]byte(`DOMAIN_SUFFIX="quoted.local"`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DomainSuffix != "quoted.local" {
		t.Errorf("DomainSuffix: got %q, want quoted.local (quotes must be stripped)", cfg.DomainSuffix)
	}
}

// clearConfigEnv unsets the config keys for the duration of a test so file/real
// env precedence can be asserted deterministically. t.Setenv restores them.
// labctl's state moves to an empty temp dir too, so the ports and domain
// recorded for a real lab on this machine never leak into the result.
func clearConfigEnv(t *testing.T) {
	t.Helper()
	t.Setenv("SNOWOPS_HOME", t.TempDir())
	for _, k := range []string{
		"PROFILE", "CLUSTER_NAME", "DOMAIN_SUFFIX", "HTTP_PORT", "HTTPS_PORT",
		"INGRESS_CLASS", "STORAGE_CLASS", "REGISTRY_TYPE", "MONITORING_NAMESPACE",
		"INGRESS_PROVIDER", "METRICS_PROVIDER",
	} {
		if _, ok := os.LookupEnv(k); ok {
			t.Setenv(k, "") // record original for restoration
		}
		os.Unsetenv(k)
	}
}

func TestLoad_MonitoringNamespaceOverride(t *testing.T) {
	saved, ok := os.LookupEnv("MONITORING_NAMESPACE")
	os.Setenv("MONITORING_NAMESPACE", "observability")
	defer func() {
		if ok {
			os.Setenv("MONITORING_NAMESPACE", saved)
		} else {
			os.Unsetenv("MONITORING_NAMESPACE")
		}
	}()

	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "runtimes", "k3d"), 0755)

	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MonitoringNamespace != "observability" {
		t.Errorf("MonitoringNamespace: got %q, want %q", cfg.MonitoringNamespace, "observability")
	}
}

// labRoot makes a minimal project with a k3d profile and the given .env.
func labRoot(t *testing.T, dotenv string) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"scenarios", "runtimes/k3d"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte(dotenv), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestRecordedPortsFollowTheCluster(t *testing.T) {
	for _, k := range []string{"HTTP_PORT", "HTTPS_PORT", "CLUSTER_NAME", "PROFILE", "DOMAIN_SUFFIX"} {
		t.Setenv(k, "")
	}
	home := t.TempDir()
	t.Setenv("SNOWOPS_HOME", home)
	root := labRoot(t, "HTTP_PORT=80\nCLUSTER_NAME=lab\n")

	t.Run("no record keeps .env", func(t *testing.T) {
		cfg, err := Load(root)
		if err != nil {
			t.Fatal(err)
		}
		if got := cfg.IngressURL("grafana"); got != "http://grafana.lab.localhost" {
			t.Errorf("IngressURL = %q", got)
		}
	})

	if err := os.MkdirAll(filepath.Join(home, "clusters"), 0o755); err != nil {
		t.Fatal(err)
	}
	rec := "HTTP_PORT=8080\nHTTPS_PORT=8443\n"
	if err := os.WriteFile(filepath.Join(home, "clusters", "lab.env"), []byte(rec), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("the runtime's record beats .env", func(t *testing.T) {
		cfg, err := Load(root)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.HTTPPort != "8080" || cfg.HTTPSPort != "8443" {
			t.Errorf("ports = %s/%s, want 8080/8443", cfg.HTTPPort, cfg.HTTPSPort)
		}
		if got := cfg.IngressURLSuffix(); got != "lab.localhost:8080" {
			t.Errorf("IngressURLSuffix = %q", got)
		}
		if got := cfg.IngressURL("grafana"); got != "http://grafana.lab.localhost:8080" {
			t.Errorf("IngressURL = %q", got)
		}
		if cfg.ScriptEnv["HTTP_PORT"] != "8080" {
			t.Errorf("scripts must see the recorded port, got %q", cfg.ScriptEnv["HTTP_PORT"])
		}
	})

	t.Run("a lab keeps the suffix it was built with", func(t *testing.T) {
		old := "HTTP_PORT=8080\nHTTPS_PORT=8443\nDOMAIN_SUFFIX=k3d.local\n"
		if err := os.WriteFile(filepath.Join(home, "clusters", "lab.env"), []byte(old), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(root)
		if err != nil {
			t.Fatal(err)
		}
		if got := cfg.IngressURL("grafana"); got != "http://grafana.k3d.local:8080" {
			t.Errorf("IngressURL = %q", got)
		}
		if cfg.ScriptEnv["DOMAIN_SUFFIX"] != "k3d.local" {
			t.Errorf("scripts must see the recorded suffix, got %q", cfg.ScriptEnv["DOMAIN_SUFFIX"])
		}
	})

	t.Run("a real environment variable still wins", func(t *testing.T) {
		t.Setenv("HTTP_PORT", "9090")
		cfg, err := Load(root)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.HTTPPort != "9090" {
			t.Errorf("HTTPPort = %q, want 9090", cfg.HTTPPort)
		}
	})
}

func TestLocalIngress(t *testing.T) {
	for profile, want := range map[string]bool{"k3d": true, "kind": true, "incluster": false} {
		if got := (&Config{Profile: profile}).LocalIngress(); got != want {
			t.Errorf("%s: LocalIngress = %v, want %v", profile, got, want)
		}
	}
}

func TestHome(t *testing.T) {
	tests := []struct {
		name        string
		snowopsHome string
		home        string
		expected    string
	}{
		{name: "snowops_home wins", snowopsHome: "/srv/lab", home: "/home/me", expected: "/srv/lab"},
		{name: "defaults under the home directory", home: "/home/me", expected: filepath.Join("/home/me", ".snowops")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SNOWOPS_HOME", tt.snowopsHome)
			t.Setenv("HOME", tt.home)
			got, err := Home()
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.expected {
				t.Errorf("Home() = %q, want %q", got, tt.expected)
			}
		})
	}
}

// makeLab creates a directory that looks like a clone: scenarios/ and runtimes/.
func makeLab(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, d := range []string{"scenarios", "runtimes"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestConfig_ResolvesLocally(t *testing.T) {
	tests := []struct {
		name     string
		suffix   string
		expected bool
	}{
		{name: "per-cluster localhost", suffix: "snowops.localhost", expected: true},
		{name: "bare localhost", suffix: "localhost", expected: true},
		{name: "a .local suffix", suffix: "k3d.local", expected: false},
		{name: "a name that only contains localhost", suffix: "mylocalhost", expected: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (&Config{DomainSuffix: tt.suffix}).ResolvesLocally(); got != tt.expected {
				t.Errorf("ResolvesLocally(%q) = %v, want %v", tt.suffix, got, tt.expected)
			}
		})
	}
}

func TestFindProjectRoot(t *testing.T) {
	cwdLab, envLab, recorded := makeLab(t), makeLab(t), makeLab(t)
	outside := t.TempDir()
	tests := []struct {
		name     string
		cwd      string
		env      string
		record   string
		expected string
		wantErr  string
	}{
		{name: "the clone around the working directory wins", cwd: filepath.Join(cwdLab, "scenarios"), env: envLab, record: recorded, expected: cwdLab},
		{name: "snowops_lab_dir outside a clone", cwd: outside, env: envLab, record: recorded, expected: envLab},
		{name: "the recorded clone", cwd: outside, record: recorded, expected: recorded},
		{name: "snowops_lab_dir that is not a clone", cwd: outside, env: outside, wantErr: "is not a snowopslabs clone"},
		{name: "a recorded clone that is gone", cwd: outside, record: filepath.Join(outside, "deleted"), wantErr: "is gone"},
		{name: "nothing to find", cwd: outside, wantErr: "could not find your lab"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("SNOWOPS_HOME", home)
			t.Setenv("SNOWOPS_LAB_DIR", tt.env)
			if tt.record != "" {
				if err := os.WriteFile(filepath.Join(home, "lab-dir"), []byte(tt.record+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			t.Chdir(tt.cwd)

			got, err := findProjectRoot()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.expected {
				t.Errorf("findProjectRoot = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestLabVersion(t *testing.T) {
	tests := []struct {
		name     string
		file     string
		expected string
	}{
		{name: "release", file: "1.5.0\n", expected: "1.5.0"},
		{name: "no lab_version file", expected: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if tt.file != "" {
				if err := os.WriteFile(filepath.Join(root, "LAB_VERSION"), []byte(tt.file), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if got := LabVersion(root); got != tt.expected {
				t.Errorf("LabVersion = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestStateDir(t *testing.T) {
	t.Setenv("SNOWOPS_HOME", "/srv/snowops")
	got, err := StateDir("lab")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("/srv/snowops", "state", "lab"); got != want {
		t.Errorf("StateDir = %q, want %q", got, want)
	}
}

func TestMigrateState(t *testing.T) {
	tests := []struct {
		name       string
		legacy     bool
		target     bool
		wantMoved  bool
		wantErr    string
		wantMarker string // where the legacy marker file must end up
	}{
		{name: "moves legacy state once", legacy: true, wantMoved: true, wantMarker: "target"},
		{name: "nothing to move", target: true},
		{name: "both exist", legacy: true, target: true, wantErr: "lab state is in both", wantMarker: "legacy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := t.TempDir()
			legacy := filepath.Join(base, "clone", ".labctl")
			target := filepath.Join(base, "home", "state", "snowops")
			if tt.legacy {
				if err := os.MkdirAll(legacy, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(legacy, "marker"), []byte("x"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tt.target {
				if err := os.MkdirAll(target, 0o755); err != nil {
					t.Fatal(err)
				}
			}

			moved, err := MigrateState(legacy, target)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
			if moved != tt.wantMoved {
				t.Errorf("moved = %v, want %v", moved, tt.wantMoved)
			}
			switch tt.wantMarker {
			case "target":
				if _, err := os.Stat(filepath.Join(target, "marker")); err != nil {
					t.Errorf("state not in target: %v", err)
				}
			case "legacy":
				if _, err := os.Stat(filepath.Join(legacy, "marker")); err != nil {
					t.Errorf("legacy state touched: %v", err)
				}
			}
		})
	}
}

// Only a suffix the user set, in the environment or .env, pins the lab to it;
// one from runtime.env or the cluster record is a default labctl may move.
func TestLoad_DomainSuffixPinned(t *testing.T) {
	tests := []struct {
		name       string
		env        string
		dotEnv     string
		runtimeEnv string
		record     string
		want       bool
	}{
		{name: "nothing set"},
		{name: "set in .env", dotEnv: "DOMAIN_SUFFIX=k3d.local\n", want: true},
		{name: "set in the environment", env: "k3d.local", want: true},
		{name: "only in runtime.env", runtimeEnv: "DOMAIN_SUFFIX=k3d.local\n"},
		{name: "only in the cluster record", record: "DOMAIN_SUFFIX=k3d.local\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearConfigEnv(t)
			if tt.env != "" {
				t.Setenv("DOMAIN_SUFFIX", tt.env)
			}
			root := t.TempDir()
			write := func(path, body string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			write(filepath.Join(root, "runtimes", "k3d", "runtime.env"), tt.runtimeEnv)
			write(filepath.Join(root, ".env"), tt.dotEnv)
			if tt.record != "" {
				file, err := ClusterStateFile("snowops")
				if err != nil {
					t.Fatal(err)
				}
				write(file, tt.record)
			}
			cfg, err := Load(root)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.DomainSuffixPinned != tt.want {
				t.Errorf("DomainSuffixPinned = %v, want %v", cfg.DomainSuffixPinned, tt.want)
			}
		})
	}
}

// The ingress class follows the installed ingress provider unless set, on
// every runtime the lab ships: an app's Ingress with another controller's
// class is never routed.
func TestLoad_IngressClassFollowsTheProvider(t *testing.T) {
	repo, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		profile string
		dotenv  string
		want    string
	}{
		{name: "kind, default provider", profile: "kind", want: "traefik"},
		{name: "kind, nginx", profile: "kind", dotenv: "INGRESS_PROVIDER=nginx\n", want: "nginx"},
		{name: "k3d, default provider", profile: "k3d", want: "traefik"},
		{name: "k3d, nginx", profile: "k3d", dotenv: "INGRESS_PROVIDER=nginx\n", want: "nginx"},
		{name: "an explicit class wins", profile: "kind", dotenv: "INGRESS_PROVIDER=nginx\nINGRESS_CLASS=internal\n", want: "internal"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, k := range []string{"INGRESS_CLASS", "INGRESS_PROVIDER", "PROFILE"} {
				t.Setenv(k, "")
			}
			root := labRoot(t, "PROFILE="+tc.profile+"\n"+tc.dotenv)
			runtimeEnv, err := os.ReadFile(filepath.Join(repo, "runtimes", tc.profile, "runtime.env"))
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(root, "runtimes", tc.profile)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "runtime.env"), runtimeEnv, 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(root)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.IngressClass != tc.want {
				t.Errorf("IngressClass = %q, want %q (provider %q)", cfg.IngressClass, tc.want, cfg.IngressProvider)
			}
		})
	}
}

// For every key the first value that is not empty wins: environment, .env,
// the profile's runtime.env, then the built-in default. A blank line in .env
// leaves the key unset, so kind keeps its own storage class. Scripts get the
// same value, or an empty one when only the built-in default applies.
func TestLoad_FirstNonEmptyValueWins(t *testing.T) {
	tests := []struct {
		name       string
		setEnv     bool
		env        string
		dotEnv     string
		runtimeEnv string
		want       string
		wantScript string
	}{
		{name: "only the profile sets it", runtimeEnv: "STORAGE_CLASS=standard\n", want: "standard", wantScript: "standard"},
		{name: ".env wins over the profile", dotEnv: "STORAGE_CLASS=fast\n", runtimeEnv: "STORAGE_CLASS=standard\n", want: "fast", wantScript: "fast"},
		{name: "a blank .env line falls through to the profile", dotEnv: "STORAGE_CLASS=\n", runtimeEnv: "STORAGE_CLASS=standard\n", want: "standard", wantScript: "standard"},
		{name: "a blank quoted .env value falls through to the profile", dotEnv: "STORAGE_CLASS=\"\"\n", runtimeEnv: "STORAGE_CLASS=standard\n", want: "standard", wantScript: "standard"},
		{name: "a later .env line fills a blank earlier one", dotEnv: "STORAGE_CLASS=\nSTORAGE_CLASS=fast\n", runtimeEnv: "STORAGE_CLASS=standard\n", want: "fast", wantScript: "fast"},
		{name: "blank everywhere gives the default", dotEnv: "STORAGE_CLASS=\n", runtimeEnv: "STORAGE_CLASS=\n", want: "local-path", wantScript: ""},
		{name: "the environment wins over everything", setEnv: true, env: "env-class", dotEnv: "STORAGE_CLASS=fast\n", runtimeEnv: "STORAGE_CLASS=standard\n", want: "env-class", wantScript: "env-class"},
		{name: "a blank environment variable is skipped", setEnv: true, dotEnv: "STORAGE_CLASS=\n", runtimeEnv: "STORAGE_CLASS=standard\n", want: "standard", wantScript: "standard"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearConfigEnv(t)
			if tt.setEnv {
				t.Setenv("STORAGE_CLASS", tt.env)
			}
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, "runtimes", "kind"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, ".env"), []byte("PROFILE=kind\n"+tt.dotEnv), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "runtimes", "kind", "runtime.env"), []byte(tt.runtimeEnv), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(root)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.StorageClass != tt.want {
				t.Errorf("StorageClass = %q, want %q", cfg.StorageClass, tt.want)
			}
			if got := cfg.ScriptEnv["STORAGE_CLASS"]; got != tt.wantScript {
				t.Errorf("ScriptEnv[STORAGE_CLASS] = %q, want %q", got, tt.wantScript)
			}
		})
	}
}
