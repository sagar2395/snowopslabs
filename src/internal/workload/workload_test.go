// SPDX-License-Identifier: Apache-2.0
package workload

import (
	"strings"
	"testing"
)

func TestDefault(t *testing.T) {
	got := Default("go-api")
	want := Workload{Name: "go-api", Namespace: "go-api", Port: DefaultPort, Metric: DefaultMetric}
	if got != want {
		t.Errorf("Default(%q) = %+v, want %+v", "go-api", got, want)
	}
}

func TestWithDefaults(t *testing.T) {
	tests := []struct {
		name string
		in   Workload
		want Workload
	}{
		{
			name: "name only",
			in:   Workload{Name: "java-api"},
			want: Workload{Name: "java-api", Namespace: "java-api", Port: DefaultPort, Metric: DefaultMetric},
		},
		{
			name: "explicit namespace is kept",
			in:   Workload{Name: "java-api", Namespace: "team-a"},
			want: Workload{Name: "java-api", Namespace: "team-a", Port: DefaultPort, Metric: DefaultMetric},
		},
		{
			name: "explicit port and metric are kept",
			in:   Workload{Name: "dotnet-api", Namespace: "dotnet-api", Port: "5000", Metric: "custom_duration_seconds"},
			want: Workload{Name: "dotnet-api", Namespace: "dotnet-api", Port: "5000", Metric: "custom_duration_seconds"},
		},
		{
			name: "fully empty stays nameless",
			in:   Workload{},
			want: Workload{Namespace: "", Port: DefaultPort, Metric: DefaultMetric},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.WithDefaults(); got != tc.want {
				t.Errorf("WithDefaults() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestServiceAndURL(t *testing.T) {
	tests := []struct {
		name    string
		in      Workload
		wantSvc string
		wantURL string
	}{
		{
			name:    "conventional binding",
			in:      Default("go-api"),
			wantSvc: "go-api.go-api.svc.cluster.local",
			wantURL: "http://go-api.go-api.svc.cluster.local:8080/",
		},
		{
			name:    "custom namespace and port",
			in:      Workload{Name: "api", Namespace: "team-a", Port: "9000"},
			wantSvc: "api.team-a.svc.cluster.local",
			wantURL: "http://api.team-a.svc.cluster.local:9000/",
		},
		{name: "no name", in: Workload{Namespace: "x"}, wantSvc: "", wantURL: ""},
		{name: "no namespace", in: Workload{Name: "x"}, wantSvc: "", wantURL: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.Service(); got != tc.wantSvc {
				t.Errorf("Service() = %q, want %q", got, tc.wantSvc)
			}
			if got := tc.in.URL(); got != tc.wantURL {
				t.Errorf("URL() = %q, want %q", got, tc.wantURL)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		in      Workload
		wantErr string
	}{
		{name: "conventional binding is valid", in: Default("go-api")},
		{name: "no name", in: Workload{Namespace: "ns", Port: "8080"}, wantErr: "no name"},
		{name: "blank name", in: Workload{Name: "  ", Namespace: "ns", Port: "8080"}, wantErr: "no name"},
		{name: "no namespace", in: Workload{Name: "api", Port: "8080"}, wantErr: "no namespace"},
		{name: "no port", in: Workload{Name: "api", Namespace: "ns"}, wantErr: "no port"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.in.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() = nil, want error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err, tc.wantErr)
			}
		})
	}
}
