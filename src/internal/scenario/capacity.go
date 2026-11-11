// SPDX-License-Identifier: Apache-2.0

package scenario

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/snowops-oss/snowopslabs/internal/capacity"
)

// AdmitFunc decides whether an activation may go ahead given what it adds to
// the lab. It writes any warnings to out and returns an error to block it.
type AdmitFunc func(ctx context.Context, out io.Writer, next capacity.Demand) error

// ReleaseFunc is told, after a scenario is deactivated, what the scenarios
// still active need, so the lab can give back what only that scenario used.
type ReleaseFunc func(ctx context.Context, remaining []capacity.Demand) error

// baselinePlatform are the platform components `labctl init` installs. The
// capacity baseline already includes them, so scenarios naming them add
// nothing.
var baselinePlatform = map[string]bool{
	"ingress":            true,
	"monitoring/metrics": true,
	"monitoring/grafana": true,
}

// Demand is what activating s against the engine's current workload adds to
// the lab: its platform components beyond the baseline, its Helm releases, its
// apps and its own requirements. The returned Demand carries every shared item
// even when its own memory figure cannot be read.
func (e *Engine) Demand(s *Scenario) (capacity.Demand, error) {
	d := capacity.Demand{
		Name:      s.Name,
		Items:     []capacity.Item{},
		CPUs:      s.Requirements.CPUs,
		Agents:    s.Requirements.Agents,
		Exclusive: s.Requirements.Exclusive,
		App:       e.Workload.Name,
	}
	for _, p := range s.Prerequisites.Platform {
		if !baselinePlatform[p] {
			d.Items = append(d.Items, capacity.Item{Kind: capacity.ItemPlatform, Name: p})
		}
	}
	for _, comp := range s.AllComponents() {
		if comp.Type != "helm" {
			continue
		}
		if owner, ok := adoptedFrom(&comp); ok {
			d.Items = append(d.Items, capacity.Item{Kind: capacity.ItemPlatform, Name: owner})
			continue
		}
		ns := e.componentNamespace(&comp, "default")
		d.Items = append(d.Items, capacity.Item{Kind: capacity.ItemRelease, Name: ns + "/" + comp.Name})
	}
	for _, app := range e.ResolvedPrereqApps(s) {
		d.Items = append(d.Items, capacity.Item{Kind: capacity.ItemApp, Name: app})
	}
	own, err := s.Requirements.MemoryMiB()
	if err != nil {
		return d, fmt.Errorf("scenario %s: %w", s.Name, err)
	}
	d.OwnMiB = own
	return d, nil
}

// adoptedFrom is the platform component ("logging/loki") an adopted release
// belongs to, taken from its platformValues path. Bringing the scenario down
// leaves such a release installed, so it counts as that platform component.
func adoptedFrom(comp *Component) (string, bool) {
	if !comp.Adopt || comp.PlatformValues == "" {
		return "", false
	}
	parts := strings.SplitN(comp.PlatformValues, "/", 3)
	if len(parts) < 2 {
		return "", false
	}
	return parts[0] + "/" + parts[1], true
}

// ActiveDemands is the Demand of every active scenario other than except,
// each against the app it was activated with.
func (e *Engine) ActiveDemands(except string) []capacity.Demand {
	entries, err := os.ReadDir(e.stateDir())
	if err != nil {
		return []capacity.Demand{}
	}
	demands := make([]capacity.Demand, 0, len(entries))
	for _, en := range entries {
		name, ok := strings.CutSuffix(en.Name(), ".active")
		if !ok || en.IsDir() || name == except {
			continue
		}
		s, err := e.Get(name)
		if err != nil {
			continue // a marker for a scenario that is no longer in the catalog
		}
		demands = append(demands, e.activeDemand(s))
	}
	return demands
}

// activeDemand is the Demand of an active scenario, bound to the app recorded
// at its activation.
func (e *Engine) activeDemand(s *Scenario) capacity.Demand {
	defer e.withActivationWorkload(s.Name)()
	// Requirements are validated when a scenario loads, so an error here only
	// loses the scenario's own figure; its shared items still count.
	d, _ := e.Demand(s)
	return d
}
