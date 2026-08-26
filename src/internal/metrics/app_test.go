// SPDX-License-Identifier: Apache-2.0

package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestApp_BuildInfoAlwaysPresent(t *testing.T) {
	a := NewApp("v1.2.3")
	got := a.Render()
	if !strings.Contains(got, `labctl_build_info{version="v1.2.3"} 1`) {
		t.Errorf("build_info should be present with the version:\n%s", got)
	}
	// The metric families are pre-registered, so their TYPE lines appear even
	// before any observation — a scraper sees the full schema.
	for _, want := range []string{
		"# TYPE labctl_http_requests_total counter",
		"# TYPE labctl_http_request_duration_seconds histogram",
		"# TYPE labctl_runs_total counter",
		"# TYPE labctl_run_duration_seconds histogram",
		"# TYPE labctl_runs_in_flight gauge",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing family header %q:\n%s", want, got)
		}
	}
}

func TestApp_EmptyVersion(t *testing.T) {
	if got := NewApp("").Render(); !strings.Contains(got, `version="unknown"`) {
		t.Errorf("empty version should render as unknown:\n%s", got)
	}
}

func TestApp_HTTPObserve(t *testing.T) {
	a := NewApp("test")
	a.HTTPObserve("GET", "/apps/{name}", 200, 12*time.Millisecond)
	a.HTTPObserve("GET", "/apps/{name}", 200, 8*time.Millisecond)
	a.HTTPObserve("POST", "/apps/{name}/build", 500, 3*time.Second)

	got := a.Render()
	for _, want := range []string{
		`labctl_http_requests_total{method="GET",route="/apps/{name}",status="200"} 2`,
		`labctl_http_requests_total{method="POST",route="/apps/{name}/build",status="500"} 1`,
		`labctl_http_request_duration_seconds_count{method="GET",route="/apps/{name}"} 2`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
}

func TestApp_RunMetrics(t *testing.T) {
	a := NewApp("test")
	// Two runs start, one finishes.
	a.RunStarted()
	a.RunStarted()
	a.RunFinished("scenario.activate", "succeeded", 5*time.Second)

	got := a.Render()
	if !strings.Contains(got, "labctl_runs_in_flight 1\n") {
		t.Errorf("in-flight should be 1 (2 started, 1 finished):\n%s", got)
	}
	if !strings.Contains(got, `labctl_runs_total{kind="scenario.activate",status="succeeded"} 1`) {
		t.Errorf("runs_total not recorded:\n%s", got)
	}
	if !strings.Contains(got, `labctl_run_duration_seconds_count{kind="scenario.activate"} 1`) {
		t.Errorf("run duration not observed:\n%s", got)
	}
}

func TestApp_HandlerServesExposition(t *testing.T) {
	a := NewApp("test")
	a.HTTPObserve("GET", "/status", 200, time.Millisecond)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("content-type = %q, want text/plain exposition", ct)
	}
	if !strings.Contains(rec.Body.String(), "labctl_http_requests_total") {
		t.Errorf("body should contain metrics:\n%s", rec.Body.String())
	}
}

func TestApp_HandlerRejectsNonGET(t *testing.T) {
	a := NewApp("test")
	req := httptest.NewRequest(http.MethodPost, "/metrics", nil)
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /metrics = %d, want 405", rec.Code)
	}
}

// The App must satisfy the run engine's Metrics interface without importing it;
// this is a compile-time check that the method set matches.
var _ interface {
	RunStarted()
	RunFinished(kind, status string, dur time.Duration)
} = (*App)(nil)
