// SPDX-License-Identifier: Apache-2.0
package auth

import "testing"

func TestRequiresOperator(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		want   bool
	}{
		{"GET platform is read", "GET", "/api/platform", false},
		{"POST platform up", "POST", "/api/platform/up", true},
		{"POST platform component", "POST", "/api/platform/component/ingress/traefik/up", true},
		{"POST runtime activate", "POST", "/api/runtimes/k3d/activate", true},
		{"POST lab reset", "POST", "/api/lab/reset", true},
		{"DELETE lab snapshot", "DELETE", "/api/lab/snapshots/foo", true},
		{"POST app build", "POST", "/api/apps/go-api/build", true},
		{"POST service up", "POST", "/api/services/redis/up", true},
		{"POST challenge complete is participant-ok", "POST", "/api/challenges/complete", false},
		{"POST incident inject is participant-ok", "POST", "/api/incidents/foo/inject", false},
		{"POST scenario up is participant-ok", "POST", "/api/scenarios/foo/up", false},
		{"POST learn start is participant-ok", "POST", "/api/learn/paths/foo/start", false},
		{"GET status is read", "GET", "/api/status", false},
		{"OPTIONS preflight", "OPTIONS", "/api/platform/up", false},
		{"prefix lookalike not matched", "POST", "/api/platformx/up", false},
		{"POST traffic start is operator", "POST", "/api/traffic/start", true},
		{"POST traffic stop is operator", "POST", "/api/traffic/stop", true},
		{"GET traffic is read", "GET", "/api/traffic", false},
		// The v2 prefix must gate identically to v1 (regression guard: the
		// operator check used to match only the literal /api/... prefix).
		{"v2 POST platform up", "POST", "/api/v2/platform/up", true},
		{"v2 POST runtime activate", "POST", "/api/v2/runtimes/k3d/activate", true},
		{"v2 POST traffic start is operator", "POST", "/api/v2/traffic/start", true},
		{"v2 POST scenario up is participant-ok", "POST", "/api/v2/scenarios/foo/up", false},
		{"v2 GET platform is read", "GET", "/api/v2/platform", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RequiresOperator(tt.method, tt.path); got != tt.want {
				t.Errorf("RequiresOperator(%q,%q) = %v, want %v", tt.method, tt.path, got, tt.want)
			}
		})
	}
}

func TestAuthorize(t *testing.T) {
	tests := []struct {
		name   string
		role   string
		method string
		path   string
		want   bool
	}{
		{"operator can mutate platform", RoleOperator, "POST", "/api/platform/up", true},
		{"operator can do anything", RoleOperator, "DELETE", "/api/lab/snapshots/x", true},
		{"participant denied platform mutation", RoleParticipant, "POST", "/api/platform/up", false},
		{"participant denied lab reset", RoleParticipant, "POST", "/api/lab/reset", false},
		{"participant allowed challenge complete", RoleParticipant, "POST", "/api/challenges/complete", true},
		{"participant allowed reads", RoleParticipant, "GET", "/api/platform", true},
		{"participant allowed scenario up", RoleParticipant, "POST", "/api/scenarios/foo/up", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Authorize(tt.role, tt.method, tt.path); got != tt.want {
				t.Errorf("Authorize(%q,%q,%q) = %v, want %v", tt.role, tt.method, tt.path, got, tt.want)
			}
		})
	}
}
