// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	incsvc "github.com/sagar2395/snowopslabs/internal/service/incident"
	"github.com/sagar2395/snowopslabs/internal/store"
)

func TestTidyAfterFix(t *testing.T) {
	tests := []struct {
		name       string
		exitCode   int
		wantStatus store.Status
		wantAdvice bool
	}{
		{name: "resolve.sh runs as a recorded resolve", exitCode: 0, wantStatus: store.StatusSucceeded},
		{name: "a failed tidy-up says how to finish it", exitCode: 1, wantStatus: store.StatusFailed, wantAdvice: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, st := fakeIncidentEnv(t)
			f.WhenArgsContainStderr("resolve.sh", "", "stderr\n", tt.exitCode)

			var buf bytes.Buffer
			tidyAfterFix(dummyCmd(&buf), "oom-kill", incsvc.Target{Namespace: "go-api", Workload: "go-api"})

			out := buf.String()
			if !strings.Contains(out, "Tidying up") {
				t.Errorf("output does not announce the tidy-up: %q", out)
			}
			if got := strings.Contains(out, "labctl incident resolve oom-kill"); got != tt.wantAdvice {
				t.Errorf("recovery advice shown = %v, want %v; output: %q", got, tt.wantAdvice, out)
			}
			runs, err := st.ListRuns(context.Background(), store.RunFilter{Kind: incsvc.KindResolve})
			if err != nil {
				t.Fatalf("ListRuns: %v", err)
			}
			if len(runs) != 1 || runs[0].Target != "oom-kill" || runs[0].Status != tt.wantStatus {
				t.Fatalf("want one %s resolve of oom-kill, got %+v", tt.wantStatus, runs)
			}
		})
	}
}
