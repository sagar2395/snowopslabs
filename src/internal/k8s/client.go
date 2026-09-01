// SPDX-License-Identifier: Apache-2.0
package k8s

import (
	"context"
	"encoding/json"
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

	// Get current context
	ctxOut, err := kubectl(ctx, "config", "current-context")
	if err != nil {
		return info, nil //nolint:nilerr // no current-context means not connected — report empty info, not an error
	}
	info.Context = ctxOut
	info.Connected = true

	// Get server URL
	serverOut, err := kubectl(ctx, "config", "view", "--minify", "-o", "jsonpath={.clusters[0].cluster.server}")
	if err == nil {
		info.Server = serverOut
	}

	// Get k8s version via structured JSON (--short is deprecated since 1.24).
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

	// Get node count
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
			Restarts:  fmt.Sprintf("%d", restarts),
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

	// Check if the namespace exists
	_, err := kubectl(ctx, "get", "namespace", namespace, "--no-headers")
	if err != nil {
		return status, nil //nolint:nilerr // a missing namespace means the app is simply not deployed
	}

	// Get deployment info
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

	// Get pods
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

// parseHPAList finds the HPA whose scaleTargetRef points at deploymentName and
// returns its structured status. Kept as a pure function (no kubectl) so it can
// be unit-tested with fixture JSON.
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

// cleanMetricName strips KEDA's scaler prefix so the UI shows the raw metric a
// learner declared, not KEDA's internal registration name. The bare "s0-" form
// (no scaler-type word) is stripped too.
func cleanMetricName(name string) string {
	if cleaned := kedaScalerPrefix.ReplaceAllString(name, ""); cleaned != name {
		return cleaned
	}
	return regexp.MustCompile(`^s\d+-`).ReplaceAllString(name, "")
}

// humanizeQuantity renders a Kubernetes quantity as a plain decimal. HPA metric
// values below 1 come back in milli notation ("300m" = 0.3), which is opaque to
// most users; this converts the common milli and plain-integer cases and leaves
// anything else (binary suffixes, unusual units) untouched.
func humanizeQuantity(q string) string {
	if strings.HasSuffix(q, "m") {
		if n, err := strconv.ParseInt(strings.TrimSuffix(q, "m"), 10, 64); err == nil {
			return strconv.FormatFloat(float64(n)/1000, 'f', -1, 64)
		}
	}
	return q
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

// HelmReleaseExists reports whether a Helm 3 release named `release` currently
// exists in `namespace`. Helm 3 records each release as one or more Secrets
// labelled `owner=helm,name=<release>`, so a matching Secret means the release
// is present on the cluster.
//
// This is how we tell apart components that SHARE a namespace: prometheus,
// grafana, loki and tempo all install into the monitoring namespace, so plain
// namespace existence reads "installed" for all four the moment any one of them
// lands. A missing namespace makes kubectl error, which returns false — exactly
// what we want after a cluster teardown.
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

// RunKubectl executes a kubectl command and returns its stdout.
func RunKubectl(ctx context.Context, args ...string) (string, error) {
	return kubectl(ctx, args...)
}

func kubectl(ctx context.Context, args ...string) (string, error) {
	path, err := exec.LookPath("kubectl")
	if err != nil {
		return "", fmt.Errorf("kubectl not found in PATH")
	}

	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
