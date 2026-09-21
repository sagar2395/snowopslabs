// SPDX-License-Identifier: Apache-2.0

package workload

import (
	"strings"
	"testing"

	"github.com/sagar2395/snowopslabs/pkg/checks"
)

func checkNames(cs []checks.Check) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Name
	}
	return out
}

func hasCheck(cs []checks.Check, name string) bool {
	for _, c := range cs {
		if c.Name == name {
			return true
		}
	}
	return false
}

// Every generated check must be a valid one, or `app verify` fails with a
// schema error instead of a contract result.
func TestChecksAreValid(t *testing.T) {
	c, err := ParseContract(map[string]string{
		KeyCapabilities: "prometheus-metrics,otlp-tracing,readiness-toggle",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, chk := range c.Checks(Default("go-api")) {
		if err := chk.Validate(); err != nil {
			t.Errorf("generated check %q is invalid: %v", chk.Name, err)
		}
		if chk.Remediation == "" {
			t.Errorf("generated check %q has no remediation", chk.Name)
		}
	}
}

// An app is never failed for lacking something it never claimed.
func TestChecksScaleWithCapabilities(t *testing.T) {
	tests := []struct {
		name         string
		capabilities string
		wantPresent  []string
		wantAbsent   []string
	}{
		{
			name:         "base contract only",
			capabilities: "",
			wantPresent:  []string{"workload-ready", "serves-contract-port", "liveness-probe-path", "readiness-probe-path"},
			wantAbsent:   []string{"request-metric-scraped"},
		},
		{
			name:         "metrics adds the promql check",
			capabilities: "prometheus-metrics",
			wantPresent:  []string{"workload-ready", "request-metric-scraped"},
		},
		{
			name:         "a conditional-promise capability adds no cluster check",
			capabilities: "otlp-tracing,readiness-toggle",
			wantPresent:  []string{"workload-ready"},
			wantAbsent:   []string{"request-metric-scraped"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, err := ParseContract(map[string]string{KeyCapabilities: tc.capabilities})
			if err != nil {
				t.Fatal(err)
			}
			got := c.Checks(Default("go-api"))
			for _, want := range tc.wantPresent {
				if !hasCheck(got, want) {
					t.Errorf("missing check %q; got %v", want, checkNames(got))
				}
			}
			for _, absent := range tc.wantAbsent {
				if hasCheck(got, absent) {
					t.Errorf("unexpected check %q; got %v", absent, checkNames(got))
				}
			}
		})
	}
}

// The checks must target the bound workload, not the app the contract came from.
func TestChecksTargetTheBinding(t *testing.T) {
	c, err := ParseContract(map[string]string{
		KeyPort: "9090", KeyRequestMetric: "micrometer_seconds", KeyCapabilities: "prometheus-metrics",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := c.Checks(Workload{Name: "java-api", Namespace: "team-a"})
	for _, chk := range got {
		if chk.Type == "kubectl" {
			if chk.Namespace != "team-a" {
				t.Errorf("check %q targets namespace %q, want team-a", chk.Name, chk.Namespace)
			}
			if !strings.Contains(chk.Resource, "java-api") {
				t.Errorf("check %q targets %q, want the java-api deployment", chk.Name, chk.Resource)
			}
		}
	}
	for _, chk := range got {
		switch chk.Name {
		case "serves-contract-port":
			if chk.Value != "9090" {
				t.Errorf("port check asserts %q, want the contract's 9090", chk.Value)
			}
		case "request-metric-scraped":
			if !strings.Contains(chk.Query, "micrometer_seconds_count") {
				t.Errorf("metric check queries %q, want the declared metric", chk.Query)
			}
			if !strings.Contains(chk.Query, `app="java-api"`) {
				t.Errorf("metric check queries %q, want it scoped to the bound app", chk.Query)
			}
		}
	}
}

// A binding with no explicit port must still assert the contract's port, not "".
func TestChecksUseContractPortNotBindingDefault(t *testing.T) {
	c, err := ParseContract(map[string]string{KeyPort: "3000"})
	if err != nil {
		t.Fatal(err)
	}
	for _, chk := range c.Checks(Workload{Name: "app", Namespace: "app"}) {
		if chk.Name == "serves-contract-port" && chk.Value != "3000" {
			t.Errorf("port check asserts %q, want 3000", chk.Value)
		}
	}
}

func TestUnverifiableCapabilities(t *testing.T) {
	tests := []struct {
		name, capabilities string
		want               []Capability
	}{
		{name: "none declared", capabilities: "", want: nil},
		{name: "a provable capability is not listed", capabilities: "prometheus-metrics", want: nil},
		{name: "readiness toggle cannot be proven read-only", capabilities: "readiness-toggle", want: []Capability{CapReadinessToggle}},
		{name: "tracing is a conditional promise", capabilities: "otlp-tracing", want: []Capability{CapOTLPTracing}},
		{name: "both, in declaration order", capabilities: "otlp-tracing,readiness-toggle", want: []Capability{CapOTLPTracing, CapReadinessToggle}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, err := ParseContract(map[string]string{KeyCapabilities: tc.capabilities})
			if err != nil {
				t.Fatal(err)
			}
			got := c.UnverifiableCapabilities()
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// Anything reported as unverifiable must say why, and anything graded must not
// claim to be unverifiable.
func TestWhyUnverifiable(t *testing.T) {
	for _, c := range []Capability{CapOTLPTracing, CapReadinessToggle} {
		if WhyUnverifiable(c) == "" {
			t.Errorf("%q is unverifiable but gives no reason", c)
		}
	}
	if WhyUnverifiable(CapPrometheusMetrics) != "" {
		t.Error("prometheus-metrics is graded by a check; it must not claim to be unverifiable")
	}
	// Every capability is either graded by a check or explained as unprovable —
	// none may fall silently between the two.
	for _, c := range AllCapabilities() {
		contract := Contract{Port: DefaultPort, HealthPath: DefaultHealthPath, ReadyPath: DefaultReadyPath,
			MetricsPath: DefaultMetricsPath, RequestMetric: DefaultMetric, Capabilities: []Capability{c}}
		graded := len(contract.Checks(Default("app"))) > len(Contract{Port: DefaultPort, HealthPath: DefaultHealthPath,
			ReadyPath: DefaultReadyPath, MetricsPath: DefaultMetricsPath, RequestMetric: DefaultMetric}.Checks(Default("app")))
		if !graded && WhyUnverifiable(c) == "" {
			t.Errorf("capability %q contributes no check and has no unprovable reason", c)
		}
	}
}
