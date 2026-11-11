// SPDX-License-Identifier: Apache-2.0

package scenario

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/snowops-oss/snowopslabs/internal/capacity"
)

const demandScenario = `name: kafka-lab
displayName: Kafka Lab
description: x
category: testing
requirements:
  memory: 300Mi
  agents: 2
  cpus: 4
prerequisites:
  apps:
    - "{{.WorkloadName}}"
  platform:
    - ingress
    - monitoring/metrics
    - data/kafka
components:
  - name: loki
    type: helm
    chart: grafana/loki
    namespace: "{{.MonitoringNamespace}}"
  - name: promtail
    type: helm
    chart: grafana/promtail
    platformValues: logging/loki/promtail-values.yaml
    adopt: true
  - name: seed
    type: script
    script: seed.sh
`

func TestEngine_Demand(t *testing.T) {
	root := t.TempDir()
	writeScenario(t, root, "kafka-lab", demandScenario)
	eng := NewEngine(root, "k3d.local", "k3d")
	eng.MonitoringNamespace = "observability"
	s, err := eng.Get("kafka-lab")
	if err != nil {
		t.Fatal(err)
	}

	got, err := eng.Demand(s)
	if err != nil {
		t.Fatal(err)
	}
	expectedItems := []capacity.Item{
		{Kind: capacity.ItemPlatform, Name: "data/kafka"},
		{Kind: capacity.ItemRelease, Name: "observability/loki"},
		{Kind: capacity.ItemPlatform, Name: "logging/loki"},
		{Kind: capacity.ItemApp, Name: "go-api"},
	}
	if !slices.Equal(got.Items, expectedItems) {
		t.Errorf("Items = %v, want %v (baseline platform and non-helm components excluded, adopted release as its platform component)", got.Items, expectedItems)
	}
	if got.OwnMiB != 300 || got.Agents != 2 || got.CPUs != 4 || got.App != "go-api" {
		t.Errorf("Demand = %+v", got)
	}
}

func TestAdoptedFrom(t *testing.T) {
	tests := []struct {
		name     string
		comp     Component
		expected string
		ok       bool
	}{
		{name: "component directory", comp: Component{Adopt: true, PlatformValues: "tracing/tempo"}, expected: "tracing/tempo", ok: true},
		{name: "file inside it", comp: Component{Adopt: true, PlatformValues: "logging/loki/promtail-values.yaml"}, expected: "logging/loki", ok: true},
		{name: "not adopted", comp: Component{PlatformValues: "tracing/tempo"}},
		{name: "no platform values", comp: Component{Adopt: true}},
		{name: "bare category", comp: Component{Adopt: true, PlatformValues: "mesh"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := adoptedFrom(&tt.comp)
			if got != tt.expected || ok != tt.ok {
				t.Errorf("adoptedFrom = %q, %v; want %q, %v", got, ok, tt.expected, tt.ok)
			}
		})
	}
}

func TestEngine_Demand_UnreadableMemoryKeepsSharedItems(t *testing.T) {
	eng := NewEngine(t.TempDir(), "k3d.local", "k3d")
	s := &Scenario{
		Name:          "broken",
		Requirements:  Requirements{Memory: "lots"},
		Prerequisites: Prerequisites{Platform: []string{"data/kafka"}},
	}
	got, err := eng.Demand(s)
	if err == nil {
		t.Fatal("expected an error for an unreadable memory requirement")
	}
	if len(got.Items) != 1 {
		t.Errorf("shared items must survive the error, got %v", got.Items)
	}
}

func TestEngine_ActiveDemands(t *testing.T) {
	root := t.TempDir()
	writeScenario(t, root, "kafka-lab", demandScenario)
	writeScenario(t, root, "plain", "name: plain\ndisplayName: Plain\ndescription: x\ncategory: testing\n")
	writeScenario(t, root, "idle", "name: idle\ndisplayName: Idle\ndescription: x\ncategory: testing\n")
	eng := NewEngine(root, "k3d.local", "k3d")
	for _, name := range []string{"kafka-lab", "plain"} {
		if err := eng.markActive(name, nil); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name     string
		except   string
		expected []string
	}{
		{name: "every active scenario", except: "", expected: []string{"kafka-lab", "plain"}},
		{name: "without the one being started", except: "plain", expected: []string{"kafka-lab"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, d := range eng.ActiveDemands(tt.except) {
				got = append(got, d.Name)
			}
			slices.Sort(got)
			if !slices.Equal(got, tt.expected) {
				t.Errorf("ActiveDemands(%q) = %v, want %v", tt.except, got, tt.expected)
			}
		})
	}
}

func TestEngine_Up_AsksAdmit(t *testing.T) {
	yaml := `name: gated
displayName: Gated
description: x
category: testing
requirements:
  memory: 1Gi
components:
  - name: step
    type: script
    script: step.sh
`
	tests := []struct {
		name        string
		admitErr    error
		wantErr     string
		wantActive  bool
		wantInstall bool
	}{
		{name: "admitted", wantActive: true, wantInstall: true},
		{name: "blocked", admitErr: errors.New("not enough memory"), wantErr: "gated cannot start: not enough memory"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeScenario(t, root, "gated", yaml)
			if err := os.WriteFile(filepath.Join(root, "scenarios", "gated", "step.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil { //nolint:gosec // test script must be executable
				t.Fatal(err)
			}
			eng := NewEngine(root, "k3d.local", "k3d")
			eng.SetOutput(io.Discard)
			var asked capacity.Demand
			eng.Admit = func(_ context.Context, _ io.Writer, next capacity.Demand) error {
				asked = next
				return tt.admitErr
			}

			rec := &recordingExec{}
			err := eng.Up("gated", rec, false)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Up error = %v, want %q", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatalf("Up: %v", err)
			}
			if asked.Name != "gated" || asked.OwnMiB != 1024 {
				t.Errorf("Admit was asked about %+v", asked)
			}
			if got := eng.isActive("gated"); got != tt.wantActive {
				t.Errorf("active = %v, want %v", got, tt.wantActive)
			}
			if got := len(rec.calls) > 0; got != tt.wantInstall {
				t.Errorf("components installed = %v, want %v", got, tt.wantInstall)
			}
		})
	}
}

func TestEngine_Down_CallsRelease(t *testing.T) {
	tests := []struct {
		name       string
		releaseErr error
		wantOutput string
	}{
		{name: "released"},
		{name: "a failed release is a warning", releaseErr: errors.New("drain failed"), wantOutput: "Warning: drain failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeScenario(t, root, "drill", "name: drill\ndisplayName: Drill\ndescription: x\ncategory: testing\n")
			eng := NewEngine(root, "k3d.local", "k3d")
			var out strings.Builder
			eng.SetOutput(&out)
			if err := eng.markActive("drill", nil); err != nil {
				t.Fatal(err)
			}
			var remaining []capacity.Demand
			called := false
			eng.Release = func(_ context.Context, r []capacity.Demand) error {
				called, remaining = true, r
				return tt.releaseErr
			}

			if err := eng.Down("drill", &recordingExec{}); err != nil {
				t.Fatalf("Down: %v", err)
			}
			if !called || len(remaining) != 0 {
				t.Errorf("Release called = %v with %v, want called with no remaining scenarios", called, remaining)
			}
			if !strings.Contains(out.String(), tt.wantOutput) {
				t.Errorf("output should mention %q:\n%s", tt.wantOutput, out.String())
			}
		})
	}
}
