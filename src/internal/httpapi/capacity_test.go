// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/snowops-oss/snowopslabs/internal/config"
	"github.com/snowops-oss/snowopslabs/internal/toolchain"
)

// colima4GB is a 2 CPU / 4 GiB engine whose machine has 2000 MiB in use. The
// meminfo rule comes first because the fake applies the first match and
// "info" is a substring of "/proc/meminfo".
func colima4GB() *toolchain.Fake {
	return toolchain.NewFake().
		WhenArgsContain("cat /proc/meminfo", "MemTotal: 4194304 kB\nMemAvailable: 2146304 kB\n", 0).
		WhenArgsContain("info", "2 4294967296\n", 0).
		WhenArgsContain("ps -q", "server-0\n", 0)
}

func TestServer_HandleCapacity(t *testing.T) {
	tests := []struct {
		name       string
		profile    string
		docker     *toolchain.Fake
		cancel     bool
		wantStatus int
		wantBody   CapacityResponse
	}{
		{
			name:       "reports size and use",
			profile:    "k3d",
			docker:     colima4GB(),
			wantStatus: http.StatusOK,
			wantBody:   CapacityResponse{CPUs: 2, MemoryMiB: 4096, UsedMiB: 2000, UsableMiB: 3481},
		},
		{
			name:       "incluster has no docker engine",
			profile:    "incluster",
			docker:     colima4GB(),
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "an unreachable docker is unavailable",
			profile:    "k3d",
			docker:     toolchain.NewFake().WhenArgsContain("info", "", 1),
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:    "an unreadable machine is unavailable",
			profile: "kind",
			docker: toolchain.NewFake().
				WhenArgsContain("cat /proc/meminfo", "", 1).
				WhenArgsContain("info", "2 4294967296\n", 0).
				WhenArgsContain("ps -q", "node\n", 0),
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "a cancelled request is unavailable",
			profile:    "k3d",
			docker:     colima4GB(),
			cancel:     true,
			wantStatus: http.StatusServiceUnavailable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{cfg: &config.Config{Profile: tt.profile, ClusterName: "snowops"}, docker: tt.docker}
			ctx, cancel := context.WithCancel(t.Context())
			if tt.cancel {
				cancel()
			} else {
				defer cancel()
			}
			req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/v2/capacity", nil)
			w := httptest.NewRecorder()
			s.handleCapacity(w, req)

			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d: %s", w.Code, tt.wantStatus, w.Body.String())
			}
			if tt.wantStatus != http.StatusOK {
				return
			}
			var got CapacityResponse
			if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
				t.Fatal(err)
			}
			if got != tt.wantBody {
				t.Errorf("body = %+v, want %+v", got, tt.wantBody)
			}
		})
	}
}
