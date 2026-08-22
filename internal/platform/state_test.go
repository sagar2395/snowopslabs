// SPDX-License-Identifier: Apache-2.0
package platform

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/sagar2395/snowopslabs/internal/executor"
)

// fakeProvider creates platform/<category>/<name>/ with install/uninstall
// scripts that exit with the given code.
func fakeProvider(t *testing.T, root, category, name string, exitCode byte) {
	t.Helper()
	dir := filepath.Join(root, "platform", category, name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	script := []byte("#!/usr/bin/env bash\nexit " + string('0'+exitCode) + "\n")
	for _, f := range []string{"install.sh", "uninstall.sh", "status.sh"} {
		if err := os.WriteFile(filepath.Join(dir, f), script, 0755); err != nil {
			t.Fatal(err)
		}
	}
	// values.yaml marks the dir as a provider for scan()
	if err := os.WriteFile(filepath.Join(dir, "values.yaml"), []byte("# stub"), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestInstallMarkers_RoundTrip(t *testing.T) {
	root := t.TempDir()
	fakeProvider(t, root, "ingress", "traefik", 0)
	fakeProvider(t, root, "monitoring", "metrics", 0)

	reg := NewRegistry(root)
	ex := executor.New(root)

	if got := reg.Installed(); got != nil {
		t.Fatalf("fresh registry should report nothing installed, got %v", got)
	}

	if err := reg.Install("ingress", "traefik", ex); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := reg.InstallStreamed("monitoring", "metrics", ex); err != nil {
		t.Fatalf("InstallStreamed: %v", err)
	}

	want := []string{"ingress/traefik", "monitoring/metrics"}
	if got := reg.Installed(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Installed = %v, want %v", got, want)
	}

	// A fresh registry instance must see the same state (it's on disk).
	if got := NewRegistry(root).Installed(); !reflect.DeepEqual(got, want) {
		t.Fatalf("state must survive registry restarts, got %v", got)
	}

	if err := reg.Uninstall("ingress", "traefik", ex); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if got := reg.Installed(); !reflect.DeepEqual(got, []string{"monitoring/metrics"}) {
		t.Fatalf("after uninstall: %v", got)
	}
	if err := reg.UninstallStreamed("monitoring", "metrics", ex); err != nil {
		t.Fatalf("UninstallStreamed: %v", err)
	}
	if got := reg.Installed(); got != nil {
		t.Fatalf("after full uninstall: %v", got)
	}
}

func TestInstallMarkers_FailedInstallNotRecorded(t *testing.T) {
	root := t.TempDir()
	fakeProvider(t, root, "ingress", "broken", 1) // install.sh exits 1

	reg := NewRegistry(root)
	ex := executor.New(root)

	if err := reg.Install("ingress", "broken", ex); err == nil {
		t.Fatal("expected install failure")
	}
	if got := reg.Installed(); got != nil {
		t.Fatalf("failed install must not be recorded, got %v", got)
	}
}

func TestInstallMarkers_FailedUninstallKeepsMarker(t *testing.T) {
	root := t.TempDir()
	fakeProvider(t, root, "ingress", "traefik", 0)

	reg := NewRegistry(root)
	ex := executor.New(root)
	if err := reg.Install("ingress", "traefik", ex); err != nil {
		t.Fatal(err)
	}

	// Make uninstall fail: the component is still (presumed) on the cluster.
	script := filepath.Join(root, "platform", "ingress", "traefik", "uninstall.sh")
	os.WriteFile(script, []byte("#!/usr/bin/env bash\nexit 1\n"), 0755)

	if err := reg.Uninstall("ingress", "traefik", ex); err == nil {
		t.Fatal("expected uninstall failure")
	}
	if got := reg.Installed(); !reflect.DeepEqual(got, []string{"ingress/traefik"}) {
		t.Fatalf("failed uninstall must keep the marker, got %v", got)
	}
}
