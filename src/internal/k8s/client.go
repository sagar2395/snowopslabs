// SPDX-License-Identifier: Apache-2.0

// Package k8s reads cluster state by running kubectl: cluster info, pods,
// deployments, autoscalers, namespaces and Helm releases.
package k8s

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// ClusterInfo holds basic cluster information.
type ClusterInfo struct {
	Context    string `json:"context"`
	Server     string `json:"server"`
	K8sVersion string `json:"k8sVersion"`
	NodeCount  int    `json:"nodeCount"`
	Connected  bool   `json:"connected"`
}

// PodInfo holds information about a pod.
type PodInfo struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Status    string `json:"status"`
	Ready     string `json:"ready"`
	Restarts  string `json:"restarts"`
	Age       string `json:"age"`
}

// AppStatus holds the deployment status of an application.
type AppStatus struct {
	Name      string    `json:"name"`
	Namespace string    `json:"namespace"`
	Replicas  string    `json:"replicas"`
	Ready     string    `json:"ready"`
	Available string    `json:"available"`
	Pods      []PodInfo `json:"pods"`
	Deployed  bool      `json:"deployed"`
}

// HPAStatus holds the live autoscaler state for a Deployment — replica counts,
// bounds, and the driving metric vs its target — the same info `kubectl get hpa`
// prints, structured for the UI. Present is false when no HPA targets the
// Deployment (KEDA-created HPAs count; KEDA renders a normal HPA underneath).
type HPAStatus struct {
	Present         bool   `json:"present"`
	Name            string `json:"name"`
	MinReplicas     int    `json:"minReplicas"`
	MaxReplicas     int    `json:"maxReplicas"`
	CurrentReplicas int    `json:"currentReplicas"`
	DesiredReplicas int    `json:"desiredReplicas"`
	// The scaling trigger: an External metric for KEDA/Prometheus, a Resource
	// metric (cpu/memory) for a classic HPA. Current/Target are pre-rendered
	// strings, e.g. "27467m / 25" or "27% / 80%".
	MetricName    string `json:"metricName,omitempty"`
	MetricCurrent string `json:"metricCurrent,omitempty"`
	MetricTarget  string `json:"metricTarget,omitempty"`
}

// GetClusterInfo returns current cluster information.
func GetClusterInfo(ctx context.Context) (*ClusterInfo, error) {
	info := &ClusterInfo{}

	ctxOut, err := kubectl(ctx, "config", "current-context")
	if err != nil {
		return info, nil //nolint:nilerr // no current-context means not connected — report empty info, not an error
	}
	info.Context = ctxOut
	info.Connected = true

	serverOut, err := kubectl(ctx, "config", "view", "--minify", "-o", "jsonpath={.clusters[0].cluster.server}")
	if err == nil {
		info.Server = serverOut
	}

	// JSON output, because kubectl deprecated --short.
	info.K8sVersion = "unknown"
	versionJSON, err := kubectl(ctx, "version", "-o", "json")
	if err == nil {
		var vOut struct {
			ServerVersion struct {
				GitVersion string `json:"gitVersion"`
			} `json:"serverVersion"`
		}
		if json.Unmarshal([]byte(versionJSON), &vOut) == nil && vOut.ServerVersion.GitVersion != "" {
			info.K8sVersion = vOut.ServerVersion.GitVersion
		}
	}

	nodesOut, err := kubectl(ctx, "get", "nodes", "--no-headers")
	if err == nil && nodesOut != "" {
		info.NodeCount = len(strings.Split(strings.TrimSpace(nodesOut), "\n"))
	}

	return info, nil
}

// podListJSON is the minimal JSON structure returned by `kubectl get pods -o json`.
type podListJSON struct {
	Items []struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Status struct {
			Phase             string `json:"phase"`
			ContainerStatuses []struct {
				Ready        bool  `json:"ready"`
				RestartCount int32 `json:"restartCount"`
			} `json:"containerStatuses"`
		} `json:"status"`
	} `json:"items"`
}

// GetNamespacePods returns pods in a namespace using JSON output for reliable parsing.
func GetNamespacePods(ctx context.Context, namespace string) ([]PodInfo, error) {
	out, err := kubectl(ctx, "get", "pods", "-n", namespace, "-o", "json")
	if err != nil {
		return nil, err
	}

	var list podListJSON
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		return nil, fmt.Errorf("parsing pod list JSON: %w", err)
	}

	var pods []PodInfo
	for _, item := range list.Items {
		readyCount := 0
		totalCount := len(item.Status.ContainerStatuses)
		var restarts int32
		for _, cs := range item.Status.ContainerStatuses {
			if cs.Ready {
				readyCount++
			}
			restarts += cs.RestartCount
		}

		pods = append(pods, PodInfo{
			Name:      item.Metadata.Name,
			Namespace: namespace,
			Status:    item.Status.Phase,
			Ready:     fmt.Sprintf("%d/%d", readyCount, totalCount),
			Restarts:  strconv.Itoa(int(restarts)),
		})
	}
	return pods, nil
}

// GetAppStatus returns the deployment status of an app.
func GetAppStatus(ctx context.Context, appName, namespace string) (*AppStatus, error) {
	status := &AppStatus{
		Name:      appName,
		Namespace: namespace,
	}

	_, err := kubectl(ctx, "get", "namespace", namespace, "--no-headers")
	if err != nil {
		return status, nil //nolint:nilerr // a missing namespace means the app is simply not deployed
	}

	deplOut, err := kubectl(ctx, "get", "deployment", "-n", namespace, "--no-headers",
		"-o", "custom-columns=NAME:.metadata.name,REPLICAS:.spec.replicas,READY:.status.readyReplicas,AVAILABLE:.status.availableReplicas")
	if err == nil && deplOut != "" {
		status.Deployed = true
		fields := strings.Fields(deplOut)
		if len(fields) >= 2 {
			status.Replicas = fields[1]
		}
		if len(fields) >= 3 {
			status.Ready = fields[2]
		}
		if len(fields) >= 4 {
			status.Available = fields[3]
		}
	}

	pods, err := GetNamespacePods(ctx, namespace)
	if err == nil {
		status.Pods = pods
	}

	return status, nil
}

// GetHPAStatus returns the HPA state for a Deployment, or Present=false when
// none targets it. "No HPA" is never an error — it just means "not autoscaled".
func GetHPAStatus(ctx context.Context, namespace, deploymentName string) (*HPAStatus, error) {
	out, err := kubectl(ctx, "get", "hpa", "-n", namespace, "-o", "json")
	if err != nil {
		// A missing namespace or no HPA resource is not an error for the caller;
		// it simply means the app is not autoscaled.
		return &HPAStatus{Present: false}, nil //nolint:nilerr
	}
	return parseHPAList(out, deploymentName)
}

// hpaMetricValue is the subset of a v2 HPA metric target/current we render.
type hpaMetricValue struct {
	AverageValue       string `json:"averageValue"`
	Value              string `json:"value"`
	AverageUtilization *int   `json:"averageUtilization"`
}

type hpaMetric struct {
	Type     string `json:"type"`
	External *struct {
		Metric  struct{ Name string } `json:"metric"`
		Target  hpaMetricValue        `json:"target"`
		Current hpaMetricValue        `json:"current"`
	} `json:"external"`
	Resource *struct {
		Name    string         `json:"name"`
		Target  hpaMetricValue `json:"target"`
		Current hpaMetricValue `json:"current"`
	} `json:"resource"`
}

// parseHPAList finds the HPA in `kubectl get hpa -o json` output whose
// scaleTargetRef is deploymentName, and returns its status.
func parseHPAList(raw, deploymentName string) (*HPAStatus, error) {
	var list struct {
		Items []struct {
			Metadata struct{ Name string } `json:"metadata"`
			Spec     struct {
				ScaleTargetRef struct {
					Kind string `json:"kind"`
					Name string `json:"name"`
				} `json:"scaleTargetRef"`
				MinReplicas *int        `json:"minReplicas"`
				MaxReplicas int         `json:"maxReplicas"`
				Metrics     []hpaMetric `json:"metrics"`
			} `json:"spec"`
			Status struct {
				CurrentReplicas int         `json:"currentReplicas"`
				DesiredReplicas int         `json:"desiredReplicas"`
				CurrentMetrics  []hpaMetric `json:"currentMetrics"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return nil, fmt.Errorf("parsing hpa list: %w", err)
	}

	for _, it := range list.Items {
		if it.Spec.ScaleTargetRef.Name != deploymentName {
			continue
		}
		hpa := &HPAStatus{
			Present:         true,
			Name:            it.Metadata.Name,
			MaxReplicas:     it.Spec.MaxReplicas,
			CurrentReplicas: it.Status.CurrentReplicas,
			DesiredReplicas: it.Status.DesiredReplicas,
		}
		if it.Spec.MinReplicas != nil {
			hpa.MinReplicas = *it.Spec.MinReplicas
		} else {
			hpa.MinReplicas = 1 // HPA default when unset
		}
		if len(it.Spec.Metrics) > 0 {
			name, target := metricNameAndValue(it.Spec.Metrics[0], true)
			hpa.MetricName = name
			hpa.MetricTarget = target
		}
		if len(it.Status.CurrentMetrics) > 0 {
			_, current := metricNameAndValue(it.Status.CurrentMetrics[0], false)
			hpa.MetricCurrent = current
		}
		return hpa, nil
	}
	return &HPAStatus{Present: false}, nil
}

// metricNameAndValue extracts a v2 HPA metric's name and rendered value —
// target when target is true, else current. Handles External (KEDA/Prometheus)
// and Resource (cpu/memory) metrics, the two kinds this lab produces. Values are
// humanized (300m → 0.3) and KEDA's "s0-" scaler prefix is stripped from names.
func metricNameAndValue(m hpaMetric, target bool) (name, value string) {
	pick := func(v hpaMetricValue, suffix string) string {
		switch {
		case v.AverageValue != "":
			return humanizeQuantity(v.AverageValue)
		case v.Value != "":
			return humanizeQuantity(v.Value)
		case v.AverageUtilization != nil:
			return fmt.Sprintf("%d%s", *v.AverageUtilization, suffix)
		}
		return ""
	}
	switch {
	case m.External != nil:
		name = cleanMetricName(m.External.Metric.Name)
		if target {
			value = pick(m.External.Target, "")
		} else {
			value = pick(m.External.Current, "")
		}
	case m.Resource != nil:
		name = m.Resource.Name
		if target {
			value = pick(m.Resource.Target, "%")
		} else {
			value = pick(m.Resource.Current, "%")
		}
	}
	return name, value
}

// kedaScalerPrefix matches the "s0-"/"s1-" scaler-index prefix KEDA prepends to
// the external metric names it registers, optionally followed by the scaler-type
// word (e.g. "s0-prometheus-go_api_requests_per_second" or "s0-kafka-lag").
var kedaScalerPrefix = regexp.MustCompile(`^s\d+-(?:prometheus|kafka|cron|cpu|memory)-`)

// cleanMetricName strips KEDA's scaler prefix, so the UI shows the metric name
// the learner wrote.
func cleanMetricName(name string) string {
	if cleaned := kedaScalerPrefix.ReplaceAllString(name, ""); cleaned != name {
		return cleaned
	}
	return regexp.MustCompile(`^s\d+-`).ReplaceAllString(name, "")
}

// humanizeQuantity converts a milli quantity ("300m") or plain integer to a
// decimal ("0.3"). Anything else, such as binary suffixes, is returned
// unchanged.
func humanizeQuantity(q string) string {
	if before, ok := strings.CutSuffix(q, "m"); ok {
		if n, err := strconv.ParseInt(before, 10, 64); err == nil {
			return strconv.FormatFloat(float64(n)/1000, 'f', -1, 64)
		}
	}
	return q
}

// NamespaceHealth counts the pods in a namespace and how many of them are
// fully ready. exists reports whether the namespace is there at all, so a
// caller can tell "not installed" from "installed and broken".
func NamespaceHealth(ctx context.Context, namespace string) (ready, total int, exists bool) {
	if !NamespaceExists(ctx, namespace) {
		return 0, 0, false
	}
	pods, err := GetNamespacePods(ctx, namespace)
	if err != nil {
		return 0, 0, true
	}
	for _, p := range pods {
		total++
		// A finished Job pod is not unhealthy, so it is not counted.
		if p.Status == "Succeeded" || (p.Status == "Running" && allContainersReady(p.Ready)) {
			ready++
		}
	}
	return ready, total, true
}

// allContainersReady parses the "n/m" readiness string PodInfo carries.
func allContainersReady(readyField string) bool {
	n, m, found := strings.Cut(readyField, "/")
	if !found {
		return false
	}
	got, err1 := strconv.Atoi(n)
	want, err2 := strconv.Atoi(m)
	return err1 == nil && err2 == nil && want > 0 && got == want
}

// NamespaceExists checks if a namespace exists.
func NamespaceExists(ctx context.Context, namespace string) bool {
	_, err := kubectl(ctx, "get", "namespace", namespace, "--no-headers")
	return err == nil
}

// ServiceExists checks if a service exists in a namespace.
func ServiceExists(ctx context.Context, namespace, name string) bool {
	_, err := kubectl(ctx, "get", "service", name, "-n", namespace, "--no-headers")
	return err == nil
}

// HelmReleaseExists reports whether a Helm release named release exists in
// namespace, by looking for the Secrets Helm labels owner=helm,name=<release>.
// It tells apart components that share a namespace, such as the monitoring
// stack. A missing namespace or unreachable cluster returns false.
func HelmReleaseExists(ctx context.Context, namespace, release string) bool {
	if namespace == "" || release == "" {
		return false
	}
	out, err := kubectl(ctx, "get", "secret", "-n", namespace,
		"-l", "owner=helm,name="+release, "--no-headers")
	return err == nil && strings.TrimSpace(out) != ""
}

// GetCurrentContext returns the current kubectl context name.
func GetCurrentContext(ctx context.Context) (string, error) {
	return kubectl(ctx, "config", "current-context")
}

// IngressHosts returns every hostname an Ingress in the cluster serves, in the
// order kubectl lists them. Duplicates and rules without a host are dropped.
func IngressHosts(ctx context.Context) ([]string, error) {
	out, err := kubectl(ctx, "get", "ingress", "--all-namespaces",
		"-o", `jsonpath={range .items[*]}{range .spec.rules[*]}{.host}{"\n"}{end}{end}`)
	if err != nil {
		return nil, err
	}
	return uniqueLines(out), nil
}

func uniqueLines(out string) []string {
	seen := map[string]bool{}
	lines := []string{}
	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !seen[line] {
			seen[line] = true
			lines = append(lines, line)
		}
	}
	return lines
}

// RunKubectl executes a kubectl command and returns its stdout.
func RunKubectl(ctx context.Context, args ...string) (string, error) {
	return kubectl(ctx, args...)
}

func kubectl(ctx context.Context, args ...string) (string, error) {
	path, err := exec.LookPath("kubectl")
	if err != nil {
		return "", errors.New("kubectl not found in PATH")
	}

	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
