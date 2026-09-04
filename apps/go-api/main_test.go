package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func TestRouteLabel(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{"root is a known route", "/", "/"},
		{"health is a known route", "/health", "/health"},
		{"unknown path is bucketed", "/does-not-exist", "other"},
		{"attacker-controlled path cannot create a series", "/../../etc/passwd", "other"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := routeLabel(tt.path); got != tt.want {
				t.Errorf("routeLabel(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

// Every served route must be metered. "/" was previously unmetered, so k6
// traffic against the app root produced logs but a flat request-rate graph.
func TestInstrument_MetersEveryRoute(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		status    int
		wantRoute string
		wantCount int
	}{
		{"root", "/", http.StatusOK, "/", 1},
		{"version", "/version", http.StatusOK, "/version", 1},
		{"error status is labelled", "/ready", http.StatusServiceUnavailable, "/ready", 1},
		{"metrics endpoint is not self-metered", "/metrics", http.StatusOK, "/metrics", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			httpRequestsTotal.Reset()
			h := instrument(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
			}))
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, tt.path, nil))

			// Assert on the actual exposition Prometheus scrapes.
			want := `http_requests_total{app="` + serviceName + `",code="` + strconv.Itoa(tt.status) +
				`",method="GET",path="` + tt.wantRoute + `"} ` + strconv.Itoa(tt.wantCount)
			body := scrapeMetrics(t)
			if tt.wantCount == 0 {
				if strings.Contains(body, `path="`+tt.wantRoute+`"`) {
					t.Errorf("expected no series for path=%q, got:\n%s", tt.wantRoute, body)
				}
				return
			}
			if !strings.Contains(body, want) {
				t.Errorf("missing %q in /metrics output:\n%s", want, body)
			}
		})
	}
}

// Turning tracing on used to replace the access-log middleware, which silently
// emptied the Loki log stream. The chain must keep logging either way.
func TestAccessLog_LogsRequestsAndSkipsProbes(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantLog bool
	}{
		{"root is logged", "/", true},
		{"health probe is not logged", "/health", false},
		{"metrics scrape is not logged", "/metrics", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger = slog.New(slog.NewJSONHandler(&buf, nil))

			h := accessLog(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, tt.path, nil))

			logged := strings.Contains(buf.String(), `"msg":"request"`)
			if logged != tt.wantLog {
				t.Fatalf("access log for %q: got logged=%v, want %v (output: %s)", tt.path, logged, tt.wantLog, buf.String())
			}
			if tt.wantLog {
				var line map[string]any
				if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
					t.Fatalf("log line is not valid JSON (Loki parses it as JSON): %v", err)
				}
				if line["path"] != tt.path {
					t.Errorf("log path = %v, want %q", line["path"], tt.path)
				}
			}
		})
	}
}

// scrapeMetrics returns the /metrics exposition, the same text Prometheus reads.
func scrapeMetrics(t *testing.T) string {
	t.Helper()
	rec := httptest.NewRecorder()
	promhttp.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return rec.Body.String()
}
