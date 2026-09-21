// SPDX-License-Identifier: Apache-2.0

package workload

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseContractDefaults(t *testing.T) {
	// An app that declares nothing still has a usable base interface, but claims
	// no capability — a claim must be explicit.
	got, err := ParseContract(nil)
	if err != nil {
		t.Fatalf("ParseContract(nil): %v", err)
	}
	want := Contract{
		Port:          DefaultPort,
		HealthPath:    DefaultHealthPath,
		ReadyPath:     DefaultReadyPath,
		MetricsPath:   DefaultMetricsPath,
		RequestMetric: DefaultMetric,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseContract(nil) = %+v, want %+v", got, want)
	}
	if len(got.Capabilities) != 0 {
		t.Errorf("undeclared app claimed capabilities: %v", got.Capabilities)
	}
}

func TestParseContract(t *testing.T) {
	tests := []struct {
		name string
		in   map[string]string
		want Contract
	}{
		{
			name: "explicit values override defaults",
			in: map[string]string{
				KeyPort: "9090", KeyHealthPath: "/healthz", KeyReadyPath: "/readyz",
				KeyMetricsPath: "/prom", KeyRequestMetric: "custom_seconds",
			},
			want: Contract{Port: "9090", HealthPath: "/healthz", ReadyPath: "/readyz",
				MetricsPath: "/prom", RequestMetric: "custom_seconds"},
		},
		{
			name: "capabilities parsed and sorted",
			in:   map[string]string{KeyCapabilities: "readiness-toggle,prometheus-metrics"},
			want: Contract{Port: DefaultPort, HealthPath: DefaultHealthPath, ReadyPath: DefaultReadyPath,
				MetricsPath: DefaultMetricsPath, RequestMetric: DefaultMetric,
				Capabilities: []Capability{CapPrometheusMetrics, CapReadinessToggle}},
		},
		{
			name: "whitespace and duplicates tolerated",
			in:   map[string]string{KeyCapabilities: " prometheus-metrics , prometheus-metrics ,, "},
			want: Contract{Port: DefaultPort, HealthPath: DefaultHealthPath, ReadyPath: DefaultReadyPath,
				MetricsPath: DefaultMetricsPath, RequestMetric: DefaultMetric,
				Capabilities: []Capability{CapPrometheusMetrics}},
		},
		{
			name: "blank values fall back to defaults",
			in:   map[string]string{KeyPort: "   ", KeyHealthPath: ""},
			want: Contract{Port: DefaultPort, HealthPath: DefaultHealthPath, ReadyPath: DefaultReadyPath,
				MetricsPath: DefaultMetricsPath, RequestMetric: DefaultMetric},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseContract(tc.in)
			if err != nil {
				t.Fatalf("ParseContract: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParseContractErrors(t *testing.T) {
	tests := []struct {
		name    string
		in      map[string]string
		wantErr string
	}{
		{name: "unknown capability", in: map[string]string{KeyCapabilities: "warp-drive"}, wantErr: "unknown capability"},
		{name: "capability typo names the vocabulary", in: map[string]string{KeyCapabilities: "prometheus_metrics"}, wantErr: "prometheus-metrics"},
		{name: "non-numeric port", in: map[string]string{KeyPort: "http"}, wantErr: KeyPort},
		{name: "port out of range", in: map[string]string{KeyPort: "70000"}, wantErr: KeyPort},
		{name: "zero port", in: map[string]string{KeyPort: "0"}, wantErr: KeyPort},
		{name: "health path without slash", in: map[string]string{KeyHealthPath: "health"}, wantErr: KeyHealthPath},
		{name: "ready path without slash", in: map[string]string{KeyReadyPath: "ready"}, wantErr: KeyReadyPath},
		{
			name:    "metrics capability with a bad metrics path",
			in:      map[string]string{KeyCapabilities: "prometheus-metrics", KeyMetricsPath: "metrics"},
			wantErr: KeyMetricsPath,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseContract(tc.in)
			if err == nil {
				t.Fatalf("ParseContract(%v) = nil error, want %q", tc.in, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err, tc.wantErr)
			}
		})
	}
}

// A blank APP_REQUEST_METRIC falls back to the semconv name the contract
// mandates, so an app claiming metrics never ends up without one.
func TestBlankMetricFallsBackToSemconv(t *testing.T) {
	c, err := ParseContract(map[string]string{KeyCapabilities: "prometheus-metrics", KeyRequestMetric: " "})
	if err != nil {
		t.Fatalf("ParseContract: %v", err)
	}
	if c.RequestMetric != DefaultMetric {
		t.Errorf("RequestMetric = %q, want the semconv default %q", c.RequestMetric, DefaultMetric)
	}
}

// Validate also guards contracts built in code rather than parsed, where the
// metric genuinely can be empty.
func TestValidateRejectsMetricsCapabilityWithoutMetric(t *testing.T) {
	c := Contract{
		Port: DefaultPort, HealthPath: DefaultHealthPath, ReadyPath: DefaultReadyPath,
		MetricsPath: DefaultMetricsPath, RequestMetric: "",
		Capabilities: []Capability{CapPrometheusMetrics},
	}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), KeyRequestMetric) {
		t.Errorf("Validate() = %v, want an error naming %s", err, KeyRequestMetric)
	}
}

// An unknown capability set directly on the struct must not slip past Validate.
func TestValidateRejectsUnknownCapability(t *testing.T) {
	c := Contract{
		Port: DefaultPort, HealthPath: DefaultHealthPath, ReadyPath: DefaultReadyPath,
		MetricsPath: DefaultMetricsPath, RequestMetric: DefaultMetric,
		Capabilities: []Capability{"warp-drive"},
	}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "warp-drive") {
		t.Errorf("Validate() = %v, want an error naming the unknown capability", err)
	}
}

func TestHasAndMissing(t *testing.T) {
	c, err := ParseContract(map[string]string{KeyCapabilities: "prometheus-metrics,otlp-tracing"})
	if err != nil {
		t.Fatal(err)
	}
	if !c.Has(CapPrometheusMetrics) || !c.Has(CapOTLPTracing) {
		t.Error("Has() denied a declared capability")
	}
	if c.Has(CapReadinessToggle) {
		t.Error("Has() claimed an undeclared capability")
	}
	tests := []struct {
		name     string
		required []Capability
		want     []Capability
	}{
		{name: "all satisfied", required: []Capability{CapPrometheusMetrics}, want: nil},
		{name: "none required", required: nil, want: nil},
		{name: "one gap", required: []Capability{CapReadinessToggle}, want: []Capability{CapReadinessToggle}},
		{
			name:     "every gap reported at once",
			required: []Capability{CapPrometheusMetrics, CapReadinessToggle},
			want:     []Capability{CapReadinessToggle},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.Missing(tc.required); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Missing(%v) = %v, want %v", tc.required, got, tc.want)
			}
		})
	}
}

// The binding must take its port and metric from the app's declaration, or a
// scenario templating {{.WorkloadPort}} silently uses a compiled-in guess.
func TestContractWorkload(t *testing.T) {
	c, err := ParseContract(map[string]string{
		KeyPort: "9090", KeyRequestMetric: "micrometer_http_seconds",
		KeyCapabilities: "prometheus-metrics",
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, appName, namespace string
		want                     Workload
	}{
		{
			name: "explicit namespace", appName: "java-api", namespace: "team-a",
			want: Workload{Name: "java-api", Namespace: "team-a", Port: "9090", Metric: "micrometer_http_seconds"},
		},
		{
			name: "namespace defaults to the name", appName: "java-api", namespace: "",
			want: Workload{Name: "java-api", Namespace: "java-api", Port: "9090", Metric: "micrometer_http_seconds"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.Workload(tc.appName, tc.namespace); got != tc.want {
				t.Errorf("Workload() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestCapabilityVocabulary(t *testing.T) {
	all := AllCapabilities()
	if len(all) == 0 {
		t.Fatal("vocabulary is empty")
	}
	for i := 1; i < len(all); i++ {
		if all[i-1] >= all[i] {
			t.Errorf("AllCapabilities() is not sorted: %v", all)
		}
	}
	for _, c := range all {
		if !c.Valid() {
			t.Errorf("%q is listed but not valid", c)
		}
		if c.Describe() == "" {
			t.Errorf("%q has no description", c)
		}
	}
	if got := Capability("nope"); got.Valid() || got.Describe() != "" {
		t.Error("an unknown capability reported as valid or documented")
	}
}
