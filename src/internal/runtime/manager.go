// SPDX-License-Identifier: Apache-2.0
package runtime

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sagar2395/snowopslabs/internal/executor"
	"github.com/sagar2395/snowopslabs/internal/k8s"
)

// RuntimeInfo describes a discovered runtime and its status.
type RuntimeInfo struct {
	Name    string `json:"name"`
	Active  bool   `json:"active"`  // context exists in kubeconfig and is reachable
	Current bool   `json:"current"` // is the current kubectl context
}

// Manager discovers and manages cluster runtimes (k3d, kind, incluster).
type Manager struct {
	ProjectRoot string
	ClusterName string
	runtimes    []string

	// injectable for tests
	getCurrentCtx func(ctx context.Context) (string, error)
	contextExists func(ctx context.Context, name string) (bool, error)
}

// NewManager scans the runtimes/ directory for available runtimes.
func NewManager(projectRoot, clusterName string) *Manager {
	m := &Manager{
		ProjectRoot: projectRoot,
		ClusterName: clusterName,
		getCurrentCtx: func(ctx context.Context) (string, error) {
			return k8s.GetCurrentContext(ctx)
		},
		contextExists: func(ctx context.Context, name string) (bool, error) {
			out, err := k8s.RunKubectl(ctx, "config", "get-contexts", name, "--no-headers")
			return err == nil && strings.TrimSpace(out) != "", nil
		},
	}
	m.scan()
	return m
}

// List returns all discovered runtimes with their current status.
func (m *Manager) List() []RuntimeInfo {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	currentCtx, _ := m.getCurrentCtx(ctx)

	var infos []RuntimeInfo
	for _, name := range m.runtimes {
		info := RuntimeInfo{Name: name}

		expectedCtx := m.expectedContext(name)
		if expectedCtx == "" {
			// No reliable context name available — report as not active.
			infos = append(infos, info)
			continue
		}

		if currentCtx == expectedCtx {
			info.Current = true
			info.Active = true
		} else {
			checkCtx, checkCancel := context.WithTimeout(context.Background(), 2*time.Second)
			exists, _ := m.contextExists(checkCtx, expectedCtx)
			checkCancel()
			if exists {
				info.Active = true
			}
		}

		infos = append(infos, info)
	}
	return infos
}

// Names returns the names of all discovered runtimes.
func (m *Manager) Names() []string {
	return m.runtimes
}

// Activate provisions a runtime by running its up.sh script.
func (m *Manager) Activate(name string, exec *executor.Executor) error {
	if !m.exists(name) {
		return fmt.Errorf("runtime %q not found", name)
	}
	scriptPath := filepath.Join("runtimes", name, "up.sh")
	_, err := exec.RunScriptStreamed(fmt.Sprintf("Activate %s", name), scriptPath, m.ClusterName)
	return err
}

// ActivateWith provisions a runtime using a pre-allocated action ID so the
// HTTP 202 response and WebSocket action_start event share the same ID.
func (m *Manager) ActivateWith(actionID, name string, exec *executor.Executor) error {
	if !m.exists(name) {
		return fmt.Errorf("runtime %q not found", name)
	}
	scriptPath := filepath.Join("runtimes", name, "up.sh")
	return exec.RunScriptStreamedWith(actionID, fmt.Sprintf("Activate %s", name), scriptPath, m.ClusterName)
}

// Deactivate tears down a runtime by running its down.sh script.
func (m *Manager) Deactivate(name string, exec *executor.Executor) error {
	if !m.exists(name) {
		return fmt.Errorf("runtime %q not found", name)
	}
	scriptPath := filepath.Join("runtimes", name, "down.sh")
	_, err := exec.RunScriptStreamed(fmt.Sprintf("Deactivate %s", name), scriptPath, m.ClusterName)
	return err
}

// DeactivateWith tears down a runtime using a pre-allocated action ID.
func (m *Manager) DeactivateWith(actionID, name string, exec *executor.Executor) error {
	if !m.exists(name) {
		return fmt.Errorf("runtime %q not found", name)
	}
	scriptPath := filepath.Join("runtimes", name, "down.sh")
	return exec.RunScriptStreamedWith(actionID, fmt.Sprintf("Deactivate %s", name), scriptPath, m.ClusterName)
}

func (m *Manager) scan() {
	runtimesDir := filepath.Join(m.ProjectRoot, "runtimes")
	entries, err := os.ReadDir(runtimesDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		upScript := filepath.Join(runtimesDir, entry.Name(), "up.sh")
		if _, err := os.Stat(upScript); err == nil {
			m.runtimes = append(m.runtimes, entry.Name())
		}
	}
}

func (m *Manager) exists(name string) bool {
	for _, r := range m.runtimes {
		if r == name {
			return true
		}
	}
	return false
}

// expectedContext returns the kubectl context name expected for a runtime.
// Returns "" if the context name cannot be determined reliably.
func (m *Manager) expectedContext(name string) string {
	switch name {
	case "k3d":
		return "k3d-" + m.ClusterName
	default:
		// For cloud runtimes the context name varies per deployment.
		// Read KUBECONFIG_CONTEXT from runtime.env; if absent, report unknown.
		envFile := filepath.Join(m.ProjectRoot, "runtimes", name, "runtime.env")
		if ctx := readEnvKey(envFile, "KUBECONFIG_CONTEXT"); ctx != "" {
			return ctx
		}
		return ""
	}
}

// readEnvKey parses a shell env file (KEY=value lines) and returns the value
// for the given key, or "" if not found or the file cannot be read.
func readEnvKey(path, key string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()

	prefix := key + "="
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}
