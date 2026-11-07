// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
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
			Restarts:  strconv.Itoa(int(restarts)),
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

// kedaHPAJSON is a KEDA-created v2 HPA driven by an External (Prometheus) metric
// - the shape the autoscaling-under-load scenario produces under load.
const kedaHPAJSON = `{
  "items": [
    {
      "metadata": {"name": "keda-hpa-go-api"},
      "spec": {
        "scaleTargetRef": {"kind": "Deployment", "name": "go-api"},
        "minReplicas": 1,
        "maxReplicas": 6,
        "metrics": [
          {"type": "External", "external": {
            "metric": {"name": "s0-prometheus-go_api_requests_per_second"},
            "target": {"type": "AverageValue", "averageValue": "25"}
          }}
        ]
      },
      "status": {
        "currentReplicas": 3,
        "desiredReplicas": 3,
        "currentMetrics": [
          {"type": "External", "external": {
            "metric": {"name": "s0-prometheus-go_api_requests_per_second"},
            "current": {"averageValue": "27467m"}
          }}
        ]
      }
    }
  ]
}`

// cpuHPAJSON is a classic Resource (CPU utilization) HPA targeting a different
// deployment, used to prove selection-by-name and the "%" rendering.
const cpuHPAJSON = `{
  "items": [
    {
      "metadata": {"name": "web-hpa"},
      "spec": {
        "scaleTargetRef": {"kind": "Deployment", "name": "web"},
        "minReplicas": 2,
        "maxReplicas": 10,
        "metrics": [
          {"type": "Resource", "resource": {"name": "cpu", "target": {"type": "Utilization", "averageUtilization": 80}}}
        ]
      },
      "status": {
        "currentReplicas": 4,
        "desiredReplicas": 5,
        "currentMetrics": [
          {"type": "Resource", "resource": {"name": "cpu", "current": {"averageUtilization": 92}}}
        ]
      }
    }
  ]
}`

func TestParseHPAList_KEDAExternalMetric(t *testing.T) {
	hpa, err := parseHPAList(kedaHPAJSON, "go-api")
	if err != nil {
		t.Fatalf("parseHPAList: %v", err)
	}
	if !hpa.Present {
		t.Fatal("expected HPA to be present for go-api")
	}
	if hpa.MinReplicas != 1 || hpa.MaxReplicas != 6 {
		t.Errorf("bounds: got %d/%d, want 1/6", hpa.MinReplicas, hpa.MaxReplicas)
	}
	if hpa.CurrentReplicas != 3 || hpa.DesiredReplicas != 3 {
		t.Errorf("replicas: got current=%d desired=%d, want 3/3", hpa.CurrentReplicas, hpa.DesiredReplicas)
	}
	if hpa.MetricName != "go_api_requests_per_second" {
		t.Errorf("name: got %q, want %q (KEDA prefix must be stripped)", hpa.MetricName, "go_api_requests_per_second")
	}
	if hpa.MetricTarget != "25" {
		t.Errorf("target: got %q, want %q", hpa.MetricTarget, "25")
	}
	// 27467m (milli) must be humanized to a plain decimal.
	if hpa.MetricCurrent != "27.467" {
		t.Errorf("current: got %q, want %q", hpa.MetricCurrent, "27.467")
	}
}

func TestCleanMetricName(t *testing.T) {
	cases := map[string]string{
		"s0-prometheus-go_api_requests_per_second": "go_api_requests_per_second",
		"s0-go_api_requests_per_second":            "go_api_requests_per_second",
		"s1-kafka-lag":                             "lag",
		"go_api_requests_per_second":               "go_api_requests_per_second",
		"cpu":                                      "cpu",
	}
	for in, want := range cases {
		if got := cleanMetricName(in); got != want {
			t.Errorf("cleanMetricName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHumanizeQuantity(t *testing.T) {
	cases := map[string]string{
		"300m":   "0.3",
		"27467m": "27.467",
		"1000m":  "1",
		"25":     "25",
		"80%":    "80%", // not a quantity - left as-is
		"":       "",
	}
	for in, want := range cases {
		if got := humanizeQuantity(in); got != want {
			t.Errorf("humanizeQuantity(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseHPAList_CPUResourceMetricRendersPercent(t *testing.T) {
	hpa, err := parseHPAList(cpuHPAJSON, "web")
	if err != nil {
		t.Fatalf("parseHPAList: %v", err)
	}
	if hpa.MetricName != "cpu" {
		t.Errorf("name: got %q, want %q", hpa.MetricName, "cpu")
	}
	if hpa.MetricTarget != "80%" {
		t.Errorf("target: got %q, want %q", hpa.MetricTarget, "80%")
	}
	if hpa.MetricCurrent != "92%" {
		t.Errorf("current: got %q, want %q", hpa.MetricCurrent, "92%")
	}
	if hpa.DesiredReplicas != 5 {
		t.Errorf("desired: got %d, want 5", hpa.DesiredReplicas)
	}
}

func TestParseHPAList_NoMatchingDeployment(t *testing.T) {
	hpa, err := parseHPAList(kedaHPAJSON, "some-other-app")
	if err != nil {
		t.Fatalf("parseHPAList: %v", err)
	}
	if hpa.Present {
		t.Error("expected Present=false when no HPA targets the deployment")
	}
}

func TestParseHPAList_EmptyList(t *testing.T) {
	hpa, err := parseHPAList(`{"items": []}`, "go-api")
	if err != nil {
		t.Fatalf("parseHPAList: %v", err)
	}
	if hpa.Present {
		t.Error("expected Present=false for an empty HPA list")
	}
}

func TestUniqueLines(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{name: "hosts in order", in: "a.k3d.local\nb.k3d.local", want: []string{"a.k3d.local", "b.k3d.local"}},
		{name: "duplicates and blanks dropped", in: "a\n\n a \nb\na\n", want: []string{"a", "b"}},
		{name: "no ingress at all", in: "", want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := uniqueLines(tt.in); fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Errorf("uniqueLines(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// stubKubectl puts a fake kubectl first on PATH. script is the body of a POSIX
// shell script that sees kubectl's arguments as "$*".
func stubKubectl(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "kubectl")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil { //nolint:gosec // test stub must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestGetClusterInfo(t *testing.T) {
	tests := []struct {
		name          string
		script        string
		wantConnected bool
		wantError     string
		wantNodes     int
	}{
		{
			name: "a reachable cluster is connected",
			script: `case "$*" in
  "config current-context") echo k3d-snowops ;;
  *readyz*) echo ok ;;
  *"--minify"*) echo https://127.0.0.1:6443 ;;
  "version -o json") echo '{"serverVersion":{"gitVersion":"v1.33.6+k3s1"}}' ;;
  "get nodes --no-headers") printf 'a Ready\nb Ready\n' ;;
esac`,
			wantConnected: true,
			wantNodes:     2,
		},
		{
			name: "a configured but unreachable cluster is not connected",
			script: `case "$*" in
  "config current-context") echo k3d-snowops ;;
  *readyz*) echo "Unable to connect to the server: net/http: TLS handshake timeout" >&2; exit 1 ;;
esac`,
			wantError: "net/http: TLS handshake timeout",
		},
		{
			name:   "no current context is not connected",
			script: `exit 1`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubKubectl(t, tt.script)
			info, err := GetClusterInfo(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if info.Connected != tt.wantConnected {
				t.Errorf("Connected = %v, want %v", info.Connected, tt.wantConnected)
			}
			if info.Error != tt.wantError {
				t.Errorf("Error = %q, want %q", info.Error, tt.wantError)
			}
			if info.NodeCount != tt.wantNodes {
				t.Errorf("NodeCount = %d, want %d", info.NodeCount, tt.wantNodes)
			}
		})
	}
}

func TestNotReadyNodes(t *testing.T) {
	stubKubectl(t, `case "$*" in
  "get nodes --no-headers") printf '%s\n' \
    'k3d-snowops-server-0   Ready                      control-plane,master   3d   v1.33.6+k3s1' \
    'k3d-snowops-agent-0    NotReady                   <none>                 3d   v1.33.6+k3s1' \
    'k3d-snowops-agent-1    Ready,SchedulingDisabled   <none>                 3d   v1.33.6+k3s1' \
    'k3d-snowops-agent-2    Unknown                    <none>                 3d   v1.33.6+k3s1' ;;
esac`)
	got, err := NotReadyNodes(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"k3d-snowops-agent-0", "k3d-snowops-agent-2"}
	if !slices.Equal(got, want) {
		t.Errorf("NotReadyNodes = %v, want %v", got, want)
	}

	stubKubectl(t, `exit 1`)
	if _, err := NotReadyNodes(t.Context()); err == nil {
		t.Error("expected an error when kubectl fails")
	}
}

func TestHelmReleaseDeployed(t *testing.T) {
	// Only a secret labelled status=deployed counts; a failed install leaves
	// status=failed, which must not make init skip the install.
	stubKubectl(t, `case "$*" in
  *"name=grafana,status=deployed"*) echo "sh.helm.release.v1.grafana.v1 helm.sh/release.v1 1 5m" ;;
esac`)
	if !HelmReleaseDeployed(t.Context(), "monitoring", "grafana") {
		t.Error("a deployed release should count")
	}
	if HelmReleaseDeployed(t.Context(), "monitoring", "prometheus") {
		t.Error("a release with no deployed revision must not count")
	}
}

func TestReadInventory(t *testing.T) {
	const cluster = `case "$*" in
  *"get namespaces"*) printf 'namespace/default\nnamespace/keda\nnamespace/monitoring\n' ;;
  *"owner=helm"*) printf 'keda         keda\nmonitoring   grafana\nmonitoring   grafana\n' ;;
esac`
	tests := []struct {
		name    string
		kubectl string
		wantErr string
		ns      map[string]bool
		release map[[2]string]bool
	}{
		{
			name:    "namespaces and releases from one listing each",
			kubectl: cluster,
			ns:      map[string]bool{"keda": true, "monitoring": true, "vault": false},
			release: map[[2]string]bool{
				{"keda", "keda"}: true, {"monitoring", "grafana"}: true,
				{"monitoring", "loki"}: false, {"keda", "grafana"}: false,
			},
		},
		{
			name: "a release without a deployed revision does not count",
			kubectl: `case "$*" in
  *"get namespaces"*) echo namespace/monitoring ;;
  *"owner=helm,status=deployed"*) echo 'monitoring   grafana' ;;
  *"owner=helm"*) printf 'monitoring   grafana\nmonitoring   loki\n' ;;
esac`,
			ns:      map[string]bool{"monitoring": true},
			release: map[[2]string]bool{{"monitoring", "grafana"}: true, {"monitoring", "loki"}: false},
		},
		{
			name:    "an empty cluster holds nothing",
			kubectl: "exit 0\n",
			ns:      map[string]bool{"keda": false},
			release: map[[2]string]bool{{"keda", "keda"}: false},
		},
		{
			name:    "namespaces that cannot be listed are an error",
			kubectl: "echo 'Unable to connect to the server' >&2; exit 1\n",
			wantErr: "listing namespaces",
		},
		{
			name: "releases that cannot be listed are an error",
			kubectl: `case "$*" in
  *"get namespaces"*) echo namespace/default ;;
  *) echo 'secrets is forbidden' >&2; exit 1 ;;
esac`,
			wantErr: "listing Helm releases",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubKubectl(t, tt.kubectl)
			inv, err := ReadInventory(t.Context())
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ReadInventory() error = %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ReadInventory() error = %v", err)
			}
			for ns, want := range tt.ns {
				if got := inv.NamespaceExists(ns); got != want {
					t.Errorf("NamespaceExists(%q) = %v, want %v", ns, got, want)
				}
			}
			for rel, want := range tt.release {
				if got := inv.HelmReleaseDeployed(rel[0], rel[1]); got != want {
					t.Errorf("HelmReleaseDeployed(%q, %q) = %v, want %v", rel[0], rel[1], got, want)
				}
			}
		})
	}
}

func TestUnreachableReason(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name: "kubectl stderr, generic prefix dropped",
			err: &kubectlError{
				err:    errors.New("exit status 1"),
				stderr: "E0927 noise\nUnable to connect to the server: net/http: TLS handshake timeout",
			},
			expected: "net/http: TLS handshake timeout",
		},
		{name: "plain error", err: errors.New("kubectl not found in PATH"), expected: "kubectl not found in PATH"},
		{
			name:     "long message is cut on a rune boundary",
			err:      errors.New(strings.Repeat("é", maxReasonRunes+10)),
			expected: strings.Repeat("é", maxReasonRunes) + "…",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := unreachableReason(tt.err); got != tt.expected {
				t.Errorf("unreachableReason = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestAgentCount(t *testing.T) {
	tests := []struct {
		name     string
		script   string
		expected int
		wantErr  bool
	}{
		{name: "two agents", script: `printf 'node/k3d-lab-agent-0\nnode/k3d-lab-agent-1\n'`, expected: 2},
		{name: "server only", script: `exit 0`, expected: 0},
		{name: "unreachable", script: `echo "connection refused" >&2; exit 1`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubKubectl(t, tt.script)
			got, err := AgentCount(t.Context())
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.expected {
				t.Errorf("AgentCount = %d, want %d", got, tt.expected)
			}
		})
	}
}

func TestRequireReachable(t *testing.T) {
	tests := []struct {
		name       string
		kubectl    string
		wantReason string // "" means reachable
	}{
		{"the API server answers", "echo ok\n", ""},
		{"the API server refuses the connection",
			"echo 'The connection to the server 127.0.0.1:6443 was refused - did you specify the right host or port?' >&2\nexit 1\n",
			"The connection to the server 127.0.0.1:6443 was refused"},
		{"kubectl fails without saying why", "exit 1\n", "exit status 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubKubectl(t, tt.kubectl)
			err := RequireReachable(t.Context())
			if tt.wantReason == "" {
				if err != nil {
					t.Fatalf("RequireReachable() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, ErrUnreachable) {
				t.Fatalf("RequireReachable() = %v, want an error matching ErrUnreachable", err)
			}
			if !strings.Contains(err.Error(), tt.wantReason) {
				t.Errorf("RequireReachable() = %q, want it to carry kubectl's reason %q", err, tt.wantReason)
			}
		})
	}
}

// A request that is already cancelled is not reported as a healthy cluster.
func TestRequireReachable_CancelledContext(t *testing.T) {
	stubKubectl(t, "echo ok\n")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := RequireReachable(ctx); !errors.Is(err, ErrUnreachable) {
		t.Errorf("RequireReachable(cancelled) = %v, want an error matching ErrUnreachable", err)
	}
}

const twoPods = `{"items":[
 {"metadata":{"name":"api-1"},"status":{"phase":"Running","containerStatuses":[{"ready":true,"restartCount":0},{"ready":true,"restartCount":2}]}},
 {"metadata":{"name":"api-2"},"status":{"phase":"Running","containerStatuses":[{"ready":false,"restartCount":5}]}},
 {"metadata":{"name":"job-1"},"status":{"phase":"Succeeded"}},
 {"metadata":{"name":"old-1"},"status":{"phase":"Failed"}}
]}`

func TestGetNamespacePods(t *testing.T) {
	tests := []struct {
		name    string
		kubectl string
		want    []PodInfo
		wantErr string
	}{
		{"pods are summarised by readiness and restarts", "cat <<'JSON'\n" + twoPods + "\nJSON\n", []PodInfo{
			{Name: "api-1", Namespace: "shop", Status: "Running", Ready: "2/2", Restarts: "2"},
			{Name: "api-2", Namespace: "shop", Status: "Running", Ready: "0/1", Restarts: "5"},
			{Name: "job-1", Namespace: "shop", Status: "Succeeded", Ready: "0/0", Restarts: "0"},
			{Name: "old-1", Namespace: "shop", Status: "Failed", Ready: "0/0", Restarts: "0"},
		}, ""},
		{"an empty namespace has no pods", `echo '{"items":[]}'` + "\n", nil, ""},
		{"kubectl fails", "echo 'namespaces \"shop\" not found' >&2\nexit 1\n", nil, "not found"},
		{"kubectl prints something that is not JSON", "echo 'Unable to connect'\n", nil, "parsing pod list JSON"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubKubectl(t, tt.kubectl)
			got, err := GetNamespacePods(t.Context(), "shop")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("GetNamespacePods() error = %v, want one mentioning %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("GetNamespacePods() error = %v", err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("GetNamespacePods() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// namespaceStub answers for one namespace, "shop": it exists, has one
// Deployment, and the pods given.
func namespaceStub(deployments, pods string) string {
	return `case "$*" in
  "get namespace shop --no-headers") echo "shop Active 1h" ;;
  "get namespace "*) exit 1 ;;
  "get deployment -n shop"*) printf '%s' "` + deployments + `" ;;
  "get pods -n shop -o json") cat <<'JSON'
` + pods + `
JSON
  ;;
  *) exit 1 ;;
esac
`
}

func TestGetAppStatus(t *testing.T) {
	tests := []struct {
		name      string
		kubectl   string
		namespace string
		want      AppStatus
		wantPods  int
	}{
		{"a deployed app reports its replicas", namespaceStub("api 3 2 2", twoPods), "shop",
			AppStatus{Name: "api", Namespace: "shop", Deployed: true, Replicas: "3", Ready: "2", Available: "2"}, 4},
		{"a Deployment still starting has no ready count", namespaceStub("api 3", `{"items":[]}`), "shop",
			AppStatus{Name: "api", Namespace: "shop", Deployed: true, Replicas: "3"}, 0},
		{"a namespace with no Deployment is not deployed", namespaceStub("", `{"items":[]}`), "shop",
			AppStatus{Name: "api", Namespace: "shop"}, 0},
		{"a missing namespace is not deployed", namespaceStub("api 3 2 2", twoPods), "ghost",
			AppStatus{Name: "api", Namespace: "ghost"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubKubectl(t, tt.kubectl)
			got, err := GetAppStatus(t.Context(), "api", tt.namespace)
			if err != nil {
				t.Fatalf("GetAppStatus() error = %v", err)
			}
			if len(got.Pods) != tt.wantPods {
				t.Errorf("pods = %d, want %d", len(got.Pods), tt.wantPods)
			}
			got.Pods = nil
			if !reflect.DeepEqual(*got, tt.want) {
				t.Errorf("GetAppStatus() = %+v, want %+v", *got, tt.want)
			}
		})
	}
}

func TestNamespaceHealth(t *testing.T) {
	tests := []struct {
		name       string
		kubectl    string
		namespace  string
		wantReady  int
		wantTotal  int
		wantExists bool
	}{
		{"failed pods are left out and a finished job counts as ready", namespaceStub("", twoPods), "shop", 2, 3, true},
		{"an installed namespace with no pods", namespaceStub("", `{"items":[]}`), "shop", 0, 0, true},
		{"a namespace whose pods cannot be listed still exists", namespaceStub("", "not json"), "shop", 0, 0, true},
		{"a namespace that is not there", namespaceStub("", twoPods), "ghost", 0, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubKubectl(t, tt.kubectl)
			ready, total, exists := NamespaceHealth(t.Context(), tt.namespace)
			if ready != tt.wantReady || total != tt.wantTotal || exists != tt.wantExists {
				t.Errorf("NamespaceHealth() = %d ready of %d, exists %v; want %d of %d, exists %v",
					ready, total, exists, tt.wantReady, tt.wantTotal, tt.wantExists)
			}
		})
	}
}

// Each existence probe is true only when kubectl finds the object, and false
// for a missing object, an unreachable cluster and an empty name alike.
func TestExistenceProbes(t *testing.T) {
	found := `case "$*" in
  "get namespace shop --no-headers") echo "shop Active" ;;
  "get service api -n shop --no-headers") echo "api ClusterIP" ;;
  "get secret -n shop -l owner=helm,name=api --no-headers") echo "sh.helm.release.v1.api.v1" ;;
  "get secret -n shop -l owner=helm,name=api,status=deployed --no-headers") echo "sh.helm.release.v1.api.v1" ;;
  "get secret -n shop -l owner=helm,name=half"*) ;;
  *) exit 1 ;;
esac
`
	probes := map[string]func(ns, name string) bool{
		"NamespaceExists":     func(ns, _ string) bool { return NamespaceExists(t.Context(), ns) },
		"ServiceExists":       func(ns, name string) bool { return ServiceExists(t.Context(), ns, name) },
		"HelmReleaseExists":   func(ns, name string) bool { return HelmReleaseExists(t.Context(), ns, name) },
		"HelmReleaseDeployed": func(ns, name string) bool { return HelmReleaseDeployed(t.Context(), ns, name) },
	}
	tests := []struct {
		name     string
		ns, obj  string
		want     bool
		onlyHelm bool
	}{
		{"the object is there", "shop", "api", true, false},
		{"another namespace", "ghost", "api", false, false},
		{"a release with no secrets listed", "shop", "half", false, true},
		{"an empty release name", "shop", "", false, true},
		{"an empty namespace", "", "api", false, true},
	}
	for probe, call := range probes {
		for _, tt := range tests {
			if tt.onlyHelm && !strings.HasPrefix(probe, "Helm") {
				continue
			}
			t.Run(probe+"/"+tt.name, func(t *testing.T) {
				stubKubectl(t, found)
				if got := call(tt.ns, tt.obj); got != tt.want {
					t.Errorf("%s(%q, %q) = %v, want %v", probe, tt.ns, tt.obj, got, tt.want)
				}
			})
		}
		t.Run(probe+"/the cluster does not answer", func(t *testing.T) {
			stubKubectl(t, "echo 'connection refused' >&2\nexit 1\n")
			if call("shop", "api") {
				t.Errorf("%s reported the object present on a cluster that did not answer", probe)
			}
		})
	}
}

func TestGetHPAStatus_NoHPAIsNotAnError(t *testing.T) {
	tests := []struct {
		name    string
		kubectl string
	}{
		{"kubectl fails", "exit 1\n"},
		{"the namespace has no HPA", `echo '{"items":[]}'` + "\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubKubectl(t, tt.kubectl)
			got, err := GetHPAStatus(t.Context(), "shop", "api")
			if err != nil || got == nil || got.Present {
				t.Errorf("GetHPAStatus() = %+v, %v; want Present=false and no error", got, err)
			}
		})
	}
}

func TestIngressHosts(t *testing.T) {
	tests := []struct {
		name    string
		kubectl string
		want    []string
		wantErr bool
	}{
		{"hosts are listed once, in order, without blanks", "printf 'grafana.lab.test\\n\\napi.lab.test\\ngrafana.lab.test\\n'\n", []string{"grafana.lab.test", "api.lab.test"}, false},
		{"a cluster with no Ingress", "true\n", []string{}, false},
		{"the cluster does not answer", "exit 1\n", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubKubectl(t, tt.kubectl)
			got, err := IngressHosts(t.Context())
			if (err != nil) != tt.wantErr {
				t.Fatalf("IngressHosts() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("IngressHosts() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGetCurrentContext(t *testing.T) {
	stubKubectl(t, "echo k3d-lab\n")
	if got, err := GetCurrentContext(t.Context()); err != nil || got != "k3d-lab" {
		t.Errorf("GetCurrentContext() = %q, %v", got, err)
	}
}

// A failed kubectl call carries what kubectl printed, and still matches the
// exit error underneath.
func TestRunKubectl_Failures(t *testing.T) {
	t.Run("the error carries kubectl's stderr", func(t *testing.T) {
		stubKubectl(t, "echo 'error: the server does not have a resource type \"widgets\"' >&2\nexit 1\n")
		_, err := RunKubectl(t.Context(), "get", "widgets")
		var exit *exec.ExitError
		if err == nil || !strings.Contains(err.Error(), `resource type "widgets"`) || !errors.As(err, &exit) {
			t.Errorf("RunKubectl() error = %v, want kubectl's message wrapping the exit error", err)
		}
	})
	t.Run("a silent failure is the exit status", func(t *testing.T) {
		stubKubectl(t, "exit 3\n")
		if _, err := RunKubectl(t.Context(), "get", "pods"); err == nil || !strings.Contains(err.Error(), "exit status 3") {
			t.Errorf("RunKubectl() error = %v, want exit status 3", err)
		}
	})
	t.Run("kubectl is not installed", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		if _, err := RunKubectl(t.Context(), "get", "pods"); err == nil || !strings.Contains(err.Error(), "kubectl not found in PATH") {
			t.Errorf("RunKubectl() error = %v, want kubectl not found", err)
		}
	})
	t.Run("a cancelled context stops the call", func(t *testing.T) {
		stubKubectl(t, "sleep 30\n")
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := RunKubectl(ctx, "get", "pods"); err == nil {
			t.Error("RunKubectl() on a cancelled context returned no error")
		}
	})
	t.Run("a deadline stops a kubectl that hangs", func(t *testing.T) {
		stubKubectl(t, "exec sleep 30\n")
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		start := time.Now()
		if _, err := RunKubectl(ctx, "get", "pods"); err == nil {
			t.Error("RunKubectl() past its deadline returned no error")
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Errorf("RunKubectl() took %s to give up on a 50ms deadline", elapsed)
		}
	})
}
