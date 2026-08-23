// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sagar2395/snowopslabs/internal/config"
)

func writeApp(t *testing.T, root, name, appEnv string) {
	t.Helper()
	dir := filepath.Join(root, "apps", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.env"), []byte(appEnv), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAppExistsAndNamespace(t *testing.T) {
	root := t.TempDir()
	writeApp(t, root, "with-ns", "APP_NAME=with-ns\nNAMESPACE=custom-ns\n")
	writeApp(t, root, "no-ns", "APP_NAME=no-ns\n")

	saved := cfg
	cfg = &config.Config{ProjectRoot: root}
	t.Cleanup(func() { cfg = saved })

	if !appExists("with-ns") {
		t.Error("appExists(with-ns) = false, want true")
	}
	if appExists("nope") {
		t.Error("appExists(nope) = true, want false")
	}
	// Explicit NAMESPACE wins; otherwise the app name is the namespace.
	if got := appNamespace("with-ns"); got != "custom-ns" {
		t.Errorf("appNamespace(with-ns) = %q, want %q", got, "custom-ns")
	}
	if got := appNamespace("no-ns"); got != "no-ns" {
		t.Errorf("appNamespace(no-ns) = %q, want %q", got, "no-ns")
	}
}
