// SPDX-License-Identifier: Apache-2.0
package cli

import "testing"

func TestPlatformState(t *testing.T) {
	tests := []struct {
		name   string
		ready  int
		total  int
		exists bool
		want   string
	}{
		{"namespace absent", 0, 0, false, "  [not installed]"},
		{"namespace but nothing in it", 0, 0, true, "  [no workloads]"},
		{"controller crashlooping", 0, 1, true, "  [degraded 0/1 ready]"},
		{"one of three down", 2, 3, true, "  [degraded 2/3 ready]"},
		{"all ready", 3, 3, true, "  [running]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := platformState(tt.ready, tt.total, tt.exists); got != tt.want {
				t.Errorf("platformState(%d, %d, %v) = %q, want %q",
					tt.ready, tt.total, tt.exists, got, tt.want)
			}
		})
	}
}
