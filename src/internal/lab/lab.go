// SPDX-License-Identifier: Apache-2.0
// Package lab implements lab-state snapshots and reset. A
// snapshot records *intent* — which platform components, apps, and
// scenarios were active — not cluster bytes. Restore replays the existing
// idempotent install paths; reset tears everything down to post-init
// (cluster + ingress only). Snapshots live in .labctl/snapshots/ (runtime
// state, never committed).
package lab

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/sagar2395/snowopslabs/internal/config"
	"github.com/sagar2395/snowopslabs/internal/k8s"
	"github.com/sagar2395/snowopslabs/internal/platform"
	"github.com/sagar2395/snowopslabs/internal/scenario"
)

// Snapshot is the declarative record of a lab's desired state.
type Snapshot struct {
	Name         string    `yaml:"name" json:"name"`
	TakenAt      time.Time `yaml:"takenAt" json:"takenAt"`
	Profile      string    `yaml:"profile,omitempty" json:"profile,omitempty"`
	DomainSuffix string    `yaml:"domainSuffix,omitempty" json:"domainSuffix,omitempty"`
	Platform     []string  `yaml:"platform" json:"platform"`   // category/provider
	Apps         []string  `yaml:"apps" json:"apps"`           // app names
	Scenarios    []string  `yaml:"scenarios" json:"scenarios"` // scenario names
}

var validSnapshotName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// Store persists snapshots under <projectRoot>/.labctl/snapshots/.
type Store struct {
	Dir string
}

// NewStore returns a snapshot store rooted at the project's runtime state dir.
func NewStore(projectRoot string) *Store {
	return &Store{Dir: filepath.Join(projectRoot, ".labctl", "snapshots")}
}

// Save writes the snapshot as YAML, overwriting any previous one of the
// same name (taking a snapshot is idempotent).
func (st *Store) Save(s *Snapshot) error {
	if !validSnapshotName.MatchString(s.Name) {
		return fmt.Errorf("invalid snapshot name %q: must match ^[a-zA-Z0-9_-]{1,64}$", s.Name)
	}
	if err := os.MkdirAll(st.Dir, 0755); err != nil {
		return err
	}
	data, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(st.Dir, s.Name+".yaml"), data, 0644)
}

// Load reads a snapshot by name.
func (st *Store) Load(name string) (*Snapshot, error) {
	if !validSnapshotName.MatchString(name) {
		return nil, fmt.Errorf("invalid snapshot name %q: must match ^[a-zA-Z0-9_-]{1,64}$", name)
	}
	data, err := os.ReadFile(filepath.Join(st.Dir, name+".yaml"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("snapshot %q not found", name)
		}
		return nil, err
	}
	var s Snapshot
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parsing snapshot %q: %w", name, err)
	}
	return &s, nil
}

// List returns all stored snapshots, newest first.
func (st *Store) List() ([]*Snapshot, error) {
	entries, err := os.ReadDir(st.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []*Snapshot
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		s, err := st.Load(strings.TrimSuffix(e.Name(), ".yaml"))
		if err != nil {
			continue // a corrupt snapshot must not hide the rest
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TakenAt.After(out[j].TakenAt) })
	return out, nil
}

// Delete removes a snapshot.
func (st *Store) Delete(name string) error {
	if !validSnapshotName.MatchString(name) {
		return fmt.Errorf("invalid snapshot name %q", name)
	}
	if err := os.Remove(filepath.Join(st.Dir, name+".yaml")); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("snapshot %q not found", name)
		}
		return err
	}
	return nil
}

// Collect assembles the current lab state: platform from the registry's
// install markers, scenarios from the scenario engine's state, and apps by
// live kubectl probe (so apps deployed outside labctl are captured too).
func Collect(ctx context.Context, name, profile, domainSuffix, projectRoot string,
	reg *platform.Registry, scenes *scenario.Engine) (*Snapshot, []string) {

	var warnings []string
	s := &Snapshot{
		Name:         name,
		TakenAt:      time.Now().UTC(),
		Profile:      profile,
		DomainSuffix: domainSuffix,
		Platform:     reg.Installed(),
	}

	for _, sc := range scenes.Status() {
		if sc.Active {
			s.Scenarios = append(s.Scenarios, sc.Name)
		}
	}
	sort.Strings(s.Scenarios)

	apps, err := config.ListApps(projectRoot)
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("listing apps: %v", err))
	}
	for _, app := range apps {
		ns := app
		if cfg, _ := config.LoadAppConfig(projectRoot, app); cfg != nil && cfg.Namespace != "" {
			ns = cfg.Namespace
		}
		status, err := k8s.GetAppStatus(ctx, app, ns)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("probing app %s: %v", app, err))
			continue
		}
		if status != nil && status.Deployed {
			s.Apps = append(s.Apps, app)
		}
	}
	sort.Strings(s.Apps)

	return s, warnings
}

// Action is one step of a restore or reset plan.
type Action struct {
	Kind   string `json:"kind"`   // platform-install | app-deploy | scenario-up | scenario-down | app-destroy | platform-uninstall | traffic-stop
	Target string `json:"target"` // category/provider, app name, or scenario name
}

func (a Action) String() string {
	if a.Target == "" {
		return a.Kind
	}
	return a.Kind + " " + a.Target
}

// categoryPriority orders platform installs: ingress carries everything
// else's URLs, monitoring carries the dashboards scenarios assume.
func categoryPriority(component string) int {
	switch strings.SplitN(component, "/", 2)[0] {
	case "ingress":
		return 0
	case "monitoring":
		return 1
	default:
		return 2
	}
}

func sortPlatform(components []string) []string {
	out := append([]string(nil), components...)
	sort.SliceStable(out, func(i, j int) bool {
		pi, pj := categoryPriority(out[i]), categoryPriority(out[j])
		if pi != pj {
			return pi < pj
		}
		return out[i] < out[j]
	})
	return out
}

// RestorePlan converts a snapshot into ordered actions: platform first
// (ingress → monitoring → rest), then apps, then scenarios. Every step is
// idempotent, so restoring over a partially-converged lab is safe.
func RestorePlan(s *Snapshot) []Action {
	var plan []Action
	for _, p := range sortPlatform(s.Platform) {
		plan = append(plan, Action{Kind: "platform-install", Target: p})
	}
	for _, a := range s.Apps {
		plan = append(plan, Action{Kind: "app-deploy", Target: a})
	}
	for _, sc := range s.Scenarios {
		plan = append(plan, Action{Kind: "scenario-up", Target: sc})
	}
	return plan
}

// ResetPlan tears the lab back to post-init: stop traffic, deactivate
// scenarios, destroy apps, uninstall platform components in reverse
// priority — keeping the ingress category (it is part of "post-init").
func ResetPlan(installedPlatform, deployedApps, activeScenarios []string) []Action {
	plan := []Action{{Kind: "traffic-stop"}}
	for _, sc := range activeScenarios {
		plan = append(plan, Action{Kind: "scenario-down", Target: sc})
	}
	for _, a := range deployedApps {
		plan = append(plan, Action{Kind: "app-destroy", Target: a})
	}
	sorted := sortPlatform(installedPlatform)
	for i := len(sorted) - 1; i >= 0; i-- {
		if categoryPriority(sorted[i]) == 0 { // keep ingress
			continue
		}
		plan = append(plan, Action{Kind: "platform-uninstall", Target: sorted[i]})
	}
	return plan
}
