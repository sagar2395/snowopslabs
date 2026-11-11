// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/snowops-oss/snowopslabs/internal/config"
	"github.com/snowops-oss/snowopslabs/internal/executor"
	"github.com/snowops-oss/snowopslabs/internal/labcheck"
)

// grafanaServer answers /api/health the way the lab's Grafana does, or with
// status and body when they are set.
func grafanaServer(t *testing.T, status int, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return port
}

// withLab points the package config at a lab with the given profile whose
// ingress listens on port. HTTP_PORT names the same port and labctl's state
// lives in a temp SNOWOPS_HOME, so reloadIngress never falls back to
// port 80, where a real lab on this machine would answer.
func withLab(t *testing.T, profile, port string) {
	t.Helper()
	t.Setenv("SNOWOPS_HOME", t.TempDir())
	t.Setenv("HTTP_PORT", port)
	oldCfg, oldExec, oldWait := cfg, scriptExec, ingressWait
	t.Cleanup(func() { cfg, scriptExec, ingressWait = oldCfg, oldExec, oldWait })
	root := t.TempDir()
	cfg = &config.Config{ProjectRoot: root, Profile: profile, ClusterName: "lab", DomainSuffix: "lab.localhost", HTTPPort: port}
	scriptExec = executor.New(root)
	ingressWait = 0
}

func TestIngressAnswers(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		expected bool
	}{
		{name: "the lab's grafana", status: http.StatusOK, body: `{"database":"ok","version":"12"}`, expected: true},
		{name: "another service on the port", status: http.StatusNotFound, body: "404 page not found"},
		{name: "a healthy answer that is not grafana", status: http.StatusOK, body: "ok"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withLab(t, "k3d", grafanaServer(t, tt.status, tt.body))
			if got := ingressAnswers(t.Context(), labcheck.HTTPClient(cfg)); got != tt.expected {
				t.Errorf("ingressAnswers = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestEnsureIngressReachable(t *testing.T) {
	tests := []struct {
		name       string
		profile    string
		answers    bool
		moveScript string
		wantErr    string
		wantOutput string
	}{
		{name: "the lab answers", profile: "k3d", answers: true},
		{name: "incluster is not checked", profile: "incluster"},
		{name: "kind says how to free or change the port", profile: "kind", wantErr: "labctl reset"},
		{name: "k3d moves the ingress when the move fails loudly", profile: "k3d", moveScript: "exit 1\n",
			wantErr: "moving the lab's ingress to free ports failed", wantOutput: "Moving the lab's ingress"},
		{name: "k3d reports a port that still does not answer", profile: "k3d", moveScript: "exit 0\n",
			wantErr: "still does not reach the lab"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := http.StatusNotFound
			if tt.answers {
				status = http.StatusOK
			}
			withLab(t, tt.profile, grafanaServer(t, status, `{"database":"ok"}`))
			if tt.moveScript != "" {
				dir := filepath.Join(cfg.ProjectRoot, "runtimes", "k3d")
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				//nolint:gosec // the test script must be executable
				if err := os.WriteFile(filepath.Join(dir, "move-ingress.sh"), []byte("#!/bin/sh\n"+tt.moveScript), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			var out bytes.Buffer
			start := time.Now()
			err := ensureIngressReachable(t.Context(), &out)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			}
			if !strings.Contains(out.String(), tt.wantOutput) {
				t.Errorf("output should mention %q:\n%s", tt.wantOutput, out.String())
			}
			if time.Since(start) > 30*time.Second {
				t.Errorf("took %s; the test wait is zero", time.Since(start))
			}
		})
	}
}
