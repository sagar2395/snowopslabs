package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
)

const maxBodyBytes = 1 << 20 // 1 MiB
const defaultCacheTTL = time.Hour

// build-time version info injected via -ldflags
var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

var (
	port        = getEnv("PORT", "8080")
	serviceName = getEnv("SERVICE_NAME", "echo-server")
	redisURL    = getEnv("REDIS_URL", "")
	cacheTTL    time.Duration
	logger      *slog.Logger
	redisClient *redis.Client

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
	// Structured logger
	logLevel := slog.LevelInfo
	switch getEnv("LOG_LEVEL", "info") {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	}
	logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(logger)
	cacheTTL = parseCacheTTL(getEnv("CACHE_TTL", defaultCacheTTL.String()))

	// Redis cache TTL (default 1h; "0" means no expiry)
	const defaultCacheTTL = time.Hour
	if raw := getEnv("CACHE_TTL", ""); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			logger.Warn("invalid CACHE_TTL value, using default 1h", "value", raw, "error", err)
			cacheTTL = defaultCacheTTL
		} else {
			cacheTTL = d
		}
	} else {
		cacheTTL = defaultCacheTTL
	}
	logger.Info("cache TTL configured", "ttl", cacheTTL)

	// Redis client (optional)
	if redisURL != "" {
		opts, err := redis.ParseURL(redisURL)
		if err != nil {
			logger.Error("failed to parse REDIS_URL", "error", err)
			os.Exit(1)
		}
		redisClient = redis.NewClient(opts)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := redisClient.Ping(ctx).Err(); err != nil {
			logger.Warn("redis not reachable at startup, continuing", "url", redisURL, "error", err)
		} else {
			logger.Info("redis connected", "url", redisURL)
		}
	}

	// Routes
	mux := http.NewServeMux()
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/ready", handleReady)
	mux.HandleFunc("/version", handleVersion)
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/echo", handleEcho)
	mux.HandleFunc("/cache", handleCache)
	mux.HandleFunc("/", handleRoot)

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		logger.Info("server starting", "port", port, "service", serviceName, "redis", redisURL != "", "cache_ttl", cacheTTL.String())
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	sig := <-sigChan
	logger.Info("received signal, shutting down", "signal", sig)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if redisClient != nil {
		if err := redisClient.Close(); err != nil {
			logger.Error("redis close error", "error", err)
		}
	}

	if err := server.Shutdown(ctx); err != nil {
		logger.Error("shutdown error", "error", err)
		os.Exit(1)
	}

	logger.Info("server stopped")
}

// handleRoot returns service info and available endpoints.
func handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	start := time.Now()
	defer func() { recordMetrics(r, http.StatusOK, start) }()

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"service": serviceName,
		"version": version,
		"endpoints": []string{
			"/health   - Health check (always 200)",
			"/ready    - Readiness check (checks Redis if configured)",
			"/version  - Build version info",
			"/echo     - Echo back request details",
			"/cache    - GET/POST key-value cache via Redis",
			"/metrics  - Prometheus metrics",
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

// handleHealth returns 200 OK with Redis connectivity reported as a non-fatal detail.
func handleHealth(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() { recordMetrics(r, http.StatusOK, start) }()

	logger.Debug("health check", "remote", r.RemoteAddr)

	resp := map[string]interface{}{"status": "ok"}
	if redisClient != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := redisClient.Ping(ctx).Err(); err != nil {
			resp["redis"] = map[string]string{"status": "degraded", "error": err.Error()}
		} else {
			resp["redis"] = map[string]string{"status": "ok"}
		}
	}
	respondJSON(w, http.StatusOK, resp)
}

// handleReady always returns 200 — the app is ready as long as its HTTP listener is up.
// Redis is optional; its status is reported separately in /health.
func handleReady(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	logger.Debug("readiness check", "remote", r.RemoteAddr)
	recordMetrics(r, http.StatusOK, start)
	respondJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// handleEcho echoes back request details as JSON.
func handleEcho(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			recordMetrics(r, http.StatusRequestEntityTooLarge, start)
			respondJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request body too large"})
			return
		}
		recordMetrics(r, http.StatusBadRequest, start)
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read body"})
		return
	}
	defer r.Body.Close()

	headers := make(map[string]string)
	for name, values := range r.Header {
		headers[name] = values[0]
	}

	query := make(map[string]string)
	for key, values := range r.URL.Query() {
		query[key] = values[0]
	}

	response := map[string]interface{}{
		"method":    r.Method,
		"path":      r.URL.Path,
		"headers":   headers,
		"query":     query,
		"body":      string(body),
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}

	logger.Debug("echo request",
		"method", r.Method,
		"path", r.URL.Path,
		"remote", r.RemoteAddr,
	)

	recordMetrics(r, http.StatusOK, start)
	respondJSON(w, http.StatusOK, response)
}

// handleCache supports GET (retrieve by key) and POST (store by key) using Redis.
func handleCache(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	if redisClient == nil {
		recordMetrics(r, http.StatusServiceUnavailable, start)
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "redis not configured (set REDIS_URL)",
		})
		return
	}

	key := r.URL.Query().Get("key")
	if key == "" {
		recordMetrics(r, http.StatusBadRequest, start)
		respondJSON(w, http.StatusBadRequest, map[string]string{
			"error": "query parameter 'key' is required",
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	switch r.Method {
	case http.MethodGet:
		val, err := redisClient.Get(ctx, key).Result()
		if err == redis.Nil {
			recordMetrics(r, http.StatusNotFound, start)
			respondJSON(w, http.StatusNotFound, map[string]string{
				"error": fmt.Sprintf("key %q not found", key),
			})
			return
		}
		if err != nil {
			logger.Error("redis get error", "key", key, "error", err)
			recordMetrics(r, http.StatusInternalServerError, start)
			respondJSON(w, http.StatusInternalServerError, map[string]string{
				"error": "failed to retrieve from cache",
			})
			return
		}

		logger.Debug("cache get", "key", key)
		recordMetrics(r, http.StatusOK, start)
		respondJSON(w, http.StatusOK, map[string]string{
			"key":   key,
			"value": val,
		})

	case http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				recordMetrics(r, http.StatusRequestEntityTooLarge, start)
				respondJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request body too large"})
				return
			}
			recordMetrics(r, http.StatusBadRequest, start)
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read body"})
			return
		}
		defer r.Body.Close()

		if err := redisClient.Set(ctx, key, string(body), cacheTTL).Err(); err != nil {
			logger.Error("redis set error", "key", key, "error", err)
			recordMetrics(r, http.StatusInternalServerError, start)
			respondJSON(w, http.StatusInternalServerError, map[string]string{
				"error": "failed to store in cache",
			})
			return
		}

		logger.Debug("cache set", "key", key)
		recordMetrics(r, http.StatusCreated, start)
		respondJSON(w, http.StatusCreated, map[string]string{
			"key":     key,
			"value":   string(body),
			"message": "stored",
		})

	default:
		recordMetrics(r, http.StatusMethodNotAllowed, start)
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{
			"error": "only GET and POST are supported",
		})
	}
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

func parseCacheTTL(value string) time.Duration {
	if value == "" {
		return defaultCacheTTL
	}
	ttl, err := time.ParseDuration(value)
	if err != nil {
		logger.Warn("invalid CACHE_TTL, using default", "value", value, "default", defaultCacheTTL.String(), "error", err)
		return defaultCacheTTL
	}
	return ttl
}

func getEnv(key, defaultVal string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return defaultVal
}
