// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/snowops-oss/snowopslabs/internal/labcheck"
	"github.com/snowops-oss/snowopslabs/internal/platform"
)

// datasourceServer answers Grafana's Prometheus datasource health check. It
// refuses any password but "admin", and reports healthy once the marker file
// exists, which a fake install.sh creates to stand for a repair.
func datasourceServer(t *testing.T, marker string, healthy bool) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, pass, _ := r.BasicAuth(); user != "admin" || pass != "admin" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/api/datasources/uid/prometheus/health" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if _, err := os.Stat(marker); healthy || err == nil {
			_, _ = w.Write([]byte(`{"status":"OK","message":"Successfully queried the Prometheus API."}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"messageId":"plugin.notRegistered","message":"Plugin not registered"}`))
	}))
	t.Cleanup(srv.Close)
	_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func TestEnsureGrafanaQueriesPrometheus(t *testing.T) {
	tests := []struct {
		name       string
		profile    string
		healthy    bool
		password   string
		install    string
		wantErr    []string
		wantOutput string
	}{
		{name: "a healthy datasource passes", profile: "k3d", healthy: true},
		{name: "incluster is not checked", profile: "incluster"},
		{name: "a changed password is reported, not failed", profile: "k3d", password: "changed",
			wantOutput: "GRAFANA_ADMIN_PASSWORD"},
		{name: "a broken datasource is repaired by re-applying grafana", profile: "k3d",
			install: `touch "$MARKER"`, wantOutput: "Plugin not registered"},
		{name: "a failed re-apply names the command to run", profile: "k3d", install: "exit 1",
			wantErr: []string{"re-applying Grafana failed", "labctl platform up monitoring/grafana"}},
		{name: "a datasource still broken after the re-apply gives the reason", profile: "k3d", install: "exit 0",
			wantErr: []string{"still cannot query Prometheus", "Plugin not registered", "docs/troubleshooting.md"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "repaired")
			withLab(t, tt.profile, datasourceServer(t, marker, tt.healthy))
			t.Setenv("MARKER", marker)
			t.Setenv("GRAFANA_ADMIN_PASSWORD", tt.password)
			oldReg, oldWait := reg, datasourceWait
			t.Cleanup(func() { reg, datasourceWait = oldReg, oldWait })
			datasourceWait = 0
			dir := filepath.Join(cfg.ProjectRoot, "platform", "monitoring", "grafana")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			//nolint:gosec // the test script must be executable
			if err := os.WriteFile(filepath.Join(dir, "install.sh"), []byte("#!/bin/sh\n"+tt.install+"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			reg = platform.NewRegistry(cfg.ProjectRoot)

			var out bytes.Buffer
			err := ensureGrafanaQueriesPrometheus(t.Context(), &out)
			if len(tt.wantErr) == 0 && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for _, want := range tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Errorf("error should mention %q, got %v", want, err)
				}
			}
			if !strings.Contains(out.String(), tt.wantOutput) {
				t.Errorf("output should mention %q:\n%s", tt.wantOutput, out.String())
			}
		})
	}
}

func TestWaitDatasourceContext(t *testing.T) {
	tests := []struct {
		name string
		ctx  func(t *testing.T) context.Context
		want error
	}{
		{name: "cancelled", ctx: func(t *testing.T) context.Context {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			return ctx
		}, want: context.Canceled},
		{name: "deadline exceeded", ctx: func(t *testing.T) context.Context {
			ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
			t.Cleanup(cancel)
			return ctx
		}, want: context.DeadlineExceeded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withLab(t, "k3d", datasourceServer(t, filepath.Join(t.TempDir(), "never"), false))
			start := time.Now()
			err := waitDatasource(tt.ctx(t), labcheck.HTTPClient(cfg), time.Minute)
			if !errors.Is(err, tt.want) {
				t.Errorf("waitDatasource = %v, want %v", err, tt.want)
			}
			if time.Since(start) > 5*time.Second {
				t.Errorf("took %s; a done context must stop the wait", time.Since(start))
			}
		})
	}
}
