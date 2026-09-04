package main

import (
	"context"
	"encoding/json"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
)

// build-time version info injected via -ldflags
var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

var releaseNote = "New release"

var (
	port            = getEnv("PORT", "8080")
	shutdownTimeout = getDurationEnv("SHUTDOWN_TIMEOUT", 30*time.Second)
	// OTEL_SERVICE_NAME is the OpenTelemetry-standard name; SERVICE_NAME is the
	// pre-existing knob. Honour both so the same value labels metrics, logs and
	// traces — otherwise the three signals cannot be correlated in Grafana.
	serviceName  = getEnv("OTEL_SERVICE_NAME", getEnv("SERVICE_NAME", "go-api"))
	otelEndpoint = getEnv("OTEL_EXPORTER_OTLP_ENDPOINT", "")

	simulateFailure atomic.Bool
	logger          *slog.Logger
	tracer          trace.Tracer

	httpRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total number of HTTP requests",
	}, []string{"method", "path", "code", "app"})

	httpRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request duration in seconds",
		Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1},
	}, []string{"method", "path", "app"})
)

func init() {
	prometheus.MustRegister(httpRequestsTotal)
	prometheus.MustRegister(httpRequestDuration)
}

func main() {
	readinessFailure := getEnv("READINESS_FAILURE", "false") == "true"
	flag.BoolFunc("failure", "Simulate readiness check failure", func(s string) error {
		v, err := strconv.ParseBool(s)
		if err != nil {
			return err
		}
		simulateFailure.Store(v)
		return nil
	})
	flag.Parse()

	simulateFailure.Store(readinessFailure)

	// Structured logger
	logLevel := slog.LevelInfo
	if getEnv("LOG_LEVEL", "info") == "debug" {
		logLevel = slog.LevelDebug
	}
	logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(logger)

	// OpenTelemetry tracing (optional - only if endpoint configured)
	var tp *sdktrace.TracerProvider
	if otelEndpoint != "" {
		var err error
		tp, err = initTracer()
		if err != nil {
			logger.Warn("failed to init tracer, continuing without tracing", "error", err)
		} else {
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				tp.Shutdown(ctx)
			}()
		}
	}
	tracer = otel.Tracer(serviceName)

	// Routes
	mux := http.NewServeMux()
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/ready", handleReady)
	mux.HandleFunc("/version", handleVersion)
	mux.HandleFunc("/toggle-failure", handleToggleFailure)
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/", handleRoot)

	// Middleware order matters: otelhttp is outermost so the access log and the
	// metrics middleware both run inside a live span (the log line can carry the
	// trace ID). Previously otelhttp replaced the access-log wrapper entirely,
	// so turning tracing on silently deleted every request log.
	var handler http.Handler = accessLog(mux)
	handler = instrument(handler)
	if otelEndpoint != "" {
		handler = otelhttp.NewHandler(handler, "http",
			otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
				return r.Method + " " + routeLabel(r.URL.Path)
			}),
			// Don't trace scrapes and probes. They run on a fixed interval
			// forever, so tracing them buries real request traces in noise.
			otelhttp.WithFilter(func(r *http.Request) bool {
				return !isInfraPath(r.URL.Path)
			}),
		)
	}

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		logger.Info("server starting", "port", port, "service", serviceName, "simulateFailure", simulateFailure.Load())
		if otelEndpoint != "" {
			logger.Info("tracing enabled", "endpoint", otelEndpoint)
		}
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	sig := <-sigChan
	logger.Info("received signal, shutting down", "signal", sig)

	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.Error("shutdown error", "error", err)
		os.Exit(1)
	}

	logger.Info("server stopped")
}

func initTracer() (*sdktrace.TracerProvider, error) {
	ctx := context.Background()

	exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithInsecure())
	if err != nil {
		return nil, err
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(version),
		),
	)
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return tp, nil
}

func handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	_, span := tracer.Start(r.Context(), "build-service-index")
	defer span.End()

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"service":         serviceName,
		"version":         version,
		"releaseNote":     releaseNote,
		"env":             getEnv("ENV_NAME", "unknown"),
		"simulateFailure": simulateFailure.Load(),
		"endpoints": []string{
			"/health          - Health check (always 200)",
			"/ready           - Readiness check (503 when failure simulated)",
			"/toggle-failure  - Toggle readiness failure simulation",
			"/version         - Build version info",
			"/metrics         - Prometheus metrics",
		},
	})
}

// statusRecorder captures the response status so the access log can report it.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// isInfraPath reports whether a path is kubelet/Prometheus traffic rather than a
// real request: scraped or probed on a timer, and noise in logs and traces.
func isInfraPath(path string) bool {
	switch path {
	case "/health", "/ready", "/metrics":
		return true
	}
	return false
}

// accessLog logs one structured line per non-probe request at Info level, so
// load from the traffic generator (or any client) shows up in `kubectl logs`.
func accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isInfraPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		attrs := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"durationMs", time.Since(start).Milliseconds(),
			"remote", r.RemoteAddr,
		}
		// Grafana's Loki datasource turns these into a link straight to the trace.
		if sc := trace.SpanContextFromContext(r.Context()); sc.IsValid() {
			attrs = append(attrs, "trace_id", sc.TraceID().String(), "span_id", sc.SpanID().String())
		}
		logger.Info("request", attrs...)
	})
}

func handleVersion(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]string{
		"app":         serviceName,
		"version":     version,
		"commit":      commit,
		"buildDate":   buildDate,
		"releaseNote": releaseNote,
		"env":         getEnv("ENV_NAME", "unknown"),
	})
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	_, span := tracer.Start(r.Context(), "health-check")
	defer span.End()

	logger.Debug("health check", "remote", r.RemoteAddr)
	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func handleReady(w http.ResponseWriter, r *http.Request) {
	_, span := tracer.Start(r.Context(), "readiness-check")
	defer span.End()

	if simulateFailure.Load() {
		span.SetAttributes(attribute.Bool("ready", false))
		logger.Warn("readiness check failed (simulated)", "remote", r.RemoteAddr)
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status": "not_ready",
			"reason": "simulated failure",
		})
		return
	}

	span.SetAttributes(attribute.Bool("ready", true))
	logger.Debug("readiness check passed", "remote", r.RemoteAddr)
	respondJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func handleToggleFailure(w http.ResponseWriter, r *http.Request) {
	prev := simulateFailure.Load()
	simulateFailure.Store(!prev)
	current := simulateFailure.Load()

	logger.Info("failure simulation toggled", "previous", prev, "current", current)
	respondJSON(w, http.StatusOK, map[string]string{
		"simulateFailure": strconv.FormatBool(current),
		"message":         "Readiness failure simulation toggled. Hit /ready to test.",
	})
}

// knownRoutes bounds the "path" label. An unbounded label (raw r.URL.Path) lets
// any client create a new time series per URL and blow up Prometheus cardinality.
var knownRoutes = map[string]bool{
	"/": true, "/health": true, "/ready": true, "/version": true,
	"/toggle-failure": true, "/metrics": true,
}

func routeLabel(path string) string {
	if knownRoutes[path] {
		return path
	}
	return "other"
}

// instrument meters EVERY request. Metrics used to be recorded per handler,
// which meant a route added without its own recordMetrics call — as "/" was —
// generated traffic that no dashboard could see.
func instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		route := routeLabel(r.URL.Path)
		elapsed := time.Since(start).Seconds()
		httpRequestsTotal.WithLabelValues(r.Method, route, strconv.Itoa(rec.status), serviceName).Inc()
		httpRequestDuration.WithLabelValues(r.Method, route, serviceName).Observe(elapsed)
	})
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func getEnv(key, defaultVal string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return defaultVal
}

func getDurationEnv(key string, defaultVal time.Duration) time.Duration {
	if value, ok := os.LookupEnv(key); ok {
		if duration, err := time.ParseDuration(value); err == nil {
			return duration
		}
	}
	return defaultVal
}
