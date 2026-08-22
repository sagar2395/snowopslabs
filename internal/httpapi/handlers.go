// SPDX-License-Identifier: Apache-2.0
package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/sagar2395/snowopslabs/internal/config"
	"github.com/sagar2395/snowopslabs/internal/k8s"
	"github.com/sagar2395/snowopslabs/internal/scenario"
	"github.com/sagar2395/snowopslabs/pkg/checks"
)

// validName matches identifiers safe to use as file-path segments and shell arguments.
var validName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func isValidName(s string) bool { return validName.MatchString(s) }

// StatusResponse represents the overall lab status.
type StatusResponse struct {
	DomainSuffix string             `json:"domainSuffix"`
	Cluster      *k8s.ClusterInfo   `json:"cluster"`
	Platform     PlatformStatusResp `json:"platform"`
	Apps         []AppStatusResp    `json:"apps"`
}

type PlatformStatusResp struct {
	Ingress ComponentStatus `json:"ingress"`
	Metrics ComponentStatus `json:"metrics"`
	Logging ComponentStatus `json:"logging"`
	Tracing ComponentStatus `json:"tracing"`
}

type ComponentStatus struct {
	Provider string `json:"provider"`
	Active   bool   `json:"active"`
}

type AppStatusResp struct {
	Name     string `json:"name"`
	Build    string `json:"buildStrategy"`
	Deploy   string `json:"deployStrategy"`
	Deployed bool   `json:"deployed"`
	Replicas string `json:"replicas,omitempty"`
	Ready    string `json:"ready,omitempty"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	resp := StatusResponse{
		DomainSuffix: s.cfg.DomainSuffix,
	}

	// Cluster info
	clusterInfo, _ := k8s.GetClusterInfo(ctx)
	resp.Cluster = clusterInfo

	// Platform status — derive namespace from the registry so we don't
	// assume namespace == provider name.
	ingressActive := false
	if p, err := s.registry.GetProvider("ingress", s.cfg.IngressProvider); err == nil {
		ingressActive = k8s.NamespaceExists(ctx, p.Namespace())
	}
	metricsActive := false
	if p, err := s.registry.GetProvider("monitoring/metrics", s.cfg.MetricsProvider); err == nil {
		metricsActive = k8s.NamespaceExists(ctx, p.Namespace())
	}
	loggingActive := false
	if p, err := s.registry.GetProvider("logging", s.cfg.LoggingProvider); err == nil {
		loggingActive = k8s.ServiceExists(ctx, p.Namespace(), "loki-gateway")
	}
	tracingActive := false
	if p, err := s.registry.GetProvider("tracing", s.cfg.TracingProvider); err == nil {
		tracingActive = k8s.ServiceExists(ctx, p.Namespace(), "tempo")
	}
	resp.Platform = PlatformStatusResp{
		Ingress: ComponentStatus{
			Provider: s.cfg.IngressProvider,
			Active:   ingressActive,
		},
		Metrics: ComponentStatus{
			Provider: s.cfg.MetricsProvider,
			Active:   metricsActive,
		},
		Logging: ComponentStatus{
			Provider: s.cfg.LoggingProvider,
			Active:   loggingActive,
		},
		Tracing: ComponentStatus{
			Provider: s.cfg.TracingProvider,
			Active:   tracingActive,
		},
	}

	// Apps
	apps, _ := config.ListApps(s.cfg.ProjectRoot)
	for _, appName := range apps {
		appCfg, _ := config.LoadAppConfig(s.cfg.ProjectRoot, appName)
		appResp := AppStatusResp{Name: appName}
		if appCfg != nil {
			appResp.Build = appCfg.BuildStrategy
			appResp.Deploy = appCfg.DeployStrategy
			ns := appName
			if appCfg.Namespace != "" {
				ns = appCfg.Namespace
			}
			status, _ := k8s.GetAppStatus(ctx, appName, ns)
			if status != nil {
				appResp.Deployed = status.Deployed
				appResp.Replicas = status.Replicas
				appResp.Ready = status.Ready
			}
		}
		resp.Apps = append(resp.Apps, appResp)
	}

	respondJSON(w, http.StatusOK, resp)
}

func (s *Server) handleListApps(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	apps, err := config.ListApps(s.cfg.ProjectRoot)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	var result []AppStatusResp
	for _, appName := range apps {
		appCfg, _ := config.LoadAppConfig(s.cfg.ProjectRoot, appName)
		appResp := AppStatusResp{Name: appName}
		if appCfg != nil {
			appResp.Build = appCfg.BuildStrategy
			appResp.Deploy = appCfg.DeployStrategy
			ns := appName
			if appCfg.Namespace != "" {
				ns = appCfg.Namespace
			}
			status, _ := k8s.GetAppStatus(ctx, appName, ns)
			if status != nil {
				appResp.Deployed = status.Deployed
				appResp.Replicas = status.Replicas
				appResp.Ready = status.Ready
			}
		}
		result = append(result, appResp)
	}

	respondJSON(w, http.StatusOK, result)
}

func (s *Server) handleAppDeploy(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	if !isValidName(name) {
		respondError(w, http.StatusBadRequest, "invalid_input", fmt.Sprintf("invalid app name %q: must match ^[a-zA-Z0-9_-]{1,64}$", name))
		return
	}
	jobID := s.exec.NextActionID()
	go func() {
		s.exec.RunScriptStreamedWith(jobID, fmt.Sprintf("Deploy %s", name), "engine/deploy.sh", "deploy", name)
	}()
	respondJSON(w, http.StatusAccepted, map[string]string{"jobId": jobID, "status": "accepted"})
}

func (s *Server) handleAppDestroy(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	if !isValidName(name) {
		respondError(w, http.StatusBadRequest, "invalid_input", fmt.Sprintf("invalid app name %q: must match ^[a-zA-Z0-9_-]{1,64}$", name))
		return
	}
	jobID := s.exec.NextActionID()
	go func() {
		s.exec.RunScriptStreamedWith(jobID, fmt.Sprintf("Destroy %s", name), "engine/deploy.sh", "destroy", name)
	}()
	respondJSON(w, http.StatusAccepted, map[string]string{"jobId": jobID, "status": "accepted"})
}

func (s *Server) handleAppBuild(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	if !isValidName(name) {
		respondError(w, http.StatusBadRequest, "invalid_input", fmt.Sprintf("invalid app name %q: must match ^[a-zA-Z0-9_-]{1,64}$", name))
		return
	}
	jobID := s.exec.NextActionID()
	go func() {
		s.exec.RunScriptStreamedWith(jobID, fmt.Sprintf("Build %s", name), "engine/build.sh", name)
	}()
	respondJSON(w, http.StatusAccepted, map[string]string{"jobId": jobID, "status": "accepted"})
}

func (s *Server) handlePlatformStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	categories := s.registry.Categories()
	result := make(map[string][]map[string]interface{})

	for _, cat := range categories {
		providers := s.registry.GetProviders(cat)
		for _, p := range providers {
			entry := map[string]interface{}{
				"name":      p.Name,
				"category":  cat,
				"installed": k8s.NamespaceExists(ctx, p.Namespace()),
			}
			result[cat] = append(result[cat], entry)
		}
	}

	respondJSON(w, http.StatusOK, result)
}

func (s *Server) handlePlatformUp(w http.ResponseWriter, r *http.Request) {
	jobID := s.exec.NextActionID()
	label := "platform-up"
	s.exec.BroadcastStart(jobID, label)
	go func() {
		var lastErr error
		if s.cfg.IngressProvider != "" {
			if err := s.registry.InstallStreamed("ingress", s.cfg.IngressProvider, s.exec); err != nil {
				lastErr = err
			}
		}
		if s.cfg.MetricsProvider != "" {
			if err := s.registry.InstallStreamed("monitoring/metrics", s.cfg.MetricsProvider, s.exec); err != nil {
				lastErr = err
			}
		}
		if err := s.registry.InstallStreamed("monitoring", "grafana", s.exec); err != nil {
			lastErr = err
		}
		if s.cfg.LoggingProvider != "" {
			if err := s.registry.InstallStreamed("logging", s.cfg.LoggingProvider, s.exec); err != nil {
				lastErr = err
			}
		}
		if s.cfg.TracingProvider != "" {
			if err := s.registry.InstallStreamed("tracing", s.cfg.TracingProvider, s.exec); err != nil {
				lastErr = err
			}
		}
		s.exec.BroadcastEnd(jobID, label, lastErr)
	}()
	respondJSON(w, http.StatusAccepted, map[string]string{"jobId": jobID, "status": "accepted"})
}

func (s *Server) handlePlatformDown(w http.ResponseWriter, r *http.Request) {
	jobID := s.exec.NextActionID()
	label := "platform-down"
	s.exec.BroadcastStart(jobID, label)
	go func() {
		var lastErr error
		// Uninstall in reverse order
		if s.cfg.TracingProvider != "" {
			if err := s.registry.UninstallStreamed("tracing", s.cfg.TracingProvider, s.exec); err != nil {
				lastErr = err
			}
		}
		if s.cfg.LoggingProvider != "" {
			if err := s.registry.UninstallStreamed("logging", s.cfg.LoggingProvider, s.exec); err != nil {
				lastErr = err
			}
		}
		if err := s.registry.UninstallStreamed("monitoring", "grafana", s.exec); err != nil {
			lastErr = err
		}
		if s.cfg.MetricsProvider != "" {
			if err := s.registry.UninstallStreamed("monitoring/metrics", s.cfg.MetricsProvider, s.exec); err != nil {
				lastErr = err
			}
		}
		if s.cfg.IngressProvider != "" {
			if err := s.registry.UninstallStreamed("ingress", s.cfg.IngressProvider, s.exec); err != nil {
				lastErr = err
			}
		}
		s.exec.BroadcastEnd(jobID, label, lastErr)
	}()
	respondJSON(w, http.StatusAccepted, map[string]string{"jobId": jobID, "status": "accepted"})
}

// resolveCategory maps a URL-safe category segment to the registry's category
// key. Nested categories like "monitoring/metrics" contain a slash and can't
// appear as a single path segment, so the API accepts the last segment
// ("metrics") and resolves it to the full key here.
func (s *Server) resolveCategory(cat string) string {
	cats := s.registry.Categories()
	for _, c := range cats {
		if c == cat {
			return c
		}
	}
	for _, c := range cats {
		if strings.HasSuffix(c, "/"+cat) {
			return c
		}
	}
	return cat
}

func (s *Server) handleComponentUp(w http.ResponseWriter, r *http.Request) {
	category := mux.Vars(r)["category"]
	name := mux.Vars(r)["name"]
	if !isValidName(category) || !isValidName(name) {
		respondError(w, http.StatusBadRequest, "invalid_input", "invalid category or component name: must match ^[a-zA-Z0-9_-]{1,64}$")
		return
	}
	category = s.resolveCategory(category)
	if _, err := s.registry.GetProvider(category, name); err != nil {
		respondError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	jobID := s.exec.NextActionID()
	label := fmt.Sprintf("install %s/%s", category, name)
	s.exec.BroadcastStart(jobID, label)
	go func() {
		err := s.registry.InstallStreamed(category, name, s.exec)
		s.exec.BroadcastEnd(jobID, label, err)
	}()
	respondJSON(w, http.StatusAccepted, map[string]string{"jobId": jobID, "status": "accepted"})
}

func (s *Server) handleComponentDown(w http.ResponseWriter, r *http.Request) {
	category := mux.Vars(r)["category"]
	name := mux.Vars(r)["name"]
	if !isValidName(category) || !isValidName(name) {
		respondError(w, http.StatusBadRequest, "invalid_input", "invalid category or component name: must match ^[a-zA-Z0-9_-]{1,64}$")
		return
	}
	category = s.resolveCategory(category)
	if _, err := s.registry.GetProvider(category, name); err != nil {
		respondError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	jobID := s.exec.NextActionID()
	label := fmt.Sprintf("uninstall %s/%s", category, name)
	s.exec.BroadcastStart(jobID, label)
	go func() {
		err := s.registry.UninstallStreamed(category, name, s.exec)
		s.exec.BroadcastEnd(jobID, label, err)
	}()
	respondJSON(w, http.StatusAccepted, map[string]string{"jobId": jobID, "status": "accepted"})
}

func (s *Server) handleListScenarios(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, s.scenes.Status())
}

func (s *Server) handleScenarioInfo(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	if !isValidName(name) {
		respondError(w, http.StatusBadRequest, "invalid_input", fmt.Sprintf("invalid scenario name %q: must match ^[a-zA-Z0-9_-]{1,64}$", name))
		return
	}
	sc, err := s.scenes.Get(name)
	if err != nil {
		respondError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}

	// Resolve template variables in explore URLs and commands
	for i := range sc.Explore.URLs {
		sc.Explore.URLs[i].URL = s.scenes.ResolveTemplate(sc.Explore.URLs[i].URL)
	}
	for i := range sc.Explore.Commands {
		sc.Explore.Commands[i].Command = s.scenes.ResolveTemplate(sc.Explore.Commands[i].Command)
	}
	for i := range sc.Explore.Tips {
		sc.Explore.Tips[i] = s.scenes.ResolveTemplate(sc.Explore.Tips[i])
	}

	respondJSON(w, http.StatusOK, sc)
}

func (s *Server) handleScenarioUp(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	if !isValidName(name) {
		respondError(w, http.StatusBadRequest, "invalid_input", fmt.Sprintf("invalid scenario name %q: must match ^[a-zA-Z0-9_-]{1,64}$", name))
		return
	}
	jobID := s.exec.NextActionID()
	label := fmt.Sprintf("Activate scenario: %s", name)
	s.exec.BroadcastStart(jobID, label)
	go func() {
		s.exec.BroadcastEnd(jobID, label, s.scenes.Up(name, s.exec))
	}()
	respondJSON(w, http.StatusAccepted, map[string]string{"jobId": jobID, "status": "accepted"})
}

func (s *Server) handleScenarioDown(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	if !isValidName(name) {
		respondError(w, http.StatusBadRequest, "invalid_input", fmt.Sprintf("invalid scenario name %q: must match ^[a-zA-Z0-9_-]{1,64}$", name))
		return
	}
	jobID := s.exec.NextActionID()
	label := fmt.Sprintf("Deactivate scenario: %s", name)
	s.exec.BroadcastStart(jobID, label)
	go func() {
		s.exec.BroadcastEnd(jobID, label, s.scenes.Down(name, s.exec))
	}()
	respondJSON(w, http.StatusAccepted, map[string]string{"jobId": jobID, "status": "accepted"})
}

// handleScenarioVerify runs the scenario's checks synchronously and returns
// the per-check results. The overall run is bounded to stay inside the HTTP
// server's write timeout; use the CLI's --watch mode for long convergence.
func (s *Server) handleScenarioVerify(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	if !isValidName(name) {
		respondError(w, http.StatusBadRequest, "invalid_input", fmt.Sprintf("invalid scenario name %q: must match ^[a-zA-Z0-9_-]{1,64}$", name))
		return
	}

	runner := checks.NewRunner()
	runner.DefaultTimeout = 10 * time.Second
	promURL := os.Getenv("PROMETHEUS_URL")
	if promURL == "" {
		promURL = "http://prometheus." + s.cfg.DomainSuffix
	}
	runner.PrometheusURL = promURL
	runner.Env = []string{
		"DOMAIN_SUFFIX=" + s.cfg.DomainSuffix,
		"MONITORING_NAMESPACE=" + s.cfg.MonitoringNamespace,
		"PROJECT_ROOT=" + s.cfg.ProjectRoot,
	}

	// Server WriteTimeout is 15s — bound the whole verify run below that.
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()

	results, err := s.scenes.Verify(ctx, name, runner)
	if err != nil {
		if errors.Is(err, scenario.ErrNoChecks) {
			respondError(w, http.StatusBadRequest, "no_checks", err.Error())
			return
		}
		respondError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"scenario": name,
		"passed":   checks.AllPass(results),
		"results":  results,
	})
}

func (s *Server) handleListServices(w http.ResponseWriter, r *http.Request) {
	svcs := s.svcs.List()
	type svcResp struct {
		Name string `json:"name"`
	}
	var result []svcResp
	for _, svc := range svcs {
		result = append(result, svcResp{Name: svc.Name})
	}
	respondJSON(w, http.StatusOK, result)
}

func (s *Server) handleServiceUp(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	if !isValidName(name) {
		respondError(w, http.StatusBadRequest, "invalid_input", fmt.Sprintf("invalid service name %q: must match ^[a-zA-Z0-9_-]{1,64}$", name))
		return
	}
	jobID := s.exec.NextActionID()
	label := fmt.Sprintf("service-up: %s", name)
	s.exec.BroadcastStart(jobID, label)
	go func() {
		s.exec.BroadcastEnd(jobID, label, s.svcs.Install(name, s.exec))
	}()
	respondJSON(w, http.StatusAccepted, map[string]string{"jobId": jobID, "status": "accepted"})
}

func (s *Server) handleServiceDown(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	if !isValidName(name) {
		respondError(w, http.StatusBadRequest, "invalid_input", fmt.Sprintf("invalid service name %q: must match ^[a-zA-Z0-9_-]{1,64}$", name))
		return
	}
	jobID := s.exec.NextActionID()
	label := fmt.Sprintf("service-down: %s", name)
	s.exec.BroadcastStart(jobID, label)
	go func() {
		s.exec.BroadcastEnd(jobID, label, s.svcs.Uninstall(name, s.exec))
	}()
	respondJSON(w, http.StatusAccepted, map[string]string{"jobId": jobID, "status": "accepted"})
}

// DashboardURL represents a link to a platform dashboard.
type DashboardURL struct {
	Name      string `json:"name"`
	Label     string `json:"label"`
	URL       string `json:"url"`
	Available bool   `json:"available"`
	Category  string `json:"category"`
}

func (s *Server) handleDashboardURLs(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	domain := s.cfg.DomainSuffix
	monitoringNS := s.cfg.MonitoringNamespace
	var dashboards []DashboardURL

	if k8s.NamespaceExists(ctx, monitoringNS) {
		dashboards = append(dashboards, DashboardURL{
			Name: "grafana", Label: "Grafana",
			URL: fmt.Sprintf("http://grafana.%s", domain), Available: true, Category: "monitoring",
		})
		dashboards = append(dashboards, DashboardURL{
			Name: "prometheus", Label: "Prometheus",
			URL: fmt.Sprintf("http://prometheus.%s", domain), Available: true, Category: "monitoring",
		})
	}

	if s.cfg.IngressProvider == "traefik" && k8s.NamespaceExists(ctx, "traefik") {
		dashboards = append(dashboards, DashboardURL{
			Name: "traefik", Label: "Traefik Dashboard",
			URL: fmt.Sprintf("http://traefik.%s/dashboard/", domain), Available: true, Category: "ingress",
		})
	}

	if k8s.NamespaceExists(ctx, "kubernetes-dashboard") {
		dashboards = append(dashboards, DashboardURL{
			Name: "kubernetes-dashboard", Label: "Kubernetes Dashboard",
			URL: fmt.Sprintf("http://dashboard.%s", domain), Available: true, Category: "cluster",
		})
	}

	if k8s.NamespaceExists(ctx, "argocd") {
		dashboards = append(dashboards, DashboardURL{
			Name: "argocd", Label: "ArgoCD",
			URL: fmt.Sprintf("http://argocd.%s", domain), Available: true, Category: "gitops",
		})
	}

	if k8s.NamespaceExists(ctx, "chaos-mesh") {
		dashboards = append(dashboards, DashboardURL{
			Name: "chaos-mesh", Label: "Chaos Mesh",
			URL: "http://localhost:2333", Available: true, Category: "chaos",
		})
	}

	if k8s.ServiceExists(ctx, monitoringNS, "loki-gateway") {
		dashboards = append(dashboards, DashboardURL{
			Name: "loki", Label: "Logs (Loki)",
			URL:       fmt.Sprintf("http://grafana.%s/explore?orgId=1&left=%%7B%%22datasource%%22:%%22Loki%%22%%7D", domain),
			Available: true, Category: "monitoring",
		})
	}

	if k8s.ServiceExists(ctx, monitoringNS, "tempo") {
		dashboards = append(dashboards, DashboardURL{
			Name: "tempo", Label: "Traces (Tempo)",
			URL:       fmt.Sprintf("http://grafana.%s/explore?orgId=1&left=%%7B%%22datasource%%22:%%22Tempo%%22%%7D", domain),
			Available: true, Category: "monitoring",
		})
	}

	respondJSON(w, http.StatusOK, dashboards)
}

// WebSocket keepalive tuning. Pings keep idle connections alive through
// proxies; the pong deadline detects half-dead connections server-side.
const (
	wsWriteWait  = 10 * time.Second
	wsPongWait   = 60 * time.Second
	wsPingPeriod = 25 * time.Second
)

// handleJobs returns the recent action/job history (newest first) so clients
// can recover job state after a page reload or a dropped WebSocket.
func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, s.exec.Broadcast.Jobs())
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	// Subscribe to action events
	actionCh := s.exec.Broadcast.Subscribe()
	defer s.exec.Broadcast.Unsubscribe(actionCh)

	// Periodic status updates
	statusTicker := time.NewTicker(5 * time.Second)
	defer statusTicker.Stop()
	pingTicker := time.NewTicker(wsPingPeriod)
	defer pingTicker.Stop()

	// Keepalive: expect a pong (or any read) within wsPongWait.
	_ = conn.SetReadDeadline(time.Now().Add(wsPongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(wsPongWait))
	})

	// Read pump — discard incoming messages, detect close / dead peer
	closeCh := make(chan struct{})
	go func() {
		defer close(closeCh)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	writeJSON := func(v interface{}) error {
		_ = conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
		return conn.WriteJSON(v)
	}
	sendStatus := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		info, _ := k8s.GetClusterInfo(ctx)
		cancel()
		return writeJSON(map[string]interface{}{"type": "status", "data": info})
	}

	// Send an immediate snapshot so a fresh client doesn't wait for the
	// first ticker interval to learn the cluster state.
	if err := sendStatus(); err != nil {
		return
	}

	for {
		select {
		case <-closeCh:
			return
		case <-pingTicker.C:
			_ = conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-statusTicker.C:
			if err := sendStatus(); err != nil {
				return
			}
		case event := <-actionCh:
			if err := writeJSON(map[string]interface{}{
				"type": "action",
				"data": event,
			}); err != nil {
				return
			}
		}
	}
}

func (s *Server) handleListRuntimes(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, s.runtimes.List())
}

func (s *Server) handleRuntimeActivate(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	if !isValidName(name) {
		respondError(w, http.StatusBadRequest, "invalid_input", fmt.Sprintf("invalid runtime name %q: must match ^[a-zA-Z0-9_-]{1,64}$", name))
		return
	}
	jobID := s.exec.NextActionID()
	go func() {
		s.runtimes.ActivateWith(jobID, name, s.exec)
	}()
	respondJSON(w, http.StatusAccepted, map[string]string{"jobId": jobID, "status": "accepted"})
}

func (s *Server) handleRuntimeDeactivate(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	if !isValidName(name) {
		respondError(w, http.StatusBadRequest, "invalid_input", fmt.Sprintf("invalid runtime name %q: must match ^[a-zA-Z0-9_-]{1,64}$", name))
		return
	}
	jobID := s.exec.NextActionID()
	go func() {
		s.runtimes.DeactivateWith(jobID, name, s.exec)
	}()
	respondJSON(w, http.StatusAccepted, map[string]string{"jobId": jobID, "status": "accepted"})
}
