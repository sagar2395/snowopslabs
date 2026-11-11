// SPDX-License-Identifier: Apache-2.0

package admission

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/snowops-oss/snowopslabs/internal/capacity"
	"github.com/snowops-oss/snowopslabs/internal/toolchain"
)

// docker4GB is a colima engine of 2 CPU / 4 GB whose machine has usedMiB in use.
func docker4GB(usedMiB string) *toolchain.Fake {
	used, err := strconv.Atoi(usedMiB)
	if err != nil {
		panic(err)
	}
	const totalKB = 4192256
	meminfo := fmt.Sprintf("MemTotal: %d kB\nMemAvailable: %d kB\n", totalKB, totalKB-used*1024)
	// The meminfo rule comes first: the fake applies the first match, and
	// "info" is a substring of "/proc/meminfo".
	return toolchain.NewFake().
		WhenArgsContain("cat /proc/meminfo", meminfo, 0).
		WhenArgsContain("context show", "colima\n", 0).
		WhenArgsContain("info", "2 4292870144\n", 0).
		WhenArgsContain("ps -q", "node1\n", 0)
}

func TestGate_Admit(t *testing.T) {
	kafka := capacity.Item{Kind: capacity.ItemPlatform, Name: "data/kafka"}
	footprints := capacity.Footprints{
		Baseline: 2000,
		Agent:    500,
		Platform: map[string]int{"data/kafka": 1200},
	}

	tests := []struct {
		name        string
		runner      *toolchain.Fake
		active      []capacity.Demand
		next        capacity.Demand
		agentsErr   error
		installed   []capacity.Item
		wantErr     []string
		wantOutput  []string
		wantAgentsN int
	}{
		{
			name:   "room to spare",
			runner: docker4GB("2100"),
			next:   capacity.Demand{Name: "secrets-management", OwnMiB: 300},
		},
		{
			name:    "no room blocks with the fix",
			runner:  docker4GB("2100"),
			next:    capacity.Demand{Name: "event-driven-arch", Items: []capacity.Item{kafka}, OwnMiB: 200},
			wantErr: []string{"Not enough memory for event-driven-arch", "colima stop && colima start"},
		},
		{
			name:      "an idle platform component is offered to make room",
			runner:    docker4GB("3000"),
			installed: []capacity.Item{kafka},
			next:      capacity.Demand{Name: "secrets-management", OwnMiB: 600},
			wantErr:   []string{"labctl platform down data/kafka"},
		},
		{
			name:      "a component the new scenario uses is not idle",
			runner:    docker4GB("3000"),
			installed: []capacity.Item{kafka},
			next:      capacity.Demand{Name: "event-driven-arch", Items: []capacity.Item{kafka}, OwnMiB: 400},
			wantErr:   []string{"Give Docker"},
		},
		{
			name:        "missing agents are added",
			runner:      docker4GB("2100"),
			next:        capacity.Demand{Name: "node-drain-drill", Agents: 2},
			wantOutput:  []string{"node-drain-drill needs 2 agent nodes; adding 1."},
			wantAgentsN: 2,
		},
		{
			name:       "warnings are printed and do not block",
			runner:     docker4GB("2100"),
			active:     []capacity.Demand{{Name: "observability-sre", App: "go-api"}},
			next:       capacity.Demand{Name: "autoscaling-under-load", App: "go-api"},
			wantOutput: []string{"Warning: observability-sre also uses go-api"},
		},
		{
			name:       "an unreachable docker skips the check",
			runner:     toolchain.NewFake().WhenArgsContain("info", "", 1),
			next:       capacity.Demand{Name: "anything", OwnMiB: 99999},
			wantOutput: []string{"skipping the capacity check"},
		},
		{
			name:      "an unreadable cluster is an error",
			runner:    docker4GB("2100"),
			next:      capacity.Demand{Name: "anything"},
			agentsErr: errors.New("connection refused"),
			wantErr:   []string{"counting agent nodes", "connection refused"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addedTo := 0
			g := &Gate{
				Footprints:   footprints,
				Runner:       tt.runner,
				Platform:     capacity.Platform{GOOS: "darwin"},
				UsageLabel:   "k3d.cluster=snowops",
				CanAddAgents: true,
				Active:       func(string) []capacity.Demand { return tt.active },
				Running:      func(context.Context, capacity.Item) bool { return false },
				Installed:    func(context.Context) []capacity.Item { return tt.installed },
				Agents:       func(context.Context) (int, error) { return 1, tt.agentsErr },
				AddAgents: func(_ context.Context, total int) error {
					addedTo = total
					return nil
				},
			}
			var out bytes.Buffer
			err := g.Admit(t.Context(), &out, tt.next)
			if len(tt.wantErr) == 0 && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for _, want := range tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Errorf("error should mention %q, got %v", want, err)
				}
			}
			for _, want := range tt.wantOutput {
				if !strings.Contains(out.String(), want) {
					t.Errorf("output should mention %q:\n%s", want, out.String())
				}
			}
			if addedTo != tt.wantAgentsN {
				t.Errorf("AddAgents called with %d, want %d", addedTo, tt.wantAgentsN)
			}
		})
	}
}

func TestGate_Release(t *testing.T) {
	tests := []struct {
		name         string
		canAdd       bool
		agents       int
		agentsErr    error
		removeErr    error
		remaining    []capacity.Demand
		wantRemoveTo int
		wantErr      string
	}{
		{name: "shrinks to what the remaining scenarios need", canAdd: true, agents: 2, remaining: []capacity.Demand{{Agents: 1}}, wantRemoveTo: 1},
		{name: "nothing active needs agents", canAdd: true, agents: 2, wantRemoveTo: 1},
		{name: "a remaining drill keeps its agents", canAdd: true, agents: 2, remaining: []capacity.Demand{{Agents: 2}}, wantRemoveTo: -1},
		{name: "a runtime that cannot change agents is left alone", agents: 2, wantRemoveTo: -1},
		{name: "an unreadable cluster is an error", canAdd: true, agentsErr: errors.New("refused"), wantRemoveTo: -1, wantErr: "counting agent nodes"},
		{name: "a failed removal is an error", canAdd: true, agents: 2, removeErr: errors.New("boom"), wantErr: "removing agent nodes: boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			removedTo := -1
			g := &Gate{
				CanAddAgents: tt.canAdd,
				MinAgents:    1,
				Agents:       func(context.Context) (int, error) { return tt.agents, tt.agentsErr },
				RemoveAgents: func(_ context.Context, total int) error {
					removedTo = total
					return tt.removeErr
				},
			}
			err := g.Release(t.Context(), tt.remaining)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			}
			if tt.wantErr == "" && removedTo != tt.wantRemoveTo {
				t.Errorf("RemoveAgents called with %d, want %d", removedTo, tt.wantRemoveTo)
			}
		})
	}
}

// A cancelled admission is refused, not waved through as "could not check":
// the caller stopped, so nothing must be installed on its behalf.
func TestGate_Admit_CancelledContext(t *testing.T) {
	added := false
	g := &Gate{
		Runner:       docker4GB("2100"),
		CanAddAgents: true,
		Active:       func(string) []capacity.Demand { return nil },
		Running:      func(context.Context, capacity.Item) bool { return false },
		Installed:    func(context.Context) []capacity.Item { return nil },
		Agents:       func(context.Context) (int, error) { return 1, nil },
		AddAgents:    func(context.Context, int) error { added = true; return nil },
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var out bytes.Buffer
	err := g.Admit(ctx, &out, capacity.Demand{Name: "node-drain-drill", Agents: 3})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Admit(cancelled) = %v, want context.Canceled", err)
	}
	if added {
		t.Error("a cancelled admission added agent nodes")
	}
	if strings.Contains(out.String(), "skipping the capacity check") {
		t.Errorf("a cancelled admission was treated as an unreachable Docker:\n%s", out.String())
	}
}
