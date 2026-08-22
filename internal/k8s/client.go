// SPDX-License-Identifier: Apache-2.0
package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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

// GetClusterInfo returns current cluster information.
func GetClusterInfo(ctx context.Context) (*ClusterInfo, error) {
	info := &ClusterInfo{}

	// Get current context
	ctxOut, err := kubectl(ctx, "config", "current-context")
	if err != nil {
		return info, nil // not connected
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
		return status, nil // Not deployed
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
