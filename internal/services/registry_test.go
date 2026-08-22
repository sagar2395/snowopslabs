// SPDX-License-Identifier: Apache-2.0
package services

import (
	"os"
	"path/filepath"
	"testing"
)

func createTestService(t *testing.T, root, name string) {
	t.Helper()
	dir := filepath.Join(root, "services", name)
	os.MkdirAll(dir, 0755)
	for _, script := range []string{"install.sh", "uninstall.sh", "status.sh"} {
		os.WriteFile(filepath.Join(dir, script), []byte("#!/bin/bash\necho ok"), 0755)
	}
	os.WriteFile(filepath.Join(dir, "values.yaml"), []byte("# test values"), 0644)
}

func TestNewRegistry_Discovery(t *testing.T) {
	root := t.TempDir()

	createTestService(t, root, "redis")
	createTestService(t, root, "postgres")

	reg := NewRegistry(root)
	svcs := reg.List()

	if len(svcs) != 2 {
		t.Fatalf("expected 2 services, got %d", len(svcs))
	}

	found := map[string]bool{}
	for _, s := range svcs {
		found[s.Name] = true
	}
	if !found["redis"] {
		t.Error("expected redis in service list")
	}
	if !found["postgres"] {
		t.Error("expected postgres in service list")
	}
}

func TestGet(t *testing.T) {
	root := t.TempDir()
	createTestService(t, root, "redis")

	reg := NewRegistry(root)
	s, err := reg.Get("redis")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if s.Name != "redis" {
		t.Errorf("Name: got %q, want %q", s.Name, "redis")
	}
}

func TestGet_NotFound(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "services"), 0755)

	reg := NewRegistry(root)
	_, err := reg.Get("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent service")
	}
}

func TestService_HasScript(t *testing.T) {
	root := t.TempDir()
	createTestService(t, root, "redis")

	reg := NewRegistry(root)
	s, _ := reg.Get("redis")

	if !s.HasScript("install.sh") {
		t.Error("expected HasScript(install.sh) = true")
	}
	if !s.HasScript("status.sh") {
		t.Error("expected HasScript(status.sh) = true")
	}
	if s.HasScript("nonexistent.sh") {
		t.Error("expected HasScript(nonexistent.sh) = false")
	}
}

func TestNewRegistry_EmptyDir(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "services"), 0755)

	reg := NewRegistry(root)
	if len(reg.List()) != 0 {
		t.Errorf("expected 0 services, got %d", len(reg.List()))
	}
}

func TestNewRegistry_MissingDir(t *testing.T) {
	root := t.TempDir()
	reg := NewRegistry(root)
	if len(reg.List()) != 0 {
		t.Errorf("expected 0 services when directory missing, got %d", len(reg.List()))
	}
}

func TestNewRegistry_SkipsNonService(t *testing.T) {
	root := t.TempDir()

	// Create a directory without install.sh — should be excluded
	dir := filepath.Join(root, "services", "not-a-service")
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("nope"), 0644)

	createTestService(t, root, "redis")

	reg := NewRegistry(root)
	svcs := reg.List()
	if len(svcs) != 1 {
		t.Errorf("expected 1 service, got %d", len(svcs))
	}
}
