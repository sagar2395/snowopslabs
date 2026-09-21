// SPDX-License-Identifier: Apache-2.0

package k8s

import "testing"

func TestAllContainersReady(t *testing.T) {
	tests := []struct {
		name  string
		ready string
		want  bool
	}{
		{"single container ready", "1/1", true},
		{"sidecar not ready", "1/2", false},
		{"both ready", "2/2", true},
		{"nothing ready", "0/1", false},
		{"no containers reported", "0/0", false},
		{"malformed", "ready", false},
		{"empty", "", false},
		{"non-numeric", "a/b", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := allContainersReady(tt.ready); got != tt.want {
				t.Errorf("allContainersReady(%q) = %v, want %v", tt.ready, got, tt.want)
			}
		})
	}
}
