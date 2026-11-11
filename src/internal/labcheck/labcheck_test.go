// SPDX-License-Identifier: Apache-2.0

package labcheck

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/snowops-oss/snowopslabs/internal/config"
	"github.com/snowops-oss/snowopslabs/internal/workload"
)

func TestNewRunner(t *testing.T) {
	t.Setenv("PROMETHEUS_URL", "")
	cfg := &config.Config{Profile: "k3d", DomainSuffix: "k3d.local", HTTPPort: "80", ProjectRoot: "/lab"}
	r := NewRunner(cfg, workload.Workload{Name: "go-api", Namespace: "go-api"}, "/lab/scenarios/x")
	if r.PrometheusURL != "http://prometheus.k3d.local" {
		t.Errorf("PrometheusURL = %q", r.PrometheusURL)
	}
	if r.ScriptDir != "/lab/scenarios/x" {
		t.Errorf("ScriptDir = %q", r.ScriptDir)
	}
	if r.HTTPClient == nil || r.HTTPClient.Transport == nil {
		t.Error("a local lab needs the dialling client")
	}
	if !strings.Contains(strings.Join(r.Env, " "), "WORKLOAD_NAMESPACE=go-api") {
		t.Errorf("env = %v", r.Env)
	}
}

func TestPrometheusAndAlertmanagerURL(t *testing.T) {
	cfg := &config.Config{Profile: "k3d", DomainSuffix: "k3d.local", HTTPPort: "8080"}
	tests := []struct {
		name         string
		promEnv      string
		amEnv        string
		expectedProm string
		expectedAM   string
	}{
		{
			name:         "through the ingress port",
			expectedProm: "http://prometheus.k3d.local:8080",
			expectedAM:   "http://alertmanager.k3d.local:8080",
		},
		{
			name:         "environment overrides win",
			promEnv:      "http://localhost:9090",
			amEnv:        "http://localhost:9093",
			expectedProm: "http://localhost:9090",
			expectedAM:   "http://localhost:9093",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("PROMETHEUS_URL", tt.promEnv)
			t.Setenv("ALERTMANAGER_URL", tt.amEnv)
			if got := PrometheusURL(cfg); got != tt.expectedProm {
				t.Errorf("PrometheusURL = %q, want %q", got, tt.expectedProm)
			}
			if got := AlertmanagerURL(cfg); got != tt.expectedAM {
				t.Errorf("AlertmanagerURL = %q, want %q", got, tt.expectedAM)
			}
		})
	}
}

func TestScriptEnv(t *testing.T) {
	t.Setenv("PROMETHEUS_URL", "")
	t.Setenv("ALERTMANAGER_URL", "")
	cfg := &config.Config{Profile: "k3d", DomainSuffix: "k3d.local", HTTPPort: "8080", HTTPSPort: "8443"}
	env := strings.Join(ScriptEnv(cfg, workload.Workload{Name: "go-api"}), "\n")
	for _, want := range []string{
		"INGRESS_URL_SUFFIX=k3d.local:8080",
		"HTTPS_PORT=8443",
		"GRAFANA_URL=http://grafana.k3d.local:8080",
		"PROMETHEUS_URL=http://prometheus.k3d.local:8080",
		"WORKLOAD_NAME=go-api",
	} {
		if !strings.Contains(env, want) {
			t.Errorf("env missing %q:\n%s", want, env)
		}
	}
}

func TestHTTPClient(t *testing.T) {
	t.Run("reaches lab hosts on loopback without a proxy", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "host="+r.Host)
		}))
		t.Cleanup(srv.Close)
		_, port, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
		if err != nil {
			t.Fatal(err)
		}
		// A proxy that would swallow the request if it were used.
		t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
		t.Setenv("http_proxy", "http://127.0.0.1:1")

		cfg := &config.Config{Profile: "k3d", DomainSuffix: "lab.test", HTTPPort: port}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, cfg.IngressURL("grafana")+"/api/health", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := HTTPClient(cfg).Do(req)
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if expected := "host=grafana.lab.test:" + port; string(body) != expected {
			t.Errorf("body = %q, want %q (the Host header must stay the lab name)", body, expected)
		}
	})

	t.Run("other hosts keep the environment's proxy", func(t *testing.T) {
		t.Setenv("HTTPS_PROXY", "http://proxy.example:3128")
		cfg := &config.Config{Profile: "k3d", DomainSuffix: "lab.test"}
		tr, ok := HTTPClient(cfg).Transport.(*http.Transport)
		if !ok {
			t.Fatal("want an *http.Transport")
		}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://example.com/", nil)
		if err != nil {
			t.Fatal(err)
		}
		u, err := tr.Proxy(req)
		if err != nil || u == nil || u.Host != "proxy.example:3128" {
			t.Errorf("proxy = %v, %v; want proxy.example:3128", u, err)
		}
	})

	t.Run("incluster uses a plain client", func(t *testing.T) {
		cfg := &config.Config{Profile: "incluster", DomainSuffix: "lab.example.com"}
		if HTTPClient(cfg).Transport != nil {
			t.Error("incluster hostnames resolve through real DNS; no dial override")
		}
	})
}
