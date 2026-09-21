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
	"path/filepath"
	"regexp"
	"slices"
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

// pathName returns the {name} route variable, having already answered 400 when
// it is not a valid identifier — so a handler only needs to return on !ok.
func pathName(w http.ResponseWriter, r *http.Request, kind string) (string, bool) {
	name := mux.Vars(r)["name"]
	if !isValidName(name) {
		respondError(w, r, http.StatusBadRequest, "invalid_input",
			fmt.Sprintf("invalid %s name %q: must match ^[a-zA-Z0-9_-]{1,64}$", kind, name))
		return "", false
	}
	return name, true
}

// StatusResponse represents the overall lab status.
type StatusResponse struct {
	DomainSuffix string             `json:"domainSuffix"`
	Cluster      *k8s.ClusterInfo   `json:"cluster"`
	Platform     PlatformStatusResp `json:"platform"`
	Apps         []AppStatusResp    `json:"apps"`
}

// PlatformStatusResp reports the provider and state of each core platform
// category.
type PlatformStatusResp struct {
	Ingress ComponentStatus `json:"ingress"`
	Metrics ComponentStatus `json:"metrics"`
	Logging ComponentStatus `json:"logging"`
	Tracing ComponentStatus `json:"tracing"`
}

// ComponentStatus is one platform category's selected provider and whether it
// is installed.
type ComponentStatus struct {
	Provider string `json:"provider"`
	Active   bool   `json:"active"`
}

// AppStatusResp is one app's declared contract and live state.
type AppStatusResp struct {
	Name     string `json:"name"`
	Build    string `json:"buildStrategy"`
	Deploy   string `json:"deployStrategy"`
	Deployed bool   `json:"deployed"`
	Replicas string `json:"replicas,omitempty"`
	Ready    string `json:"ready,omitempty"`
	// URL is the app's ingress address, http://<app>.<domainSuffix>, set only
	// once the app is deployed. It works only if the app's ingress is enabled
	// and the host resolves (an /etc/hosts entry on k3d or kind).
	URL string `json:"url,omitempty"`
	// ServiceURL is the app's in-cluster base URL, from its declared contract.
	// The UI sends traffic to it.
	ServiceURL string `json:"serviceUrl,omitempty"`
	// Capabilities the app declares (ADR-0014), so the UI can show which
	// scenarios it can run.
	Capabilities []string `json:"capabilities,omitempty"`
	// HPA is the live autoscaler state when an HPA, including one KEDA
	// manages, targets the app; otherwise nil.
	HPA *k8s.HPAStatus `json:"hpa,omitempty"`
}

// appURL returns the ingress URL for a deployed app, or "" when the app is not
// deployed or no domain suffix is set.
func appURL(name, domainSuffix string, deployed bool) string {
	if !deployed || domainSuffix == "" {
		return ""
	}
	return fmt.Sprintf("http://%s.%s", name, domainSuffix)
}

// appStatus returns one app's status: its declared contract plus what the
// cluster reports. /status and /apps both use it, so they always agree.
func (s *Server) appStatus(ctx context.Context, appName string) AppStatusResp {
	resp := AppStatusResp{Name: appName}
	appCfg, _ := config.LoadAppConfig(s.cfg.ProjectRoot, appName)
	if appCfg == nil {
		return resp
	}
	resp.Build = appCfg.BuildStrategy
	resp.Deploy = appCfg.DeployStrategy
	resp.ServiceURL = appCfg.Workload().URL()
	for _, c := range appCfg.Contract.Capabilities {
		resp.Capabilities = append(resp.Capabilities, string(c))
	}
	ns := appName
	if appCfg.Namespace != "" {
		ns = appCfg.Namespace
	}
	if status, _ := k8s.GetAppStatus(ctx, appName, ns); status != nil {
		resp.Deployed = status.Deployed
		resp.Replicas = status.Replicas
		resp.Ready = status.Ready
	}
	// Query the autoscaler only once the app is running.
	if resp.Deployed {
		if hpa, err := k8s.GetHPAStatus(ctx, ns, appName); err == nil && hpa.Present {
			resp.HPA = hpa
		}
	}
	resp.URL = appURL(appName, s.cfg.DomainSuffix, resp.Deployed)
	return resp
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	resp := StatusResponse{
		DomainSuffix: s.cfg.DomainSuffix,
	}

	clusterInfo, _ := k8s.GetClusterInfo(ctx)
	resp.Cluster = clusterInfo

	// Take each provider's namespace from the registry; it is not always the
	// provider's name.
	ingressActive := false
	if p, err := s.registry.GetProvider("ingress", s.cfg.IngressProvider); err == nil {
		ingressActive = k8s.NamespaceExists(ctx, p.Namespace())
	}
	metricsActive := false
	if p, err := s.registry.GetProvider("monitoring/metrics", s.cfg.MetricsProvider); err == nil {
		// Metrics shares the monitoring namespace with other components, so
		// check its Helm release instead of the namespace.
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

	apps, _ := config.ListApps(s.cfg.ProjectRoot)
	for _, appName := range apps {
		resp.Apps = append(resp.Apps, s.appStatus(ctx, appName))
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

	result := make([]AppStatusResp, 0, len(apps))
	for _, appName := range apps {
		result = append(result, s.appStatus(ctx, appName))
	}

	respondJSON(w, http.StatusOK, result)
}

// handleAppDetail returns how one app is built and deployed: an overview, its
// stack, and its Dockerfile and Helm chart with their repo paths.
func (s *Server) handleAppDetail(w http.ResponseWriter, r *http.Request) {
	name, ok := pathName(w, r, "app")
	if !ok {
		return
	}
	apps, _ := config.ListApps(s.cfg.ProjectRoot)
	if !slices.Contains(apps, name) {
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
	name, ok := pathName(w, r, "app")
	if !ok {
		return
	}
	jobID := s.exec.NextActionID()
	go func() {
		_ = s.exec.RunScriptStreamedWith(jobID, fmt.Sprintf("Deploy %s", name), "src/engine/deploy.sh", "deploy", name)
	}()
	respondJSON(w, http.StatusAccepted, map[string]string{"jobId": jobID, "status": "accepted"})
}

func (s *Server) handleAppDestroy(w http.ResponseWriter, r *http.Request) {
	name, ok := pathName(w, r, "app")
	if !ok {
		return
	}
	jobID := s.exec.NextActionID()
	go func() {
		_ = s.exec.RunScriptStreamedWith(jobID, fmt.Sprintf("Destroy %s", name), "src/engine/deploy.sh", "destroy", name)
	}()
	respondJSON(w, http.StatusAccepted, map[string]string{"jobId": jobID, "status": "accepted"})
}

func (s *Server) handleAppBuild(w http.ResponseWriter, r *http.Request) {
	name, ok := pathName(w, r, "app")
	if !ok {
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
	result := make(map[string][]map[string]any)

	for _, cat := range categories {
		providers := s.registry.GetProviders(cat)
		exclusive := s.registry.IsExclusive(cat)
		for _, p := range providers {
			// Providers that share a namespace are checked by Helm release;
			// the rest by whether their namespace exists, which is cheaper.
			installed := k8s.NamespaceExists(ctx, p.Namespace())
			if p.SharesNamespace() {
				installed = k8s.HelmReleaseExists(ctx, p.Namespace(), p.Name)
			}
			entry := map[string]any{
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

// handlePlatformUp installs the baseline platform: ingress, metrics and
// Grafana. Everything else (logging, tracing, GitOps, chaos, ...) is installed
// on demand from the Platform tab or by a scenario that needs it.
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
		// Uninstall in reverse install order.
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

// resolveCategory maps a category path segment to the registry's category
// key. A nested category such as "monitoring/metrics" cannot be one path
// segment, so the API accepts its last part ("metrics") and this maps it back.
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

// PlatformComponentDetail describes one platform component: what it is, how
// it is installed, and which scenarios depend on it.
type PlatformComponentDetail struct {
	Category        string   `json:"category"`
	Name            string   `json:"name"`
	Namespace       string   `json:"namespace"`
	Installed       bool     `json:"installed"`
	Description     string   `json:"description"`
	Provides        []string `json:"provides"`
	Ports           []string `json:"ports"`
	Dependencies    []string `json:"dependencies"`
	Resources       []string `json:"resources"`
	Chart           string   `json:"chart"`
	InstallCommands []string `json:"installCommands"`
	// InstallVars explains every shell variable in the install commands, with
	// its value where the server knows it.
	InstallVars     []InstallVar  `json:"installVars"`
	UsedInScenarios []ScenarioRef `json:"usedInScenarios"`
}

// InstallVar is one shell variable referenced by a provider's install commands,
// with its resolved value (empty when it is a secret/env-provided) and a short
// explanation.
type InstallVar struct {
	Name        string `json:"name"`
	Value       string `json:"value"`
	Description string `json:"description"`
}

// shellVarRe matches a shell variable reference ($VAR or ${VAR}) in an install
// command.
var shellVarRe = regexp.MustCompile(`\$\{?([A-Z_][A-Z0-9_]*)\}?`)

// installCommandVars replaces the shell variables whose values the server
// knows, such as $NAMESPACE and $SCRIPT_DIR, in a provider's install commands.
// It also returns an explanation of every variable that appeared. providerDir
// is the provider's directory relative to the repo root.
func installCommandVars(cmds []string, namespace, domain, providerDir string) ([]string, []InstallVar) {
	valuesFile := ""
	if providerDir != "" {
		valuesFile = providerDir + "/values.yaml"
	}
	// Variables with a known value. Secrets and other values that come from
	// the environment stay as $VAR and are only explained.
	inline := map[string]string{
		"NAMESPACE":     namespace,
		"DOMAIN_SUFFIX": domain,
		"SCRIPT_DIR":    providerDir,
		"VALUES_FILE":   valuesFile,
	}

	resolved := make([]string, len(cmds))
	for i, c := range cmds {
		for name, val := range inline {
			if val == "" {
				continue
			}
			c = strings.ReplaceAll(c, "${"+name+"}", val)
			c = strings.ReplaceAll(c, "$"+name, val)
		}
		resolved[i] = c
	}

	// Build the explanations from the original commands, so substituted
	// variables are explained too; one entry per variable, in order of
	// appearance.
	seen := map[string]bool{}
	var vars []InstallVar
	for _, c := range cmds {
		for _, m := range shellVarRe.FindAllStringSubmatch(c, -1) {
			vname := m[1]
			if seen[vname] {
				continue
			}
			seen[vname] = true
			vars = append(vars, describeInstallVar(vname, inline))
		}
	}
	return resolved, vars
}

// describeInstallVar returns the value/meaning of one install-command variable.
// Known lab variables get a concrete value; recognised secret patterns are
// flagged as env-provided; anything else is described generically.
func describeInstallVar(name string, inline map[string]string) InstallVar {
	switch name {
	case "NAMESPACE":
		return InstallVar{name, inline[name], "Kubernetes namespace this component installs into."}
	case "DOMAIN_SUFFIX":
		return InstallVar{name, inline[name], "Ingress domain suffix for this lab (host = <service>.<suffix>)."}
	case "SCRIPT_DIR":
		return InstallVar{name, inline[name], "This provider's directory, where install.sh and values.yaml live."}
	case "VALUES_FILE":
		return InstallVar{name, inline[name], "Helm values file the install renders and applies (a temp copy of this provider's values.yaml)."}
	case "MONITORING_NAMESPACE":
		return InstallVar{name, "monitoring", "Namespace of the monitoring stack (Prometheus/Grafana), used for cross-references."}
	case "PROFILE":
		return InstallVar{name, "", "Active runtime profile (k3d | kind | aks | eks), selected by the lab."}
	}
	if strings.HasSuffix(name, "_PASSWORD") || strings.HasSuffix(name, "_TOKEN") || strings.HasSuffix(name, "_KEY") {
		return InstallVar{name, "", "Secret — export this environment variable before installing (a built-in default is used otherwise)."}
	}
	return InstallVar{name, "", "Environment variable read by the install script (provide it before installing to override the default)."}
}

// nonNilStrings returns s, or an empty slice when s is nil, so it encodes as
// JSON [] rather than null.
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
	providerDir := p.Name
	if rel, err := filepath.Rel(s.cfg.ProjectRoot, p.Path); err == nil {
		providerDir = filepath.ToSlash(rel)
	}
	resolvedCmds, installVars := installCommandVars(p.InstallCommands(), p.Namespace(), s.cfg.DomainSuffix, providerDir)
	// The UI reads .length on these, so they must encode as [] and not null.
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
		InstallCommands: nonNilStrings(resolvedCmds),
		InstallVars:     installVars,
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

// scenarioResp is a scenario as the UI needs it: the scenario, the workload it
// is bound to, and the app prerequisites it names literally.
type scenarioResp struct {
	*scenario.Scenario
	PinnedApps []string     `json:"pinnedApps"`
	Workload   WorkloadResp `json:"workload"`
}

func (s *Server) handleScenarioInfo(w http.ResponseWriter, r *http.Request) {
	name, ok := pathName(w, r, "scenario")
	if !ok {
		return
	}
	sc, err := s.scenes.Get(name)
	if err != nil {
		respondError(w, r, http.StatusNotFound, "not_found", err.Error())
		return
	}

	// Expand templates for display, using the parameter defaults for
	// {{.Param}}. ResolvedForDisplay returns a copy; sc is shared and must not
	// be modified.
	defaults := s.scenes.ParamDefaults(sc)
	resp := *s.scenes.ResolvedForDisplay(sc, defaults, s.boundWorkload(s.scenes.ActiveApp(name)))

	// An active scenario is shown for the app it was activated against.
	respondJSON(w, http.StatusOK, scenarioResp{
		Scenario:   &resp,
		PinnedApps: s.scenes.PinnedPrereqApps(name),
		Workload:   workloadResp(s.boundWorkload(s.scenes.ActiveApp(name))),
	})
}

func (s *Server) handleScenarioUp(w http.ResponseWriter, r *http.Request) {
	name, ok := pathName(w, r, "scenario")
	if !ok {
		return
	}
	// Optional overrides for this activation:
	//   {"params": {"threshold": "15"}, "app": "java-api"}
	// An empty or absent body means "the declared defaults, the current binding".
	var body struct {
		Params map[string]string `json:"params"`
		App    string            `json:"app"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body) // tolerate an empty body
	}
	if body.App != "" && !isValidName(body.App) {
		respondError(w, r, http.StatusBadRequest, "invalid_input", fmt.Sprintf("invalid app name %q", body.App))
		return
	}

	jobID := s.exec.NextActionID()
	label := fmt.Sprintf("Activate scenario: %s", name)
	s.exec.BroadcastStart(jobID, label)
	go func() {
		// Bind first: Up records the bound app in the activation marker, and
		// verify and down read it from there.
		scenes, _, release, err := s.withWorkload(body.App)
		if err != nil {
			s.exec.BroadcastEnd(jobID, label, err)
			return
		}
		defer release()
		scenes.SetActivationParams(body.Params)
		s.exec.BroadcastEnd(jobID, label, scenes.Up(name, s.exec, false))
	}()
	respondJSON(w, http.StatusAccepted, map[string]string{"jobId": jobID, "status": "accepted"})
}

func (s *Server) handleScenarioDown(w http.ResponseWriter, r *http.Request) {
	name, ok := pathName(w, r, "scenario")
	if !ok {
		return
	}
	jobID := s.exec.NextActionID()
	label := fmt.Sprintf("Deactivate scenario: %s", name)
	s.exec.BroadcastStart(jobID, label)
	go func() {
		// Tear down using the app the scenario was activated against.
		scenes, _, release, err := s.withWorkload(s.scenes.ActiveApp(name))
		if err != nil {
			s.exec.BroadcastEnd(jobID, label, err)
			return
		}
		defer release()
		s.exec.BroadcastEnd(jobID, label, scenes.Down(name, s.exec))
	}()
	respondJSON(w, http.StatusAccepted, map[string]string{"jobId": jobID, "status": "accepted"})
}

// scenarioVerifyBudget is the time limit for one verify request. It equals the
// CLI's `scenario verify --timeout` default, and the UI waits as long.
const scenarioVerifyBudget = 5 * time.Minute

// handleScenarioVerify runs the scenario's checks synchronously and returns
// the per-check results. Use the CLI's --watch mode for long convergence.
func (s *Server) handleScenarioVerify(w http.ResponseWriter, r *http.Request) {
	name, ok := pathName(w, r, "scenario")
	if !ok {
		return
	}

	// Check the app the scenario was activated against; the check environment
	// below is built from it.
	scenes, _, release, err := s.withWorkload(s.scenes.ActiveApp(name))
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid_input", err.Error())
		return
	}
	defer release()

	// Keep NewRunner's per-check timeout, the same default the CLI uses.
	runner := checks.NewRunner()
	promURL := os.Getenv("PROMETHEUS_URL")
	if promURL == "" {
		promURL = "http://prometheus." + s.cfg.DomainSuffix
	}
	runner.PrometheusURL = promURL
	// The same check environment the CLI uses.
	runner.Env = []string{
		"DOMAIN_SUFFIX=" + s.cfg.DomainSuffix,
		"MONITORING_NAMESPACE=" + s.cfg.MonitoringNamespace,
		"PROJECT_ROOT=" + s.cfg.ProjectRoot,
		"PROMETHEUS_URL=" + promURL,
		"WORKLOAD_NAME=" + scenes.Workload.Name,
		"WORKLOAD_NAMESPACE=" + scenes.Workload.Namespace,
		"WORKLOAD_PORT=" + scenes.Workload.Port,
		"WORKLOAD_METRIC=" + scenes.Workload.Metric,
	}

	// Checks run one after another, each with its own timeout, so the total
	// limit must allow for a long list, as in the CLI.
	startedAt := time.Now()
	ctx, cancel := context.WithTimeout(r.Context(), scenarioVerifyBudget)
	defer cancel()

	results, err := scenes.Verify(ctx, name, runner)
	if err != nil {
		if errors.Is(err, scenario.ErrNoChecks) {
			respondError(w, r, http.StatusBadRequest, "no_checks", err.Error())
			return
		}
		respondError(w, r, http.StatusNotFound, "not_found", err.Error())
		return
	}

	// Record the result so the results view can show it.
	s.recordScenarioVerify(name, results, startedAt)

	respondJSON(w, http.StatusOK, map[string]any{
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
	name, ok := pathName(w, r, "service")
	if !ok {
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
	name, ok := pathName(w, r, "service")
	if !ok {
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

// grafanaExploreURL builds a Grafana Explore link for a datasource, using the
// ?panes=<json> parameter Grafana 11 expects and the datasource UID (loki,
// tempo or prometheus, as provisioned). An empty expr opens Explore with no
// query.
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
			// 6h rather than Grafana's 1h default: the lab's apps log mostly at
			// start-up, so a short range often shows nothing.
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

	// Resume from the client's ?after=<seq>: replay the events it missed, then
	// stream new ones. SubscribeFrom guarantees no gap and no duplicate.
	after := parseAfterCursor(r)
	backlog, actionCh, contiguous := s.exec.Broadcast.SubscribeFrom(after)
	defer s.exec.Broadcast.Unsubscribe(actionCh)

	statusTicker := time.NewTicker(5 * time.Second)
	defer statusTicker.Stop()
	pingTicker := time.NewTicker(wsPingPeriod)
	defer pingTicker.Stop()

	// Keepalive: expect a pong (or any read) within wsPongWait.
	_ = conn.SetReadDeadline(time.Now().Add(wsPongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(wsPongWait))
	})

	// Read and discard incoming messages, to notice when the client goes away.
	closeCh := make(chan struct{})
	go func() {
		defer close(closeCh)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	writeJSON := func(v any) error {
		_ = conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
		return conn.WriteJSON(v)
	}
	sendStatus := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		info, _ := k8s.GetClusterInfo(ctx)
		cancel()
		return writeJSON(map[string]any{"type": "status", "data": info})
	}

	// If events after the client's cursor were dropped, tell it to resync from
	// GET /jobs.
	if !contiguous {
		if err := writeJSON(map[string]any{"type": "resync"}); err != nil {
			return
		}
	}
	// Replay missed events before streaming new ones.
	for _, event := range backlog {
		if err := writeJSON(map[string]any{"type": "action", "data": event}); err != nil {
			return
		}
	}

	// Send the cluster state now, rather than at the first tick.
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
			if err := writeJSON(map[string]any{
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
	name, ok := pathName(w, r, "runtime")
	if !ok {
		return
	}
	jobID := s.exec.NextActionID()
	go func() {
		_ = s.runtimes.ActivateWith(jobID, name, s.exec)
	}()
	respondJSON(w, http.StatusAccepted, map[string]string{"jobId": jobID, "status": "accepted"})
}

func (s *Server) handleRuntimeDeactivate(w http.ResponseWriter, r *http.Request) {
	name, ok := pathName(w, r, "runtime")
	if !ok {
		return
	}
	jobID := s.exec.NextActionID()
	go func() {
		_ = s.runtimes.DeactivateWith(jobID, name, s.exec)
	}()
	respondJSON(w, http.StatusAccepted, map[string]string{"jobId": jobID, "status": "accepted"})
}
