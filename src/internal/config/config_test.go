// SPDX-License-Identifier: Apache-2.0
package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// findProjectRoot must detect a make-free checkout — the release binary dropped
// next to a git clone that has no Makefile. It keys on scenarios/ + runtimes/,
// user-facing content that stays at the repo root after the Go source moved
// under src/.
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

// After the src/ restructure the Go module lives under src/, while the
// user-facing content (scenarios/, runtimes/) stays at the repo root. Running
// labctl or `go test` from inside src/ must still walk up to the content root.
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

// ---------------------------------------------------------------------------
// Profile / app validation tests (task 029)
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
	if cfg.DomainSuffix != "k3d.local" {
		t.Errorf("DomainSuffix: got %q, want %q", cfg.DomainSuffix, "k3d.local")
	}
	if cfg.MonitoringNamespace != "monitoring" {
		t.Errorf("MonitoringNamespace: got %q, want %q", cfg.MonitoringNamespace, "monitoring")
	}
	if cfg.IngressProvider != "traefik" {
		t.Errorf("IngressProvider: got %q, want %q", cfg.IngressProvider, "traefik")
	}
}

// TestLoad_Isolation guards against the global-env-pollution regression: Load
// must not stash file values in the process environment, so loading a second,
// independent project returns that project's values rather than the first's.
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

// TestParseEnvValue covers quoting and inline-comment handling (finding #4).
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

// TestMergeEnvFile_QuotedValueReachesConfig is the end-to-end proof of #4: a
// quoted runtime.env value must land in the Config without its quotes.
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
func clearConfigEnv(t *testing.T) {
	t.Helper()
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
