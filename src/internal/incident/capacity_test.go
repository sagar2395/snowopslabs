// SPDX-License-Identifier: Apache-2.0

package incident

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snowops-oss/snowopslabs/internal/capacity"
	"github.com/snowops-oss/snowopslabs/internal/executor"
	"github.com/snowops-oss/snowopslabs/pkg/scenario"
)

func TestEngine_Demand(t *testing.T) {
	e, _ := testEngine(t)
	tests := []struct {
		name     string
		req      scenario.Requirements
		expected int
		wantErr  bool
	}{
		{name: "own memory counts", req: scenario.Requirements{Memory: "512Mi"}, expected: 512},
		{name: "no requirements", req: scenario.Requirements{}, expected: 0},
		{name: "unreadable memory", req: scenario.Requirements{Memory: "lots"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := e.Demand(&Fault{Name: "noisy", Requirements: tt.req})
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if d.Name != "incident noisy" || d.App != "go-api" || len(d.Items) != 1 {
				t.Errorf("Demand = %+v; want it to name the fault and the app it breaks", d)
			}
			if d.OwnMiB != tt.expected {
				t.Errorf("OwnMiB = %d, want %d", d.OwnMiB, tt.expected)
			}
		})
	}
}

func TestEngine_Inject_AsksAdmit(t *testing.T) {
	tests := []struct {
		name       string
		admitErr   error
		wantErr    string
		wantBroken bool
	}{
		{name: "admitted", wantBroken: true},
		{name: "blocked", admitErr: errors.New("not enough memory"), wantErr: "fault-a cannot start: not enough memory"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, root := testEngine(t, "fault-a")
			var asked capacity.Demand
			e.Admit = func(_ context.Context, _ io.Writer, next capacity.Demand) error {
				asked = next
				return tt.admitErr
			}
			_, err := e.Inject("fault-a", executor.New(root), false, false)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Inject error = %v, want %q", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatalf("Inject: %v", err)
			}
			if asked.Name != "incident fault-a" {
				t.Errorf("Admit was asked about %+v", asked)
			}
			_, statErr := os.Stat(filepath.Join(root, "incidents", "fault-a", "BROKEN"))
			if broken := statErr == nil; broken != tt.wantBroken {
				t.Errorf("inject.sh ran = %v, want %v", broken, tt.wantBroken)
			}
		})
	}
}
