// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/snowops-oss/snowopslabs/internal/labcheck"
)

// datasourceWait is how long Grafana's Prometheus datasource may take to
// answer while the pods behind it settle.
var datasourceWait = 60 * time.Second

// errGrafanaAuth means Grafana refused the admin credentials labctl knows.
var errGrafanaAuth = errors.New("grafana refused the admin password")

// grafanaAdminPassword returns the admin password install.sh gives Grafana.
func grafanaAdminPassword() string {
	if v := os.Getenv("GRAFANA_ADMIN_PASSWORD"); v != "" {
		return v
	}
	if v := cfg.ScriptEnv["GRAFANA_ADMIN_PASSWORD"]; v != "" {
		return v
	}
	return "admin"
}

// prometheusDatasourceHealth asks Grafana to query its Prometheus datasource,
// the path every lab dashboard depends on. The error carries Grafana's reason.
func prometheusDatasourceHealth(ctx context.Context, client *http.Client) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	url := cfg.IngressURL("grafana") + "/api/datasources/uid/prometheus/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth("admin", grafanaAdminPassword())
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return errGrafanaAuth
	}
	var health struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&health)
	if resp.StatusCode == http.StatusOK && health.Status == "OK" {
		return nil
	}
	if health.Message != "" {
		return errors.New(health.Message)
	}
	return fmt.Errorf("HTTP %d", resp.StatusCode)
}

// waitDatasource polls prometheusDatasourceHealth until it passes, Grafana
// refuses the credentials, or wait passes, and returns the last result.
func waitDatasource(ctx context.Context, client *http.Client, wait time.Duration) error {
	deadline := time.Now().Add(wait)
	for {
		err := prometheusDatasourceHealth(ctx, client)
		if err == nil || errors.Is(err, errGrafanaAuth) || time.Now().After(deadline) || ctx.Err() != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(3 * time.Second):
		}
	}
}

// ensureGrafanaQueriesPrometheus confirms Grafana can query Prometheus, so
// the dashboards have data. When it cannot, init re-applies Grafana with the
// lab's current values, because an existing lab otherwise keeps whatever
// values its Grafana was first installed with.
func ensureGrafanaQueriesPrometheus(ctx context.Context, out io.Writer) error {
	if !cfg.LocalIngress() {
		return nil
	}
	client := labcheck.HTTPClient(cfg)
	err := waitDatasource(ctx, client, datasourceWait)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, errGrafanaAuth):
		fmt.Fprintln(out, "\nSkipped the Grafana → Prometheus check: Grafana refuses the default admin password.\n"+
			"If you changed it, set GRAFANA_ADMIN_PASSWORD in .env so labctl can check the dashboards' data source.")
		return nil
	}
	fmt.Fprintf(out, "\nGrafana cannot query Prometheus (%v), so every dashboard shows \"No data\".\n"+
		"Re-applying Grafana with the lab's current values.\n", err)
	if err := reg.Install("monitoring", "grafana", scriptExec); err != nil {
		return fmt.Errorf("re-applying Grafana failed: %w\n"+
			"Re-run 'labctl platform up monitoring/grafana' and read its output", err)
	}
	if err := waitDatasource(ctx, client, datasourceWait); err != nil && !errors.Is(err, errGrafanaAuth) {
		return fmt.Errorf("grafana still cannot query Prometheus: %w.\n"+
			"Check that Prometheus is running with 'kubectl -n %s get pods', and see "+
			"\"Grafana dashboards show No data\" in docs/troubleshooting.md", err, cfg.MonitoringNamespace)
	}
	return nil
}
