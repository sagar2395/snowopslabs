// SPDX-License-Identifier: Apache-2.0

package metrics

import (
	"net/http"
	"strconv"
	"time"
)

// App is labctl's concrete metric set, built on a Registry. One App is shared
// by the HTTP server (which records request metrics and serves the endpoint)
// and the run engine (which records run metrics through the run.Metrics
// interface this type satisfies).
//
// It deliberately knows nothing about net/http routing or the run engine's
// types — callers pass plain strings — so neither of those packages has to
// import a metrics library, and this package stays a leaf.
type App struct {
	reg *Registry

	httpRequests *CounterVec   // labels: method, route, status
	httpDuration *HistogramVec // labels: method, route

	runsTotal    *CounterVec   // labels: kind, status
	runDuration  *HistogramVec // labels: kind
	runsInFlight *GaugeVec     // no labels
}

// NewApp builds the metric set and records the build version as a constant
// info gauge, so /metrics is never empty even before any request or run.
func NewApp(version string) *App {
	r := NewRegistry()
	a := &App{
		reg: r,
		httpRequests: r.NewCounterVec(
			"labctl_http_requests_total",
			"Total HTTP requests handled by the labctl API, by method, route template and status code.",
			"method", "route", "status"),
		httpDuration: r.NewHistogramVec(
			"labctl_http_request_duration_seconds",
			"HTTP request handling latency in seconds, by method and route template.",
			DefaultHTTPBuckets, "method", "route"),
		runsTotal: r.NewCounterVec(
			"labctl_runs_total",
			"Total run-engine runs that reached a terminal state, by kind and outcome.",
			"kind", "status"),
		runDuration: r.NewHistogramVec(
			"labctl_run_duration_seconds",
			"Run-engine run execution time in seconds, by kind.",
			DefaultRunBuckets, "kind"),
		runsInFlight: r.NewGaugeVec(
			"labctl_runs_in_flight",
			"Run-engine runs currently executing."),
	}

	buildInfo := r.NewGaugeVec(
		"labctl_build_info",
		"Build information; the value is always 1, the version is in the label.",
		"version")
	if version == "" {
		version = "unknown"
	}
	buildInfo.Set(1, version)

	return a
}

// Registry exposes the underlying registry (used in tests).
func (a *App) Registry() *Registry { return a.reg }

// Render returns the current metrics in Prometheus text exposition format.
func (a *App) Render() string { return a.reg.Render() }

// HTTPObserve records one handled HTTP request.
func (a *App) HTTPObserve(method, route string, status int, dur time.Duration) {
	code := strconv.Itoa(status)
	a.httpRequests.Inc(method, route, code)
	a.httpDuration.Observe(dur.Seconds(), method, route)
}

// RunStarted and RunFinished implement the run.Metrics interface, so an *App
// can be passed straight to run.WithMetrics.
func (a *App) RunStarted() { a.runsInFlight.Inc() }

// RunFinished records a run that reached a terminal state.
func (a *App) RunFinished(kind, status string, dur time.Duration) {
	a.runsInFlight.Dec()
	a.runsTotal.Inc(kind, status)
	a.runDuration.Observe(dur.Seconds(), kind)
}

// Handler serves the exposition format. It answers GET (and HEAD) only.
func (a *App) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = w.Write([]byte(a.reg.Render()))
	})
}
