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

var (
	port            = getEnv("PORT", "8080")
	shutdownTimeout = getDurationEnv("SHUTDOWN_TIMEOUT", 30*time.Second)
	serviceName     = getEnv("SERVICE_NAME", "go-api")
	otelEndpoint    = getEnv("OTEL_EXPORTER_OTLP_ENDPOINT", "")

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

	// Wrap with OTel HTTP instrumentation if tracing is configured
	var handler http.Handler = mux
	if otelEndpoint != "" {
		handler = otelhttp.NewHandler(mux, "http",
			otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
				return r.Method + " " + r.URL.Path
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
			semconv.ServiceVersion("0.1.0"),
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
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"service":         serviceName,
		"version":         version,
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

func handleVersion(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]string{
		"app":       serviceName,
		"version":   version,
		"commit":    commit,
		"buildDate": buildDate,
	})
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer recordMetrics(r, http.StatusOK, start)

	_, span := tracer.Start(r.Context(), "health-check")
	defer span.End()

	logger.Debug("health check", "remote", r.RemoteAddr)
	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func handleReady(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	_, span := tracer.Start(r.Context(), "readiness-check")
	defer span.End()

	if simulateFailure.Load() {
		span.SetAttributes(attribute.Bool("ready", false))
		logger.Warn("readiness check failed (simulated)", "remote", r.RemoteAddr)
		recordMetrics(r, http.StatusServiceUnavailable, start)
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status": "not_ready",
			"reason": "simulated failure",
		})
		return
	}

	span.SetAttributes(attribute.Bool("ready", true))
	logger.Debug("readiness check passed", "remote", r.RemoteAddr)
	recordMetrics(r, http.StatusOK, start)
	respondJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func handleToggleFailure(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	prev := simulateFailure.Load()
	simulateFailure.Store(!prev)
	current := simulateFailure.Load()

	logger.Info("failure simulation toggled", "previous", prev, "current", current)
	recordMetrics(r, http.StatusOK, start)
	respondJSON(w, http.StatusOK, map[string]string{
		"simulateFailure": strconv.FormatBool(current),
		"message":         "Readiness failure simulation toggled. Hit /ready to test.",
	})
}

func recordMetrics(r *http.Request, code int, start time.Time) {
	httpRequestsTotal.WithLabelValues(r.Method, r.URL.Path, strconv.Itoa(code), serviceName).Inc()
	httpRequestDuration.WithLabelValues(r.Method, r.URL.Path, serviceName).Observe(time.Since(start).Seconds())
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
