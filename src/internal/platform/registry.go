// SPDX-License-Identifier: Apache-2.0

// Package platform discovers the platform component providers under
// platform/<category>/<provider>/ and runs their install, uninstall and status
// scripts.
package platform

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sagar2395/snowopslabs/internal/executor"
	"gopkg.in/yaml.v3"
)

// Provider represents a platform component provider (e.g., traefik, nginx).
type Provider struct {
	Category     string // e.g., "ingress", "monitoring/metrics"
	Name         string // e.g., "traefik", "prometheus"
	Path         string // Filesystem path to the provider directory
	monitoringNS string // resolved monitoring namespace (set by Registry)
	declaredNS   string // namespace from _interface.yaml, when declared
}

// HasScript checks if the provider has a specific script.
func (p *Provider) HasScript(name string) bool {
	_, err := os.Stat(filepath.Join(p.Path, name))
	return err == nil
}

// Namespace returns the Kubernetes namespace this provider installs into.
// Monitoring, logging and tracing providers share the configured monitoring
// namespace. Otherwise it is the namespace declared in _interface.yaml, if
// any (istio uses istio-system, nginx uses ingress-nginx), else the provider's
// name.
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
	}
	if p.declaredNS != "" {
		return p.declaredNS
	}
	return p.Name
}

// SharesNamespace reports whether this provider shares its namespace with
// other providers, as monitoring, logging and tracing providers do. For those,
// the namespace existing does not show the provider is installed; callers must
// check something specific to it, such as its Helm release.
func (p *Provider) SharesNamespace() bool {
	top := p.Category
	if i := strings.Index(top, "/"); i >= 0 {
		top = top[:i]
	}
	switch top {
	case "monitoring", "logging", "tracing":
		return true
	default:
		return false
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

// --- install markers ---------------------------------------------------------
//
// A successful install through the registry writes a marker file in
// .labctl/platform/, and an uninstall removes it, so lab snapshot and reset
// know what labctl installed without asking the cluster. Components installed
// any other way (make targets, manual helm) have no marker.

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

// IsExclusive reports whether only one provider of a category may be installed
// at a time (ingress, mesh) rather than several together (secrets: vault and
// external-secrets). A category is exclusive when its
// platform/<category>/_interface.yaml says `selection: exclusive`.
func (r *Registry) IsExclusive(category string) bool {
	// Exclusivity is a property of the top-level category (e.g. "monitoring"
	// for "monitoring/metrics").
	top := category
	if i := strings.Index(top, "/"); i >= 0 {
		top = top[:i]
	}
	data, err := os.ReadFile(filepath.Join(r.ProjectRoot, "platform", top, "_interface.yaml"))
	if err != nil {
		return false
	}
	var iface struct {
		Selection string `yaml:"selection"`
	}
	if err := yaml.Unmarshal(data, &iface); err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(iface.Selection), "exclusive")
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
	if _, err := os.Stat(script); errors.Is(err, fs.ErrNotExist) {
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
	if _, err := os.Stat(script); errors.Is(err, fs.ErrNotExist) {
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
	if _, err := os.Stat(script); errors.Is(err, fs.ErrNotExist) {
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

		// A directory with install.sh is a provider.
		if _, err := os.Stat(filepath.Join(fullPath, "install.sh")); err == nil {
			p := Provider{
				Category:     prefix,
				Name:         entry.Name(),
				Path:         fullPath,
				monitoringNS: r.monitoringNS,
			}
			p.declaredNS = p.Meta().Namespace
			r.providers[prefix] = append(r.providers[prefix], p)
		} else {
			// Otherwise it is a category, possibly nested (monitoring/metrics).
			r.scanDir(fullPath, category)
		}
	}
}
