// SPDX-License-Identifier: Apache-2.0
package services

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/sagar2395/snowopslabs/internal/executor"
)

// Service represents a shared service (e.g., redis, postgres).
type Service struct {
	Name string // directory name under services/
	Path string // absolute filesystem path
}

// HasScript checks if the service has a specific script.
func (s *Service) HasScript(name string) bool {
	_, err := os.Stat(filepath.Join(s.Path, name))
	return err == nil
}

// Registry discovers and manages shared services.
type Registry struct {
	ProjectRoot string
	services    []Service
}

// NewRegistry scans the services/ directory for available services.
func NewRegistry(projectRoot string) *Registry {
	r := &Registry{
		ProjectRoot: projectRoot,
	}
	r.scan()
	return r
}

// List returns all discovered services.
func (r *Registry) List() []Service {
	return r.services
}

// Get returns a specific service by name.
func (r *Registry) Get(name string) (*Service, error) {
	for _, s := range r.services {
		if s.Name == name {
			return &s, nil
		}
	}
	return nil, fmt.Errorf("service %q not found", name)
}

// Install runs install.sh for a service, streaming output via the broadcaster.
func (r *Registry) Install(name string, exec *executor.Executor) error {
	return r.runScript(name, "install.sh", "Service install: "+name, exec)
}

// Uninstall runs uninstall.sh for a service, streaming output via the broadcaster.
func (r *Registry) Uninstall(name string, exec *executor.Executor) error {
	return r.runScript(name, "uninstall.sh", "Service uninstall: "+name, exec)
}

// Status runs status.sh for a service, streaming output via the broadcaster.
func (r *Registry) Status(name string, exec *executor.Executor) error {
	return r.runScript(name, "status.sh", "Service status: "+name, exec)
}

func (r *Registry) runScript(name, script, label string, exec *executor.Executor) error {
	s, err := r.Get(name)
	if err != nil {
		return err
	}
	fullPath := filepath.Join(s.Path, script)
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		return fmt.Errorf("%s not found for service %q", script, name)
	}
	rel, err := filepath.Rel(r.ProjectRoot, fullPath)
	if err != nil {
		return err
	}
	_, err = exec.RunScriptStreamed(label, rel)
	return err
}

func (r *Registry) scan() {
	servicesDir := filepath.Join(r.ProjectRoot, "services")
	entries, err := os.ReadDir(servicesDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		fullPath := filepath.Join(servicesDir, entry.Name())
		// A service must have install.sh
		if _, err := os.Stat(filepath.Join(fullPath, "install.sh")); err == nil {
			r.services = append(r.services, Service{
				Name: entry.Name(),
				Path: fullPath,
			})
		}
	}
}
