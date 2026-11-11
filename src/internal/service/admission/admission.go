// SPDX-License-Identifier: Apache-2.0

// Package admission decides whether a scenario or fault may start, given the
// Docker engine's size, what the lab already runs and what the new activation
// adds. It blocks an activation that would run the engine out of memory,
// explaining how to make room, and adds agent nodes an activation needs.
package admission

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/snowops-oss/snowopslabs/internal/capacity"
	"github.com/snowops-oss/snowopslabs/internal/toolchain"
)

// Gate answers admission questions for one lab. Its function fields are the
// lab facts it depends on, so tests can supply them without a cluster.
type Gate struct {
	// Footprints are the measured costs of what the lab can run.
	Footprints capacity.Footprints
	// Runner reaches the docker CLI.
	Runner toolchain.Runner
	// Platform is the operating system labctl runs on.
	Platform capacity.Platform
	// UsageLabel selects the lab's node containers, e.g. "k3d.cluster=snowops".
	UsageLabel string
	// CanAddAgents reports whether the runtime can add agent nodes live.
	CanAddAgents bool
	// MinAgents is the agent count the cluster was created with; Release never
	// shrinks below it.
	MinAgents int

	// Active returns the Demand of every active scenario except the named one.
	Active func(except string) []capacity.Demand
	// Running reports whether item already runs in the lab.
	Running func(ctx context.Context, item capacity.Item) bool
	// Installed lists the platform components installed beyond the baseline.
	Installed func(ctx context.Context) []capacity.Item
	// Agents returns the cluster's current agent count.
	Agents func(ctx context.Context) (int, error)
	// AddAgents grows the cluster to total agent nodes.
	AddAgents func(ctx context.Context, total int) error
	// RemoveAgents shrinks the cluster towards total agent nodes, removing only
	// agents AddAgents created and keeping any it cannot remove safely.
	RemoveAgents func(ctx context.Context, total int) error
}

// Admit evaluates next against the lab. It writes warnings to out, adds any
// agent nodes next needs, and returns an error listing what blocks it.
func (g *Gate) Admit(ctx context.Context, out io.Writer, next capacity.Demand) error {
	res, err := capacity.Probe(ctx, g.Runner)
	if err != nil {
		// A caller that stopped is refused: nothing is installed for it.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		// Without the engine's size there is nothing to judge; the activation
		// itself reports an unreachable Docker.
		fmt.Fprintf(out, "Warning: skipping the capacity check: %v\n", err)
		return nil
	}
	lab, err := g.lab(ctx, res, next)
	if err != nil {
		return err
	}

	active := g.Active(next.Name)
	lab.Installed = g.Installed(ctx)
	v := capacity.Evaluate(g.Footprints, lab, active, next)
	for _, w := range v.Warnings {
		fmt.Fprintf(out, "Warning: %s\n", w)
	}
	if !v.Allowed() {
		return errors.New(strings.Join(v.Problems, "\n\n"))
	}
	if v.AddAgents > 0 {
		total := lab.Agents + v.AddAgents
		fmt.Fprintf(out, "%s needs %d agent nodes; adding %d.\n", next.Name, total, v.AddAgents)
		if err := g.AddAgents(ctx, total); err != nil {
			return fmt.Errorf("adding agent nodes: %w", err)
		}
	}
	return nil
}

// Release shrinks the cluster to the most agent nodes any remaining demand
// needs, but not below MinAgents, so agents added for a drill do not outlive
// it.
func (g *Gate) Release(ctx context.Context, remaining []capacity.Demand) error {
	if !g.CanAddAgents || g.RemoveAgents == nil {
		return nil
	}
	need := g.MinAgents
	for _, d := range remaining {
		need = max(need, d.Agents)
	}
	have, err := g.Agents(ctx)
	if err != nil {
		return fmt.Errorf("counting agent nodes: %w", err)
	}
	if have <= need {
		return nil
	}
	if err := g.RemoveAgents(ctx, need); err != nil {
		return fmt.Errorf("removing agent nodes: %w", err)
	}
	return nil
}

// lab gathers the live facts Evaluate needs. A failed usage reading is not
// fatal: the footprints alone still give an estimate.
func (g *Gate) lab(ctx context.Context, res capacity.Resources, next capacity.Demand) (capacity.Lab, error) {
	agents, err := g.Agents(ctx)
	if err != nil {
		return capacity.Lab{}, fmt.Errorf("counting agent nodes: %w", err)
	}
	used, err := capacity.Usage(ctx, g.Runner, g.UsageLabel)
	if err != nil {
		used = 0
	}
	running := make(map[capacity.Item]bool, len(next.Items))
	for _, item := range next.Items {
		running[item] = g.Running(ctx, item)
	}
	return capacity.Lab{
		Engine:       res,
		Host:         capacity.DetectHost(ctx, g.Runner, g.Platform),
		UsedMiB:      used,
		Running:      running,
		Agents:       agents,
		CanAddAgents: g.CanAddAgents,
	}, nil
}
