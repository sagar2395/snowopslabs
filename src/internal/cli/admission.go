// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"context"
	"log/slog"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/snowops-oss/snowopslabs/internal/capacity"
	"github.com/snowops-oss/snowopslabs/internal/config"
	"github.com/snowops-oss/snowopslabs/internal/k8s"
	"github.com/snowops-oss/snowopslabs/internal/service/admission"
	"github.com/snowops-oss/snowopslabs/internal/toolchain"
)

// newAdmissionGate builds the capacity gate for the configured lab, or nil for
// a runtime whose memory is not the Docker engine's (incluster).
func newAdmissionGate() *admission.Gate {
	label, ok := capacity.NodeLabel(cfg.Profile, cfg.ClusterName)
	if !ok {
		return nil
	}
	footprints, err := capacity.LoadFootprints(filepath.Join(cfg.ProjectRoot, "config", "footprints.yaml"))
	if err != nil {
		slog.Warn("capacity footprints unreadable; judging by live usage only", "err", err)
	}
	return &admission.Gate{
		Footprints:   footprints,
		Runner:       toolchain.NewExec(),
		Platform:     capacity.Platform{GOOS: runtime.GOOS, WSL: isWSL()},
		UsageLabel:   label,
		CanAddAgents: cfg.Profile == "k3d",
		MinAgents:    configuredAgents(),
		Active:       scenes.ActiveDemands,
		Running:      itemRunning,
		Installed:    installedPlatform,
		Agents:       k8s.AgentCount,
		AddAgents: func(_ context.Context, total int) error {
			return scriptExec.RunScript(filepath.Join("runtimes", cfg.Profile, "add-agents.sh"), strconv.Itoa(total))
		},
		RemoveAgents: func(_ context.Context, total int) error {
			return scriptExec.RunScript(filepath.Join("runtimes", cfg.Profile, "remove-agents.sh"), strconv.Itoa(total))
		},
	}
}

// configuredAgents is the agent count the cluster was created with (AGENTS),
// or 1, up.sh's default, when unset or unreadable.
func configuredAgents() int {
	n, err := strconv.Atoi(cfg.Agents)
	if err != nil || n < 0 {
		return 1
	}
	return n
}

// itemRunning reports whether a release, platform component or app already
// runs in the cluster, so the capacity check does not charge for it twice.
func itemRunning(ctx context.Context, item capacity.Item) bool {
	switch item.Kind {
	case capacity.ItemRelease:
		ns, name, ok := strings.Cut(item.Name, "/")
		return ok && k8s.HelmReleaseDeployed(ctx, ns, name)
	case capacity.ItemPlatform:
		installed, _ := platformPrereqInstalled(ctx, item.Name)
		return installed
	case capacity.ItemApp:
		ns := item.Name
		if appCfg, err := config.LoadAppConfig(cfg.ProjectRoot, item.Name); err == nil && appCfg.Namespace != "" {
			ns = appCfg.Namespace
		}
		status, err := k8s.GetAppStatus(ctx, item.Name, ns)
		return err == nil && status != nil && status.Deployed
	default:
		return false
	}
}

// installedPlatform lists the platform components installed beyond the
// baseline `labctl init` sets up (ingress, metrics and Grafana).
func installedPlatform(ctx context.Context) []capacity.Item {
	items := []capacity.Item{}
	for _, category := range reg.Categories() {
		if category == "ingress" || category == "monitoring/metrics" {
			continue
		}
		providers := reg.GetProviders(category)
		for i := range providers {
			p := &providers[i]
			if category == "monitoring" && p.Name == "grafana" {
				continue
			}
			if providerInstalled(ctx, p) {
				items = append(items, capacity.Item{Kind: capacity.ItemPlatform, Name: category + "/" + p.Name})
			}
		}
	}
	return items
}

// attachAdmissionGate attaches the capacity gate to both engines, so every
// activation path (CLI, web UI, challenges) is checked the same way and a
// deactivated scenario gives back the agent nodes only it needed.
func attachAdmissionGate() {
	gate := newAdmissionGate()
	if gate == nil {
		return
	}
	scenes.Admit = gate.Admit
	scenes.Release = gate.Release
	incEng.Admit = gate.Admit
}
