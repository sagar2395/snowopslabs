// SPDX-License-Identifier: Apache-2.0

package scenario

import (
	"strings"
	"testing"

	schema "github.com/sagar2395/snowopslabs/pkg/scenario"

	"github.com/sagar2395/snowopslabs/internal/workload"
)

func contractWith(t *testing.T, caps string) workload.Contract {
	t.Helper()
	c, err := workload.ParseContract(map[string]string{workload.KeyCapabilities: caps})
	if err != nil {
		t.Fatalf("ParseContract(%q): %v", caps, err)
	}
	return c
}

// Preflight must reject a scenario the bound app cannot satisfy before anything
// installs, and must say which app to bind instead.
func TestPreflightCapabilityGate(t *testing.T) {
	tests := []struct {
		name     string
		declared string
		required []string
		wantErr  string
	}{
		{name: "no requirement passes against a bare app", declared: "", required: nil},
		{name: "satisfied requirement passes", declared: "prometheus-metrics", required: []string{"prometheus-metrics"}},
		{
			name:     "every requirement satisfied",
			declared: "prometheus-metrics,otlp-tracing",
			required: []string{"prometheus-metrics", "otlp-tracing"},
		},
		{
			name:     "missing capability is rejected",
			declared: "prometheus-metrics",
			required: []string{"otlp-tracing"},
			wantErr:  "does not declare: otlp-tracing",
		},
		{
			name:     "every gap is named at once",
			declared: "",
			required: []string{"prometheus-metrics", "readiness-toggle"},
			wantErr:  "prometheus-metrics, readiness-toggle",
		},
		{
			name:     "an unknown capability name is an authoring error",
			declared: "prometheus-metrics",
			required: []string{"warp-drive"},
			wantErr:  "unknown capability",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := &Engine{
				ProjectRoot: t.TempDir(),
				Workload:    workload.Default("go-api"),
				Contract:    contractWith(t, tc.declared),
			}
			s := &Scenario{
				Name:          "demo",
				Prerequisites: schema.Prerequisites{Capabilities: tc.required},
			}
			err := e.Preflight(s)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Preflight() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Preflight() = nil, want an error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err, tc.wantErr)
			}
		})
	}
}

// The failure must name the bound app, so the reader knows what to change.
func TestPreflightCapabilityGateNamesTheBinding(t *testing.T) {
	e := &Engine{
		ProjectRoot: t.TempDir(),
		Workload:    workload.Default("echo-server"),
		Contract:    contractWith(t, ""),
	}
	err := e.Preflight(&Scenario{
		Name:          "demo",
		Prerequisites: schema.Prerequisites{Capabilities: []string{"prometheus-metrics"}},
	})
	if err == nil {
		t.Fatal("Preflight() = nil, want an error")
	}
	for _, want := range []string{"echo-server", "APP_NAME", "labctl app list"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}
