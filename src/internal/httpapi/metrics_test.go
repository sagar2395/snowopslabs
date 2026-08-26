// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	"github.com/sagar2395/snowopslabs/internal/config"
	"github.com/sagar2395/snowopslabs/internal/metrics"
)

func newMetricsServer(t *testing.T, opts ...ServerOption) *Server {
	t.Helper()
	cfg := &config.Config{ProjectRoot: t.TempDir()}
	return NewServer(cfg, nil, nil, nil, nil, nil, nil, nil, opts...)
}

func TestMetricsEndpoint_EnabledServesExposition(t *testing.T) {
	app := metrics.NewApp("v9.9.9")
	s := newMetricsServer(t, WithMetrics(app))

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /metrics = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("content-type = %q, want prometheus text exposition", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"# TYPE labctl_http_requests_total counter",
		"# TYPE labctl_runs_total counter",
		`labctl_build_info{version="v9.9.9"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("exposition missing %q:\n%s", want, body)
		}
	}
}

func TestMetricsEndpoint_DisabledByDefault(t *testing.T) {
	s := newMetricsServer(t) // no WithMetrics

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)

	// With metrics off, /metrics is not registered; the SPA catch-all handles
	// it. The one thing that must never happen is a 200 exposition response.
	if rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), "labctl_") {
		t.Errorf("/metrics must not serve metrics when disabled:\n%s", rec.Body.String())
	}
}

func TestMetricsEndpoint_RejectsPost(t *testing.T) {
	s := newMetricsServer(t, WithMetrics(metrics.NewApp("t")))
	req := httptest.NewRequest(http.MethodPost, "/metrics", nil)
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)
	// mux returns 405 for a route that exists but not for this method.
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /metrics = %d, want 405", rec.Code)
	}
}

func TestMetricsMiddleware_RecordsRouteTemplate(t *testing.T) {
	app := metrics.NewApp("test")
	s := &Server{metrics: app}

	r := mux.NewRouter()
	api := r.PathPrefix("/api").Subrouter()
	api.Use(s.metricsMiddleware)
	api.HandleFunc("/apps/{name}/build", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}).Methods(http.MethodPost)

	req := httptest.NewRequest(http.MethodPost, "/api/apps/go-api/build", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("handler status = %d, want 202", rec.Code)
	}

	out := app.Render()
	// The concrete name "go-api" must be abstracted to the {name} template so
	// label cardinality stays bounded, and the status code is recorded.
	want := `labctl_http_requests_total{method="POST",route="/api/apps/{name}/build",status="202"} 1`
	if !strings.Contains(out, want) {
		t.Errorf("expected %q in:\n%s", want, out)
	}
	if strings.Contains(out, "go-api") {
		t.Errorf("concrete path value leaked into a label (unbounded cardinality):\n%s", out)
	}
}

func TestMetricsMiddleware_ImplicitStatusAndWebsocketSkip(t *testing.T) {
	app := metrics.NewApp("test")
	s := &Server{metrics: app}

	r := mux.NewRouter()
	api := r.PathPrefix("/api").Subrouter()
	api.Use(s.metricsMiddleware)
	// Handler writes a body without calling WriteHeader -> implicit 200.
	api.HandleFunc("/ok", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hi"))
	})
	// A websocket upgrade must pass through unmeasured.
	api.HandleFunc("/ws", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusSwitchingProtocols)
	})

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/ok", nil))
	wsReq := httptest.NewRequest(http.MethodGet, "/api/ws", nil)
	wsReq.Header.Set("Upgrade", "websocket")
	r.ServeHTTP(httptest.NewRecorder(), wsReq)

	out := app.Render()
	if !strings.Contains(out, `route="/api/ok",status="200"`) {
		t.Errorf("implicit 200 not recorded:\n%s", out)
	}
	if strings.Contains(out, "/api/ws") {
		t.Errorf("websocket request should be skipped, not measured:\n%s", out)
	}
}

func TestStatusRecorder_Passthrough(t *testing.T) {
	// Flush forwards to a Flusher; httptest.ResponseRecorder is one.
	base := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: base, status: http.StatusOK}
	rec.Flush()
	if !base.Flushed {
		t.Error("Flush did not forward to the underlying Flusher")
	}
	// Hijack returns an error when the underlying writer is not a Hijacker
	// (httptest.ResponseRecorder is not), rather than panicking.
	if _, _, err := rec.Hijack(); err == nil {
		t.Error("Hijack should error when the writer does not support hijacking")
	}
}
