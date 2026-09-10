// SPDX-License-Identifier: Apache-2.0
package checks

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A check killed by its own deadline must not read as a verdict about the
// cluster: "signal: killed" is what the learner saw before, and it named
// neither the timeout nor the way to raise it.
func TestRun_TimeoutErrorExplainsItself(t *testing.T) {
	tests := []struct {
		name           string
		timeoutSeconds int
		wantFragment   string
	}{
		{"default timeout", 0, "timed out after 1s"},
		{"per-check timeout", 2, "timed out after 2s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Runner{
				DefaultTimeout: time.Second,
				Exec: func(ctx context.Context, _ string, _ ...string) (string, error) {
					<-ctx.Done()
					return "", ctx.Err()
				},
			}
			res := r.Run(context.Background(), Check{
				Name:           "slow",
				Type:           TypeKubectl,
				Resource:       "deployment/x",
				Namespace:      "default",
				JSONPath:       "{.status.readyReplicas}",
				Operator:       ">=",
				Value:          "1",
				TimeoutSeconds: tt.timeoutSeconds,
			})
			if !strings.Contains(res.Error, tt.wantFragment) {
				t.Errorf("error = %q, want it to contain %q", res.Error, tt.wantFragment)
			}
			if !strings.Contains(res.Error, "timeoutSeconds") {
				t.Errorf("error = %q, want it to name the timeoutSeconds field", res.Error)
			}
		})
	}
}
