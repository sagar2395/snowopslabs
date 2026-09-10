// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"strings"
	"testing"

	"github.com/sagar2395/snowopslabs/internal/config"
	"github.com/sagar2395/snowopslabs/internal/scenario"
	"github.com/sagar2395/snowopslabs/internal/workload"
)

// A check script grades the bound workload, so it needs the same workload
// identity a component script gets. Without it a check can only hardcode an app
// name, which is what the workload binding exists to remove (ADR-0014).
func TestNewCheckRunner_CarriesWorkloadEnv(t *testing.T) {
	oldCfg, oldScenes := cfg, scenes
	t.Cleanup(func() { cfg, scenes = oldCfg, oldScenes })

	cfg = &config.Config{
		DomainSuffix:        "test.local",
		MonitoringNamespace: "observability",
		ProjectRoot:         "/tmp/project",
	}
	scenes = scenario.NewEngine(cfg.ProjectRoot, cfg.DomainSuffix, "k3d")
	scenes.Workload = workload.Workload{
		Name:      "java-api",
		Namespace: "apps",
		Port:      "9090",
		Metric:    "http_server_request_duration_seconds",
	}

	env := newCheckRunner().Env

	want := map[string]string{
		"DOMAIN_SUFFIX":        "test.local",
		"MONITORING_NAMESPACE": "observability",
		"PROJECT_ROOT":         "/tmp/project",
		"PROMETHEUS_URL":       "http://prometheus.test.local",
		"WORKLOAD_NAME":        "java-api",
		"WORKLOAD_NAMESPACE":   "apps",
		"WORKLOAD_PORT":        "9090",
		"WORKLOAD_METRIC":      "http_server_request_duration_seconds",
	}

	got := map[string]string{}
	for _, kv := range env {
		k, v, found := strings.Cut(kv, "=")
		if !found {
			t.Fatalf("env entry %q is not KEY=VALUE", kv)
		}
		got[k] = v
	}

	for k, v := range want {
		if got[k] != v {
			t.Errorf("env %s = %q, want %q", k, got[k], v)
		}
	}
}
