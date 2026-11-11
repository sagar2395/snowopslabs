// SPDX-License-Identifier: Apache-2.0

package services

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snowops-oss/snowopslabs/internal/executor"
	"github.com/snowops-oss/snowopslabs/internal/labtest"
)

func createTestService(t *testing.T, root, name string) {
	t.Helper()
	dir := filepath.Join(root, "src", "services", name)
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
	os.MkdirAll(filepath.Join(root, "src", "services"), 0755)

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
	os.MkdirAll(filepath.Join(root, "src", "services"), 0755)

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

	// Create a directory without install.sh - should be excluded
	dir := filepath.Join(root, "src", "services", "not-a-service")
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("nope"), 0644)

	createTestService(t, root, "redis")

	reg := NewRegistry(root)
	svcs := reg.List()
	if len(svcs) != 1 {
		t.Errorf("expected 1 service, got %d", len(svcs))
	}
}

// fixtureRegistry is a Registry over the fixture lab, with the executor that
// runs its scripts and the buffer their output goes to.
func fixtureRegistry(t *testing.T) (*Registry, *executor.Executor, *labtest.Lab, *bytes.Buffer) {
	t.Helper()
	l := labtest.New(t)
	ex := executor.New(l.Root)
	var out bytes.Buffer
	ex.Stdout, ex.Stderr = &out, &out
	return NewRegistry(l.Root), ex, l, &out
}

func TestInstallUninstallStatus(t *testing.T) {
	steps := []struct {
		name   string
		script string
		run    func(r *Registry, ex *executor.Executor, service string) error
		want   string
	}{
		{"Install", "install.sh", func(r *Registry, ex *executor.Executor, s string) error { return r.Install(s, ex) }, "installing pager"},
		{"Uninstall", "uninstall.sh", func(r *Registry, ex *executor.Executor, s string) error { return r.Uninstall(s, ex) }, "removing pager"},
		{"Status", "status.sh", func(r *Registry, ex *executor.Executor, s string) error { return r.Status(s, ex) }, "running"},
	}
	for _, step := range steps {
		t.Run(step.name+" runs the service's script", func(t *testing.T) {
			r, ex, _, out := fixtureRegistry(t)
			if err := step.run(r, ex, labtest.Service); err != nil {
				t.Fatalf("%s() error = %v", step.name, err)
			}
			if !strings.Contains(out.String(), step.want) {
				t.Errorf("%s() output = %q, want it to contain %q", step.name, out.String(), step.want)
			}
		})
		t.Run(step.name+" refuses a service that does not exist", func(t *testing.T) {
			r, ex, _, out := fixtureRegistry(t)
			err := step.run(r, ex, "ghost")
			if err == nil || !strings.Contains(err.Error(), `service "ghost" not found`) {
				t.Errorf("%s(ghost) error = %v", step.name, err)
			}
			if out.Len() != 0 {
				t.Errorf("%s(ghost) ran a script: %q", step.name, out.String())
			}
		})
		t.Run(step.name+" names a script the service lacks", func(t *testing.T) {
			r, ex, l, _ := fixtureRegistry(t)
			if err := os.Remove(filepath.Join(l.Root, "src", "services", labtest.Service, step.script)); err != nil {
				t.Fatal(err)
			}
			err := step.run(r, ex, labtest.Service)
			if err == nil || !strings.Contains(err.Error(), step.script+` not found for service "pager"`) {
				t.Errorf("%s() error = %v, want it to name the missing %s", step.name, err, step.script)
			}
		})
		t.Run(step.name+" reports a script that fails", func(t *testing.T) {
			r, ex, l, _ := fixtureRegistry(t)
			l.Write(t, "src/services/"+labtest.Service+"/"+step.script, "#!/usr/bin/env bash\nexit 4\n")
			if err := step.run(r, ex, labtest.Service); err == nil {
				t.Errorf("%s() returned no error for a script that exited 4", step.name)
			}
		})
	}
}
