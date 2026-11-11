// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/snowops-oss/snowopslabs/internal/config"
	"github.com/snowops-oss/snowopslabs/internal/labcheck"
)

// ingressWait is how long the ingress may take to answer through the host
// port before labctl concludes something else holds that port.
var ingressWait = 30 * time.Second

// reloadIngress takes the ingress ports and domain suffix the runtime recorded
// for the cluster, and passes them to every script that runs after it.
func reloadIngress() {
	fresh, err := config.Load(cfg.ProjectRoot)
	if err != nil {
		return
	}
	cfg.HTTPPort, cfg.HTTPSPort, cfg.DomainSuffix = fresh.HTTPPort, fresh.HTTPSPort, fresh.DomainSuffix
	scriptExec.SetEnv("HTTP_PORT", cfg.HTTPPort)
	scriptExec.SetEnv("HTTPS_PORT", cfg.HTTPSPort)
	scriptExec.SetEnv("DOMAIN_SUFFIX", cfg.DomainSuffix)
	scriptExec.SetEnv("INGRESS_URL_SUFFIX", cfg.IngressURLSuffix())
}

// ingressAnswers reports whether the lab's own Grafana answers through the
// host port, which proves the port reaches this lab and not something else.
func ingressAnswers(ctx context.Context, client *http.Client) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.IngressURL("grafana")+"/api/health", nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var body strings.Builder
	_, _ = io.Copy(&body, io.LimitReader(resp.Body, 4096))
	return resp.StatusCode == http.StatusOK && strings.Contains(body.String(), `"database"`)
}

// waitIngress polls ingressAnswers until it succeeds or wait passes.
func waitIngress(ctx context.Context, client *http.Client, wait time.Duration) bool {
	deadline := time.Now().Add(wait)
	for {
		if ingressAnswers(ctx, client) {
			return true
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(2 * time.Second):
		}
	}
}

// ensureIngressReachable confirms the lab's URLs reach this lab from this
// machine. A port can be free when the lab is built and taken later, by another
// lab or another user's Docker VM; a k3d lab then moves its ingress to free
// ports, and other runtimes say how to.
func ensureIngressReachable(ctx context.Context, out io.Writer) error {
	if !cfg.LocalIngress() {
		return nil
	}
	client := labcheck.HTTPClient(cfg)
	if waitIngress(ctx, client, ingressWait) {
		return nil
	}
	url := cfg.IngressURL("grafana")
	if cfg.Profile != "k3d" {
		return fmt.Errorf("%s does not reach the lab: something else on this machine answers on port %s.\n"+
			"Free that port, or rebuild the lab on other ports: set HTTP_PORT=8080 and HTTPS_PORT=8443 in .env, then run 'labctl reset'",
			url, cfg.HTTPPort)
	}
	fmt.Fprintf(out, "\n%s does not reach the lab: port %s on this machine answers for something else "+
		"(another lab, or another user's Docker VM). Moving the lab's ingress to free ports.\n", url, cfg.HTTPPort)
	if err := scriptExec.RunScript("runtimes/k3d/move-ingress.sh", cfg.ClusterName); err != nil {
		return fmt.Errorf("moving the lab's ingress to free ports failed: %w", err)
	}
	reloadIngress()
	if !waitIngress(ctx, client, ingressWait) {
		return fmt.Errorf("%s still does not reach the lab after moving it to port %s.\n"+
			"Check the load balancer with 'docker ps --filter name=k3d-%s-serverlb', then re-run 'labctl init'",
			cfg.IngressURL("grafana"), cfg.HTTPPort, cfg.ClusterName)
	}
	return nil
}
