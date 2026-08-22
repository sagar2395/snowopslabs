// SPDX-License-Identifier: Apache-2.0
package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sagar2395/snowopslabs/internal/executor"
)

// Provider represents a platform component provider (e.g., traefik, nginx).
type Provider struct {
	Category     string // e.g., "ingress", "monitoring/metrics"
	Name         string // e.g., "traefik", "prometheus"
	Path         string // Filesystem path to the provider directory
	monitoringNS string // resolved monitoring namespace (set by Registry)
}

// HasScript checks if the provider has a specific script.
func (p *Provider) HasScript(name string) bool {
	_, err := os.Stat(filepath.Join(p.Path, name))
	return err == nil
}

// Namespace returns the Kubernetes namespace for this provider.
// Monitoring, logging, and tracing providers share the configured monitoring
// namespace (default "monitoring"). Other providers use their own name.
func (p *Provider) Namespace() string {
	top := p.Category
	if i := strings.Index(top, "/"); i >= 0 {
		top = top[:i]
	}
	switch top {
	case "monitoring", "logging", "tracing":
		if p.monitoringNS != "" {
			return p.monitoringNS
		}
		return "monitoring"
	default:
		return p.Name
	}
}

// Registry discovers and manages platform component providers.
type Registry struct {
	ProjectRoot  string
	monitoringNS string
	providers    map[string][]Provider // category -> providers
	stateDir     string                // .labctl/platform — install-intent markers
}

// NewRegistry scans the platform/ directory for available providers.
// The monitoring namespace defaults to "monitoring".
func NewRegistry(projectRoot string) *Registry {
	return NewRegistryWithNamespace(projectRoot, "monitoring")
}

// NewRegistryWithNamespace is like NewRegistry but uses the given namespace for
// monitoring, logging, and tracing providers (instead of "monitoring").
func NewRegistryWithNamespace(projectRoot, monitoringNS string) *Registry {
	if monitoringNS == "" {
		monitoringNS = "monitoring"
	}
	r := &Registry{
		ProjectRoot:  projectRoot,
		monitoringNS: monitoringNS,
		providers:    make(map[string][]Provider),
		stateDir:     filepath.Join(projectRoot, ".labctl", "platform"),
	}
	r.scan()
	return r
}

// --- install-intent tracking -------------------------------------------------
//
// Successful installs/uninstalls through the registry leave a marker in
// .labctl/platform/ so lab snapshot/reset (task 043) can know what labctl
// put on the cluster without probing it. Installs done outside labctl
// (raw make targets, manual helm) are not tracked — documented limitation.

func markerFile(category, name string) string {
	return strings.ReplaceAll(category, "/", "__") + "__" + name + ".installed"
}

func (r *Registry) markInstalled(category, name string) {
	if err := os.MkdirAll(r.stateDir, 0755); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(r.stateDir, markerFile(category, name)),
		[]byte(category+"/"+name+"\n"), 0644)
}

func (r *Registry) markUninstalled(category, name string) {
	_ = os.Remove(filepath.Join(r.stateDir, markerFile(category, name)))
}

// Installed returns the components installed through the registry, as sorted
// "category/provider" strings (read from marker file contents, so category
// nesting survives round-trips).
func (r *Registry) Installed() []string {
	entries, err := os.ReadDir(r.stateDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".installed") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(r.stateDir, e.Name()))
		if err != nil {
			continue
		}
		if v := strings.TrimSpace(string(data)); v != "" {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

// GetProviders returns all providers for a given category.
func (r *Registry) GetProviders(category string) []Provider {
	return r.providers[category]
}

// GetProvider returns a specific provider by category and name.
func (r *Registry) GetProvider(category, name string) (*Provider, error) {
	for _, p := range r.providers[category] {
		if p.Name == name {
			return &p, nil
		}
	}
	return nil, fmt.Errorf("provider %s/%s not found", category, name)
}

// Categories returns all discovered categories.
func (r *Registry) Categories() []string {
	var cats []string
	for k := range r.providers {
		cats = append(cats, k)
	}
	return cats
}

// Install runs the install.sh for a provider.
func (r *Registry) Install(category, name string, exec *executor.Executor) error {
	p, err := r.GetProvider(category, name)
	if err != nil {
		return err
	}
	scriptPath, err := filepath.Rel(r.ProjectRoot, filepath.Join(p.Path, "install.sh"))
	if err != nil {
		return err
	}
	if err := exec.RunScript(scriptPath); err != nil {
		return err
	}
	r.markInstalled(category, name)
	return nil
}

// InstallStreamed runs install.sh for a provider with output streaming.
func (r *Registry) InstallStreamed(category, name string, exec *executor.Executor) error {
	p, err := r.GetProvider(category, name)
	if err != nil {
		return err
	}
	scriptPath, err := filepath.Rel(r.ProjectRoot, filepath.Join(p.Path, "install.sh"))
	if err != nil {
		return err
	}
	if _, err = exec.RunScriptStreamed(fmt.Sprintf("Install %s/%s", category, name), scriptPath); err != nil {
		return err
	}
	r.markInstalled(category, name)
	return nil
}

// Uninstall runs the uninstall.sh for a provider.
func (r *Registry) Uninstall(category, name string, exec *executor.Executor) error {
	p, err := r.GetProvider(category, name)
	if err != nil {
		return err
	}
	script := filepath.Join(p.Path, "uninstall.sh")
	if _, err := os.Stat(script); os.IsNotExist(err) {
		return fmt.Errorf("uninstall.sh not found for %s/%s", category, name)
	}
	scriptPath, err := filepath.Rel(r.ProjectRoot, script)
	if err != nil {
		return err
	}
	if err := exec.RunScript(scriptPath); err != nil {
		return err
	}
	r.markUninstalled(category, name)
	return nil
}

// UninstallStreamed runs uninstall.sh for a provider with output streaming.
func (r *Registry) UninstallStreamed(category, name string, exec *executor.Executor) error {
	p, err := r.GetProvider(category, name)
	if err != nil {
		return err
	}
	script := filepath.Join(p.Path, "uninstall.sh")
	if _, err := os.Stat(script); os.IsNotExist(err) {
		return fmt.Errorf("uninstall.sh not found for %s/%s", category, name)
	}
	scriptPath, err := filepath.Rel(r.ProjectRoot, script)
	if err != nil {
		return err
	}
	if _, err = exec.RunScriptStreamed(fmt.Sprintf("Uninstall %s/%s", category, name), scriptPath); err != nil {
		return err
	}
	r.markUninstalled(category, name)
	return nil
}

// Status runs the status.sh for a provider.
func (r *Registry) Status(category, name string, exec *executor.Executor) error {
	p, err := r.GetProvider(category, name)
	if err != nil {
		return err
	}
	script := filepath.Join(p.Path, "status.sh")
	if _, err := os.Stat(script); os.IsNotExist(err) {
		return fmt.Errorf("status.sh not found for %s/%s", category, name)
	}
	scriptPath, err := filepath.Rel(r.ProjectRoot, script)
	if err != nil {
		return err
	}
	return exec.RunScript(scriptPath)
}

func (r *Registry) scan() {
	platformDir := filepath.Join(r.ProjectRoot, "platform")
	r.scanDir(platformDir, "")
}

func (r *Registry) scanDir(dir, prefix string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "_schema" {
			continue
		}

		fullPath := filepath.Join(dir, entry.Name())
		category := entry.Name()
		if prefix != "" {
			category = prefix + "/" + entry.Name()
		}

		// Check if this directory is a provider (has install.sh)
		if _, err := os.Stat(filepath.Join(fullPath, "install.sh")); err == nil {
			r.providers[prefix] = append(r.providers[prefix], Provider{
				Category:     prefix,
				Name:         entry.Name(),
				Path:         fullPath,
				monitoringNS: r.monitoringNS,
			})
		} else {
			// Recurse one level deeper
			r.scanDir(fullPath, category)
		}
	}
}
