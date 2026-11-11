// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"testing"

	"github.com/snowops-oss/snowopslabs/internal/config"
)

func TestConfiguredAgents(t *testing.T) {
	oldCfg := cfg
	t.Cleanup(func() { cfg = oldCfg })
	tests := []struct {
		name     string
		input    string
		expected int
	}{
		{name: "set", input: "2", expected: 2},
		{name: "unset", input: "", expected: 1},
		{name: "not a number", input: "two", expected: 1},
		{name: "negative", input: "-1", expected: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg = &config.Config{Agents: tt.input}
			if got := configuredAgents(); got != tt.expected {
				t.Errorf("configuredAgents() = %d, want %d", got, tt.expected)
			}
		})
	}
}
