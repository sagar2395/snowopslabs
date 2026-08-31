// SPDX-License-Identifier: Apache-2.0
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/sagar2395/snowopslabs/internal/appdetail"
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
	// URL is the app's ingress address, by the lab convention
	// http://<app>.<domainSuffix>. Only set once the app is deployed; the UI
	// links it so a user can jump straight to the running app. It requires the
	// app's ingress to be enabled and the host resolvable (a /etc/hosts entry
	// for k3d/kind), which the UI notes.
	URL string `json:"url,omitempty"`
	// HPA carries live autoscaler state when an HPA (KEDA-managed included)
	// targets the app; nil otherwise, so the UI shows the plain replica count.
	HPA *k8s.HPAStatus `json:"hpa,omitempty"`
}

// appURL builds the conventional ingress URL for a deployed app, or "" when the
// app isn't deployed or no domain suffix is configured (so no dead link shows).
func appURL(name, domainSuffix string, deployed bool) string {
	if !deployed || domainSuffix == "" {
		return ""
	}
	return fmt.Sprintf("http://%s.%s", name, domainSuffix)
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
		// Metrics shares the monitoring namespace with grafana/loki/tempo, so
		// detect it by its own Helm release rather than namespace existence
		// (which would read active whenever any monitoring component is present).
		metricsActive = k8s.HelmReleaseExists(ctx, p.Namespace(), p.Name)
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
			// Surface live autoscaler state so the UI can show why the app scaled,
			// without a terminal. Only worth querying once it's deployed.
			if appResp.Deployed {
				if hpa, err := k8s.GetHPAStatus(ctx, ns, appName); err == nil && hpa.Present {
					appResp.HPA = hpa
				}
			}
			appResp.URL = appURL(appName, s.cfg.DomainSuffix, appResp.Deployed)
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
		respondError(w, r, http.StatusInternalServerError, "internal_error", err.Error())
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
			// Surface live autoscaler state so the UI can answer "why did it
			// scale" without a terminal. Only worth querying once the app is up.
			if appResp.Deployed {
				if hpa, err := k8s.GetHPAStatus(ctx, ns, appName); err == nil && hpa.Present {
					appResp.HPA = hpa
				}
			}
			appResp.URL = appURL(appName, s.cfg.DomainSuffix, appResp.Deployed)
		}
		result = append(result, appResp)
	}

	respondJSON(w, http.StatusOK, result)
}

// handleAppDetail returns the "how it's built and deployed" view for one app:
// overview, stack, and the actual Dockerfile + Helm chart with their repo paths.
func (s *Server) handleAppDetail(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	if !isValidName(name) {
		respondError(w, r, http.StatusBadRequest, "invalid_input", fmt.Sprintf("invalid app name %q: must match ^[a-zA-Z0-9_-]{1,64}$", name))
		return
	}
	apps, _ := config.ListApps(s.cfg.ProjectRoot)
	found := false
	for _, a := range apps {
		if a == name {
			found = true
			break
		}
	}
	if !found {
		respondError(w, r, http.StatusNotFound, "not_found", fmt.Sprintf("app %q not found", name))
		return
	}

	build, deploy, namespace, valuesFile := "", "", "", ""
	if appCfg, _ := config.LoadAppConfig(s.cfg.ProjectRoot, name); appCfg != nil {
		build, deploy, namespace, valuesFile = appCfg.BuildStrategy, appCfg.DeployStrategy, appCfg.Namespace, appCfg.HelmValues
	}
	detail := appdetail.Build(s.cfg.ProjectRoot, name, build, deploy, namespace, valuesFile)
	respondJSON(w, http.StatusOK, detail)
}

func (s *Server) handleAppDeploy(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	if !isValidName(name) {
		respondError(w, r, http.StatusBadRequest, "invalid_input", fmt.Sprintf("invalid app name %q: must match ^[a-zA-Z0-9_-]{1,64}$", name))
		return
	}
	jobID := s.exec.NextActionID()
	go func() {
		_ = s.exec.RunScriptStreamedWith(jobID, fmt.Sprintf("Deploy %s", name), "src/engine/deploy.sh", "deploy", name)
	}()
	respondJSON(w, http.StatusAccepted, map[string]string{"jobId": jobID, "status": "accepted"})
}

func (s *Server) handleAppDestroy(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	if !isValidName(name) {
		respondError(w, r, http.StatusBadRequest, "invalid_input", fmt.Sprintf("invalid app name %q: must match ^[a-zA-Z0-9_-]{1,64}$", name))
		return
	}
	jobID := s.exec.NextActionID()
	go func() {
		_ = s.exec.RunScriptStreamedWith(jobID, fmt.Sprintf("Destroy %s", name), "src/engine/deploy.sh", "destroy", name)
	}()
	respondJSON(w, http.StatusAccepted, map[string]string{"jobId": jobID, "status": "accepted"})
}

func (s *Server) handleAppBuild(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	if !isValidName(name) {
		respondError(w, r, http.StatusBadRequest, "invalid_input", fmt.Sprintf("invalid app name %q: must match ^[a-zA-Z0-9_-]{1,64}$", name))
		return
	}
	jobID := s.exec.NextActionID()
	go func() {
		_ = s.exec.RunScriptStreamedWith(jobID, fmt.Sprintf("Build %s", name), "src/engine/build.sh", name)
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
		exclusive := s.registry.IsExclusive(cat)
		for _, p := range providers {
			// Providers that share a namespace (prometheus, grafana, loki, tempo
			// all live in the monitoring namespace) can't be told apart by
			// namespace existence — the first install creates the namespace and
			// then every one of them reads as installed. Detect those by their own
			// Helm release instead; everyone else owns its namespace, so namespace
			// existence remains the (cheaper) signal.
			installed := k8s.NamespaceExists(ctx, p.Namespace())
			if p.SharesNamespace() {
				installed = k8s.HelmReleaseExists(ctx, p.Namespace(), p.Name)
			}
			entry := map[string]interface{}{
				"name":      p.Name,
				"category":  cat,
				"installed": installed,
				"exclusive": exclusive,
			}
			result[cat] = append(result[cat], entry)
		}
	}

	respondJSON(w, http.StatusOK, result)
}

// handlePlatformUp installs the slim baseline only: ingress + metrics + Grafana
// — the minimum needed for a cluster you can reach and observe. Logging,
// tracing, GitOps, chaos and the rest are opt-in, installed per-need from the
// Platform tab or pulled in by a scenario that declares them. (Previously this
// installed the whole observability stack, which surprised users with more than
// they asked for.)
func (s *Server) handlePlatformUp(w http.ResponseWriter, r *http.Request) {
	jobID := s.exec.NextActionID()
	label := "platform-baseline"
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
		respondError(w, r, http.StatusBadRequest, "invalid_input", "invalid category or component name: must match ^[a-zA-Z0-9_-]{1,64}$")
		return
	}
	category = s.resolveCategory(category)
	if _, err := s.registry.GetProvider(category, name); err != nil {
		respondError(w, r, http.StatusNotFound, "not_found", err.Error())
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
		respondError(w, r, http.StatusBadRequest, "invalid_input", "invalid category or component name: must match ^[a-zA-Z0-9_-]{1,64}$")
		return
	}
	category = s.resolveCategory(category)
	if _, err := s.registry.GetProvider(category, name); err != nil {
		respondError(w, r, http.StatusNotFound, "not_found", err.Error())
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

// ScenarioRef is a compact scenario pointer for the "used in" list.
type ScenarioRef struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
}

// PlatformComponentDetail is the per-tool details payload: what it is, how it's
// installed, and which scenarios depend on it.
type PlatformComponentDetail struct {
	Category        string        `json:"category"`
	Name            string        `json:"name"`
	Namespace       string        `json:"namespace"`
	Installed       bool          `json:"installed"`
	Description     string        `json:"description"`
	Provides        []string      `json:"provides"`
	Ports           []string      `json:"ports"`
	Dependencies    []string      `json:"dependencies"`
	Resources       []string      `json:"resources"`
	Chart           string        `json:"chart"`
	InstallCommands []string      `json:"installCommands"`
	UsedInScenarios []ScenarioRef `json:"usedInScenarios"`
}

// resolveInstallVars substitutes the shell variables an install.sh uses into
// the commands shown on the details page, so a learner sees the real values
// (namespace, ingress host) instead of raw $NAMESPACE / ${DOMAIN_SUFFIX}. Only
// the variables the server can resolve are replaced; secrets and script-local
// temp paths (e.g. $GRAFANA_ADMIN_PASSWORD, $VALUES_FILE) are left as-is —
// they are meant to be supplied at install time, not shown.
func resolveInstallVars(cmds []string, namespace, domain string) []string {
	repl := []struct{ from, to string }{}
	if namespace != "" {
		repl = append(repl, struct{ from, to string }{"${NAMESPACE}", namespace}, struct{ from, to string }{"$NAMESPACE", namespace})
	}
	if domain != "" {
		repl = append(repl, struct{ from, to string }{"${DOMAIN_SUFFIX}", domain}, struct{ from, to string }{"$DOMAIN_SUFFIX", domain})
	}
	out := make([]string, len(cmds))
	for i, c := range cmds {
		for _, r := range repl {
			c = strings.ReplaceAll(c, r.from, r.to)
		}
		out[i] = c
	}
	return out
}

// nonNilStrings returns s, or an empty (non-nil) slice when s is nil, so it
// marshals to a JSON [] instead of null — the UI indexes .length on these.
func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (s *Server) handlePlatformComponentDetail(w http.ResponseWriter, r *http.Request) {
	category := mux.Vars(r)["category"]
	name := mux.Vars(r)["name"]
	if !isValidName(category) || !isValidName(name) {
		respondError(w, r, http.StatusBadRequest, "invalid_input", "invalid category or component name: must match ^[a-zA-Z0-9_-]{1,64}$")
		return
	}
	category = s.resolveCategory(category)
	p, err := s.registry.GetProvider(category, name)
	if err != nil {
		respondError(w, r, http.StatusNotFound, "not_found", err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	installed := k8s.NamespaceExists(ctx, p.Namespace())
	if p.SharesNamespace() {
		installed = k8s.HelmReleaseExists(ctx, p.Namespace(), p.Name)
	}

	meta := p.Meta()
	// Guarantee non-nil slices: a Go nil slice marshals to JSON null, and the UI
	// reads e.g. detail.provides.length — a null there crashes the details view.
	detail := PlatformComponentDetail{
		Category:        category,
		Name:            name,
		Namespace:       p.Namespace(),
		Installed:       installed,
		Description:     meta.Description,
		Provides:        nonNilStrings(meta.Provides),
		Ports:           nonNilStrings(meta.Ports),
		Dependencies:    nonNilStrings(meta.Dependencies),
		Resources:       nonNilStrings(meta.Resources),
		Chart:           meta.Chart,
		InstallCommands: nonNilStrings(resolveInstallVars(p.InstallCommands(), p.Namespace(), s.cfg.DomainSuffix)),
		UsedInScenarios: s.scenariosUsingComponent(category, name),
	}
	respondJSON(w, http.StatusOK, detail)
}

// scenariosUsingComponent returns the scenarios whose platform prerequisites
// reference this component, matching by full "category/name" key, bare category,
// or bare name — the three forms scenarios use to name a prerequisite.
func (s *Server) scenariosUsingComponent(category, name string) []ScenarioRef {
	full := category + "/" + name
	out := []ScenarioRef{}
	for _, sc := range s.scenes.List() {
		for _, prereq := range sc.Prerequisites.Platform {
			if prereq == full || prereq == category || prereq == name {
				out = append(out, ScenarioRef{Name: sc.Name, DisplayName: sc.DisplayName})
				break
			}
		}
	}
	return out
}

func (s *Server) handleListScenarios(w http.ResponseWriter, r *http.Request) {
	respondCatalog(w, r, s.scenes.Status())
}

func (s *Server) handleScenarioInfo(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	if !isValidName(name) {
		respondError(w, r, http.StatusBadRequest, "invalid_input", fmt.Sprintf("invalid scenario name %q: must match ^[a-zA-Z0-9_-]{1,64}$", name))
		return
	}
	sc, err := s.scenes.Get(name)
	if err != nil {
		respondError(w, r, http.StatusNotFound, "not_found", err.Error())
		return
	}

	// Resolve template vars for display, using the scenario's parameter defaults
	// so {{.Param}} shows a real value. Work on a copy with fresh slices — sc is
	// the cached scenario and must not be mutated.
	defaults := s.scenes.ParamDefaults(sc)
	resolve := func(in string) string { return s.scenes.ResolveTemplateWithParams(in, defaults) }
	resp := *sc

	urls := make([]scenario.ExploreURL, len(sc.Explore.URLs))
	for i, u := range sc.Explore.URLs {
		u.URL = resolve(u.URL)
		urls[i] = u
	}
	cmds := make([]scenario.ExploreCommand, len(sc.Explore.Commands))
	for i, c := range sc.Explore.Commands {
		c.Command = resolve(c.Command)
		cmds[i] = c
	}
	tips := make([]string, len(sc.Explore.Tips))
	for i, t := range sc.Explore.Tips {
		tips[i] = resolve(t)
	}
	resp.Explore = scenario.Explore{URLs: urls, Commands: cmds, Tips: tips}

	// Inline each snippet's content (from its file or inline YAML) so the UI can
	// show the actual manifest a learner would apply.
	if len(sc.Snippets) > 0 {
		snips := make([]scenario.Snippet, len(sc.Snippets))
		for i, sn := range sc.Snippets {
			if content, err := s.scenes.SnippetContent(sc, sn); err == nil {
				sn.YAML = content
			}
			snips[i] = sn
		}
		resp.Snippets = snips
	}

	respondJSON(w, http.StatusOK, resp)
}

func (s *Server) handleScenarioUp(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	if !isValidName(name) {
		respondError(w, r, http.StatusBadRequest, "invalid_input", fmt.Sprintf("invalid scenario name %q: must match ^[a-zA-Z0-9_-]{1,64}$", name))
		return
	}
	// Optional scenario parameter overrides: {"params": {"threshold": "15", ...}}.
	// An empty or absent body means "use the defaults" — same as before.
	var body struct {
		Params map[string]string `json:"params"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body) // tolerate an empty body
	}

	jobID := s.exec.NextActionID()
	label := fmt.Sprintf("Activate scenario: %s", name)
	s.exec.BroadcastStart(jobID, label)
	go func() {
		// Stage overrides for this activation only (serialised per engine, like
		// the existing SetOutput usage).
		s.scenes.SetActivationParams(body.Params)
		defer s.scenes.SetActivationParams(nil)
		s.exec.BroadcastEnd(jobID, label, s.scenes.Up(name, s.exec, false))
	}()
	respondJSON(w, http.StatusAccepted, map[string]string{"jobId": jobID, "status": "accepted"})
}

func (s *Server) handleScenarioDown(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	if !isValidName(name) {
		respondError(w, r, http.StatusBadRequest, "invalid_input", fmt.Sprintf("invalid scenario name %q: must match ^[a-zA-Z0-9_-]{1,64}$", name))
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
		respondError(w, r, http.StatusBadRequest, "invalid_input", fmt.Sprintf("invalid scenario name %q: must match ^[a-zA-Z0-9_-]{1,64}$", name))
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
	startedAt := time.Now()
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()

	results, err := s.scenes.Verify(ctx, name, runner)
	if err != nil {
		if errors.Is(err, scenario.ErrNoChecks) {
			respondError(w, r, http.StatusBadRequest, "no_checks", err.Error())
			return
		}
		respondError(w, r, http.StatusNotFound, "not_found", err.Error())
		return
	}

	// Record the verification in history so the results view shows whether the
	// user solved the scenario, with objectives + per-check breakdown (W4-T02).
	s.recordScenarioVerify(name, results, startedAt)

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
		respondError(w, r, http.StatusBadRequest, "invalid_input", fmt.Sprintf("invalid service name %q: must match ^[a-zA-Z0-9_-]{1,64}$", name))
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
		respondError(w, r, http.StatusBadRequest, "invalid_input", fmt.Sprintf("invalid service name %q: must match ^[a-zA-Z0-9_-]{1,64}$", name))
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
			URL:       grafanaExploreURL(domain, "loki", "loki", ""),
			Available: true, Category: "monitoring",
		})
	}

	if k8s.ServiceExists(ctx, monitoringNS, "tempo") {
		dashboards = append(dashboards, DashboardURL{
			Name: "tempo", Label: "Traces (Tempo)",
			URL:       grafanaExploreURL(domain, "tempo", "tempo", ""),
			Available: true, Category: "monitoring",
		})
	}

	respondJSON(w, http.StatusOK, dashboards)
}

// grafanaExploreURL builds a Grafana Explore deep link for a datasource. Grafana
// 11+ replaced the old ?left=<json> parameter with ?panes=<json> keyed by a pane
// id, and resolves the datasource by UID (not name) — so a name-based link opens
// an empty Explore. The datasources are provisioned with stable UIDs
// (loki/tempo/prometheus), which these links reference. expr may be empty to open
// Explore with the datasource selected and no pre-filled query.
func grafanaExploreURL(domain, dsType, dsUID, expr string) string {
	type dsRef struct {
		Type string `json:"type"`
		UID  string `json:"uid"`
	}
	type query struct {
		RefID      string `json:"refId"`
		Datasource dsRef  `json:"datasource"`
		Expr       string `json:"expr,omitempty"`
	}
	type pane struct {
		Datasource string            `json:"datasource"`
		Queries    []query           `json:"queries"`
		Range      map[string]string `json:"range"`
	}
	panes := map[string]pane{
		"exp": {
			Datasource: dsUID,
			Queries:    []query{{RefID: "A", Datasource: dsRef{Type: dsType, UID: dsUID}, Expr: expr}},
			// 6h (vs Grafana's 1h default) so the lab's quiet apps, which log
			// mostly at startup, still show something when logs are first opened.
			Range: map[string]string{"from": "now-6h", "to": "now"},
		},
	}
	b, _ := json.Marshal(panes)
	return fmt.Sprintf("http://grafana.%s/explore?orgId=1&schemaVersion=1&panes=%s", domain, url.QueryEscape(string(b)))
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
	defer func() { _ = conn.Close() }()

	// Subscribe from the client's cursor (?after=<seq>): replay any events it
	// missed while disconnected, then stream live. SubscribeFrom makes the
	// backlog snapshot and live registration atomic, so the join is gap-free and
	// duplicate-free.
	after := parseAfterCursor(r)
	backlog, actionCh, contiguous := s.exec.Broadcast.SubscribeFrom(after)
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

	// If the client's cursor fell off the replay ring, tell it to resync from
	// the job history (GET /jobs) rather than silently resuming mid-stream.
	if !contiguous {
		if err := writeJSON(map[string]interface{}{"type": "resync"}); err != nil {
			return
		}
	}
	// Replay missed events before going live, each already carrying its Seq.
	for _, event := range backlog {
		if err := writeJSON(map[string]interface{}{"type": "action", "data": event}); err != nil {
			return
		}
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
		respondError(w, r, http.StatusBadRequest, "invalid_input", fmt.Sprintf("invalid runtime name %q: must match ^[a-zA-Z0-9_-]{1,64}$", name))
		return
	}
	jobID := s.exec.NextActionID()
	go func() {
		_ = s.runtimes.ActivateWith(jobID, name, s.exec)
	}()
	respondJSON(w, http.StatusAccepted, map[string]string{"jobId": jobID, "status": "accepted"})
}

func (s *Server) handleRuntimeDeactivate(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	if !isValidName(name) {
		respondError(w, r, http.StatusBadRequest, "invalid_input", fmt.Sprintf("invalid runtime name %q: must match ^[a-zA-Z0-9_-]{1,64}$", name))
		return
	}
	jobID := s.exec.NextActionID()
	go func() {
		_ = s.runtimes.DeactivateWith(jobID, name, s.exec)
	}()
	respondJSON(w, http.StatusAccepted, map[string]string{"jobId": jobID, "status": "accepted"})
}
