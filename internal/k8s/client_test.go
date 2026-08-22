// SPDX-License-Identifier: Apache-2.0
package k8s

import (
	"encoding/json"
	"fmt"
	"testing"
)

// parseVersionJSON exercises the same JSON unmarshaling logic as GetClusterInfo.
func parseVersionJSON(raw string) string {
	var vOut struct {
		ServerVersion struct {
			GitVersion string `json:"gitVersion"`
		} `json:"serverVersion"`
	}
	if json.Unmarshal([]byte(raw), &vOut) == nil && vOut.ServerVersion.GitVersion != "" {
		return vOut.ServerVersion.GitVersion
	}
	return "unknown"
}

func TestParseVersionJSON_HappyPath(t *testing.T) {
	raw := `{"clientVersion":{"gitVersion":"v1.36.1"},"serverVersion":{"gitVersion":"v1.31.0+k3s1"}}`
	got := parseVersionJSON(raw)
	if got != "v1.31.0+k3s1" {
		t.Errorf("got %q, want %q", got, "v1.31.0+k3s1")
	}
}

func TestParseVersionJSON_Offline(t *testing.T) {
	// When cluster is unreachable, kubectl version -o json omits serverVersion.
	raw := `{"clientVersion":{"gitVersion":"v1.36.1"}}`
	got := parseVersionJSON(raw)
	if got != "unknown" {
		t.Errorf("got %q, want \"unknown\"", got)
	}
}

func TestParseVersionJSON_Empty(t *testing.T) {
	got := parseVersionJSON("")
	if got != "unknown" {
		t.Errorf("got %q, want \"unknown\"", got)
	}
}

// parsePodListJSON exercises the same JSON unmarshaling logic used by GetNamespacePods.
func parsePodListJSON(raw, namespace string) ([]PodInfo, error) {
	var list podListJSON
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return nil, err
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

const runningPodJSON = `{
  "items": [{
    "metadata": {"name": "go-api-abc"},
    "status": {
      "phase": "Running",
      "containerStatuses": [
        {"ready": true, "restartCount": 0}
      ]
    }
  }]
}`

const pendingPodJSON = `{
  "items": [{
    "metadata": {"name": "go-api-xyz"},
    "status": {
      "phase": "Pending",
      "containerStatuses": []
    }
  }]
}`

const restartsPodJSON = `{
  "items": [{
    "metadata": {"name": "crasher-pod"},
    "status": {
      "phase": "Running",
      "containerStatuses": [
        {"ready": true,  "restartCount": 3},
        {"ready": false, "restartCount": 2}
      ]
    }
  }]
}`

func TestParsePodList_RunningPod(t *testing.T) {
	pods, err := parsePodListJSON(runningPodJSON, "go-api")
	if err != nil {
		t.Fatalf("parsePodListJSON: %v", err)
	}
	if len(pods) != 1 {
		t.Fatalf("expected 1 pod, got %d", len(pods))
	}
	p := pods[0]
	if p.Name != "go-api-abc" {
		t.Errorf("Name: got %q, want %q", p.Name, "go-api-abc")
	}
	if p.Status != "Running" {
		t.Errorf("Status: got %q, want %q", p.Status, "Running")
	}
	if p.Ready != "1/1" {
		t.Errorf("Ready: got %q, want %q", p.Ready, "1/1")
	}
	if p.Restarts != "0" {
		t.Errorf("Restarts: got %q, want %q", p.Restarts, "0")
	}
}

func TestParsePodList_PendingPod(t *testing.T) {
	pods, err := parsePodListJSON(pendingPodJSON, "go-api")
	if err != nil {
		t.Fatalf("parsePodListJSON: %v", err)
	}
	if len(pods) != 1 {
		t.Fatalf("expected 1 pod, got %d", len(pods))
	}
	p := pods[0]
	if p.Status != "Pending" {
		t.Errorf("Status: got %q, want %q", p.Status, "Pending")
	}
	if p.Ready != "0/0" {
		t.Errorf("Ready: got %q, want %q", p.Ready, "0/0")
	}
}

func TestParsePodList_MultiContainerRestarts(t *testing.T) {
	pods, err := parsePodListJSON(restartsPodJSON, "default")
	if err != nil {
		t.Fatalf("parsePodListJSON: %v", err)
	}
	if len(pods) != 1 {
		t.Fatalf("expected 1 pod, got %d", len(pods))
	}
	p := pods[0]
	if p.Ready != "1/2" {
		t.Errorf("Ready: got %q, want %q", p.Ready, "1/2")
	}
	if p.Restarts != "5" {
		t.Errorf("Restarts: got %q, want %q", p.Restarts, "5")
	}
}
