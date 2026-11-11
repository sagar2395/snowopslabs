// SPDX-License-Identifier: Apache-2.0

// Package labcheck builds the check runner every grading path uses - scenario
// verify, incident status, learning modules, challenge submit, compare - so
// they all reach the lab the same way: through the ingress port the runtime
// actually bound, with the same script environment.
package labcheck

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/snowops-oss/snowopslabs/internal/config"
	"github.com/snowops-oss/snowopslabs/internal/workload"
	"github.com/snowops-oss/snowopslabs/pkg/checks"
)

// NewRunner returns a check runner for the lab. scriptDir is where relative
// check scripts resolve (a scenario's directory, or the project root).
func NewRunner(cfg *config.Config, w workload.Workload, scriptDir string) *checks.Runner {
	r := checks.NewRunner()
	r.HTTPClient = HTTPClient(cfg)
	r.ScriptDir = scriptDir
	r.PrometheusURL = PrometheusURL(cfg)
	r.Env = ScriptEnv(cfg, w)
	return r
}

// PrometheusURL is PROMETHEUS_URL when set, else Prometheus through the lab
// ingress.
func PrometheusURL(cfg *config.Config) string {
	if u := os.Getenv("PROMETHEUS_URL"); u != "" {
		return u
	}
	return cfg.IngressURL("prometheus")
}

// AlertmanagerURL is ALERTMANAGER_URL when set, else Alertmanager through the
// lab ingress.
func AlertmanagerURL(cfg *config.Config) string {
	if u := os.Getenv("ALERTMANAGER_URL"); u != "" {
		return u
	}
	return cfg.IngressURL("alertmanager")
}

// ScriptEnv is the environment check scripts run with.
func ScriptEnv(cfg *config.Config, w workload.Workload) []string {
	return []string{
		"DOMAIN_SUFFIX=" + cfg.DomainSuffix,
		"INGRESS_URL_SUFFIX=" + cfg.IngressURLSuffix(),
		"HTTP_PORT=" + cfg.HTTPPort,
		"HTTPS_PORT=" + cfg.HTTPSPort,
		"MONITORING_NAMESPACE=" + cfg.MonitoringNamespace,
		"PROJECT_ROOT=" + cfg.ProjectRoot,
		"LAB_STATE_DIR=" + cfg.StateDir,
		// The same Prometheus, Grafana and Alertmanager the Go checks use.
		"PROMETHEUS_URL=" + PrometheusURL(cfg),
		"GRAFANA_URL=" + cfg.IngressURL("grafana"),
		"ALERTMANAGER_URL=" + AlertmanagerURL(cfg),
		"WORKLOAD_NAME=" + w.Name,
		"WORKLOAD_NAMESPACE=" + w.Namespace,
		"WORKLOAD_PORT=" + w.Port,
		"WORKLOAD_METRIC=" + w.Metric,
	}
}

// HTTPClient returns a client for lab URLs. On a local runtime every host
// under the lab's domain suffix is dialled on 127.0.0.1 directly and never
// through a proxy, so grading does not depend on /etc/hosts entries, on a
// corporate HTTP_PROXY, or on the multi-second mDNS lookup macOS makes for
// *.local names.
func HTTPClient(cfg *config.Config) *http.Client {
	if !cfg.LocalIngress() {
		return &http.Client{}
	}
	suffix := strings.ToLower(cfg.DomainSuffix)
	isLab := func(host string) bool {
		host = strings.ToLower(host)
		return host == suffix || strings.HasSuffix(host, "."+suffix)
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	tr := &http.Transport{}
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		tr = base.Clone()
	}
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if host, port, err := net.SplitHostPort(addr); err == nil && isLab(host) {
			addr = net.JoinHostPort("127.0.0.1", port)
		}
		return dialer.DialContext(ctx, network, addr)
	}
	tr.Proxy = func(req *http.Request) (*url.URL, error) {
		if isLab(req.URL.Hostname()) {
			return nil, nil
		}
		return http.ProxyFromEnvironment(req)
	}
	return &http.Client{Transport: tr}
}
