// SPDX-License-Identifier: Apache-2.0
package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"

	"github.com/sagar2395/snowopslabs/internal/auth"
	"github.com/sagar2395/snowopslabs/internal/config"
	"github.com/sagar2395/snowopslabs/internal/executor"
	"github.com/sagar2395/snowopslabs/internal/incident"
	"github.com/sagar2395/snowopslabs/internal/metrics"
	"github.com/sagar2395/snowopslabs/internal/platform"
	"github.com/sagar2395/snowopslabs/internal/runtime"
	"github.com/sagar2395/snowopslabs/internal/scenario"
	"github.com/sagar2395/snowopslabs/internal/services"
	"github.com/sagar2395/snowopslabs/internal/store"
)

// Server is the API server that backs the web UI.
type Server struct {
	cfg       *config.Config
	exec      *executor.Executor
	registry  *platform.Registry
	scenes    *scenario.Engine
	incidents *incident.Engine
	svcs      *services.Registry
	runtimes  *runtime.Manager
	router    *mux.Router
	upgrader  websocket.Upgrader
	uiFS      fs.FS
	uiDir     string
	uiSource  string

	// runStore is the durable run store the run console reads.
	runStore *store.Store

	// authEnabled mirrors LABCTL_AUTH at construction time;
	// when false, the middleware is a pass-through and behaviour is unchanged.
	authEnabled bool
	users       *auth.Store
	sessions    *auth.SessionStore
	userLoadErr error
	loginLimit  *loginLimiter

	// metrics is set via WithMetrics when the /metrics endpoint is enabled;
	// nil (the default) leaves the endpoint and request instrumentation off.
	metrics *metrics.App
}

// ServerOption configures optional Server behaviour without changing the
// constructor signature (existing callers pass none).
type ServerOption func(*Server)

// WithMetrics enables the Prometheus /metrics endpoint and per-request
// instrumentation, recording into the given metric set.
func WithMetrics(app *metrics.App) ServerOption {
	return func(s *Server) { s.metrics = app }
}

// WithRunStore supplies the run store the /runs endpoints read from. Tests use
// it to inject a temp store; in production NewServer opens the default store
// best-effort when no store is provided.
func WithRunStore(st *store.Store) ServerOption {
	return func(s *Server) { s.runStore = st }
}

// WithUIDir serves the UI live from a directory on disk (via http.Dir) instead of the embedded bundle.
func WithUIDir(dir string) ServerOption {
	return func(s *Server) { s.uiDir = dir }
}

// NewServer creates a new API server. The embeddedUI parameter should be the
// embedded ui/dist filesystem (from go:embed). If nil or empty, the server
// falls back to serving UI files from the project's ui/dist/ directory.
func NewServer(cfg *config.Config, exec *executor.Executor, registry *platform.Registry, scenes *scenario.Engine, incidents *incident.Engine, svcs *services.Registry, rtm *runtime.Manager, embeddedUI fs.FS, opts ...ServerOption) *Server {
	s := &Server{
		cfg:       cfg,
		exec:      exec,
		registry:  registry,
		scenes:    scenes,
		incidents: incidents,
		svcs:      svcs,
		runtimes:  rtm,
		upgrader: websocket.Upgrader{
			CheckOrigin: originAllowed,
		},
		uiFS: embeddedUI,
	}
	// Apply options before setupRoutes so the route table reflects them (the
	// /metrics endpoint and request instrumentation are conditional on them).
	for _, opt := range opts {
		opt(s)
	}
	// Open the durable run store best-effort unless a test injected one. A
	// failure here leaves the /runs endpoints returning 503 rather than
	// preventing the server from booting — the rest of the UI is unaffected.
	if s.runStore == nil {
		if path, err := store.DefaultPath(); err == nil {
			if st, err := store.Open(context.Background(), path); err == nil {
				s.runStore = st
			} else {
				slog.Warn("run store unavailable; the run console will be empty", "error", err, "path", path)
			}
		}
	}
	if auth.Enabled() {
		s.authEnabled = true
		s.sessions = auth.NewSessionStore(0)
		s.loginLimit = newLoginLimiter(loginMaxAttempts, loginWindow)
		// Load users best-effort; an unreadable file leaves an empty store and
		// the server logs a warning at start rather than refusing to boot.
		if store, err := auth.LoadStore(auth.DefaultUsersPath(cfg.ProjectRoot)); err == nil {
			s.users = store
		} else {
			s.users = auth.NewStore()
			s.userLoadErr = err
		}
		switch {
		case s.userLoadErr != nil:
			slog.Warn("auth enabled but users file failed to load; nobody can log in",
				"error", s.userLoadErr, "path", auth.DefaultUsersPath(cfg.ProjectRoot))
		case s.users.Count() == 0:
			slog.Warn("auth enabled but no users defined; add one with 'labctl users add <name> --role operator'")
		default:
			slog.Info("auth enabled", "users", s.users.Count())
		}
	}
	s.setupRoutes()
	return s
}

// Start starts the HTTP server.
func (s *Server) Start(addr string) error {
	return s.httpServer(addr).ListenAndServe()
}

// StartTLS starts the server over HTTPS using the given certificate and key.
func (s *Server) StartTLS(addr, certFile, keyFile string) error {
	return s.httpServer(addr).ListenAndServeTLS(certFile, keyFile)
}

func (s *Server) httpServer(addr string) *http.Server {
	// WriteTimeout is deliberately unset: the event stream (WebSocket and SSE)
	// is a long-lived response that a write deadline would sever. Read/idle
	// timeouts still bound slow-loris and idle connections.
	return &http.Server{
		Addr:        addr,
		Handler:     s.router,
		ReadTimeout: 15 * time.Second,
		IdleTimeout: 60 * time.Second,
	}
}

// CheckBind refuses a network-exposed bind when authentication is off.
func CheckBind(host string, authEnabled bool) error {
	if authEnabled || IsLoopbackHost(host) {
		return nil
	}
	shown := host
	if shown == "" {
		shown = "0.0.0.0 (all interfaces)"
	}
	return fmt.Errorf(
		"refusing to bind %s without authentication: it exposes cluster-control endpoints to the network; "+
			"enable auth (set LABCTL_AUTH=true and add a user with 'labctl users add') or bind to 127.0.0.1",
		shown)
}

// IsLoopbackHost reports whether host refers only to the local machine. An empty
// host means "all interfaces" and is therefore NOT loopback.
func IsLoopbackHost(host string) bool {
	if host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (s *Server) setupRoutes() {
	s.router = mux.NewRouter()

	s.registerAPI(s.router.PathPrefix("/api/v2").Subrouter(), apiV2)
	s.registerAPI(s.router.PathPrefix("/api").Subrouter(), apiV1)

	if s.metrics != nil {
		s.router.Handle("/metrics", s.metrics.Handler())
	}

	s.router.PathPrefix("/").Handler(spaHandler(s.resolveUIFS()))
}

// resolveUIFS decides where the UI is served from and records a startup banner
// (UIInfo) naming the source and the built bundle, so a stale server process is
// obvious rather than silently serving old code.
func (s *Server) resolveUIFS() http.FileSystem {
	if s.uiDir != "" {
		s.uiSource = fmt.Sprintf("UI from disk (live): %s [%s]", s.uiDir, uiBundleName(http.Dir(s.uiDir)))
		return http.Dir(s.uiDir)
	}
	if s.uiFS != nil {
		if _, err := fs.Stat(s.uiFS, "index.html"); err == nil {
			fsys := http.FS(s.uiFS)
			s.uiSource = fmt.Sprintf("embedded UI [%s] — rebuild with `make cli-build` to update", uiBundleName(fsys))
			return fsys
		}
	}
	// Dev checkout with no embedded bundle: serve the Vite output from disk.
	for _, p := range []string{
		filepath.Join(s.cfg.ProjectRoot, "src", "ui", "dist"),
		filepath.Join(s.cfg.ProjectRoot, "ui", "dist"),
	} {
		if _, err := os.Stat(filepath.Join(p, "index.html")); err == nil {
			s.uiSource = fmt.Sprintf("UI from disk: %s [%s]", p, uiBundleName(http.Dir(p)))
			return http.Dir(p)
		}
	}
	fallback := filepath.Join(s.cfg.ProjectRoot, "src", "ui", "dist")
	s.uiSource = "UI not found (no embedded bundle and no built dist) — run `make ui`"
	return http.Dir(fallback)
}

// UIInfo returns a one-line description of the resolved UI source and its built
// bundle hash, for `labctl ui` to print on startup.
func (s *Server) UIInfo() string { return s.uiSource }

var uiBundleRe = regexp.MustCompile(`assets/index-[A-Za-z0-9_-]+\.(?:js|css)`)

// uiBundleName reads index.html and returns the hashed entry-bundle filename,
// which changes on every UI build — the fingerprint that tells two builds apart.
func uiBundleName(fsys http.FileSystem) string {
	f, err := fsys.Open("index.html")
	if err != nil {
		return "no index.html"
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 1<<16))
	if err != nil {
		return "unreadable"
	}
	if m := uiBundleRe.FindString(string(b)); m != "" {
		return strings.TrimPrefix(m, "assets/")
	}
	return "unknown bundle"
}

// spaHandler serves static UI assets, falling back to index.html for any path
// that is not an existing file.
func spaHandler(fsys http.FileSystem) http.Handler {
	fileServer := http.FileServer(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		f, err := fsys.Open(name)
		if err != nil {
			// Unknown path — hand the SPA shell to the client router.
			rr := new(http.Request)
			*rr = *r
			rr.URL = new(url.URL)
			*rr.URL = *r.URL
			rr.URL.Path = "/"
			fileServer.ServeHTTP(w, rr)
			return
		}
		_ = f.Close()
		fileServer.ServeHTTP(w, r)
	})
}

// registerAPI wires the shared middleware chain and the full route table onto
// one API subrouter. It is called once per version prefix so /api and /api/v2
// serve identical behaviour today; future versions can diverge by branching
// inside handlers on apiVersion(r) without duplicating the table.
func (s *Server) registerAPI(api *mux.Router, version string) {
	// Middleware, outermost first. Request ID and API version are context tags
	// set before anything can reject the request, so even CORS/auth failures get
	// a correlation ID and the right error envelope. Access logging wraps the
	// rest so every request — served or rejected — produces one log line.
	api.Use(s.requestIDMiddleware)
	api.Use(apiVersionMiddleware(version))
	api.Use(s.accessLogMiddleware)
	api.Use(corsMiddleware)
	api.Use(jsonMiddleware)
	// Auth middleware is a pass-through when LABCTL_AUTH is off, so the local
	// experience is unchanged. When on, it gates every /api route except the
	// auth endpoints below and enforces operator-only mutations.
	api.Use(s.authMiddleware)
	// Request instrumentation, innermost so it measures the handler itself.
	// Only active when metrics are enabled.
	if s.metrics != nil {
		api.Use(s.metricsMiddleware)
	}

	// Auth endpoints (always registered; meaningful only when auth is enabled).
	api.HandleFunc("/auth/me", s.handleAuthMe).Methods("GET", "OPTIONS")
	api.HandleFunc("/auth/login", s.handleAuthLogin).Methods("POST", "OPTIONS")
	api.HandleFunc("/auth/logout", s.handleAuthLogout).Methods("POST", "OPTIONS")

	api.HandleFunc("/status", s.handleStatus).Methods("GET", "OPTIONS")
	api.HandleFunc("/jobs", s.handleJobs).Methods("GET", "OPTIONS")
	api.HandleFunc("/apps", s.handleListApps).Methods("GET", "OPTIONS")
	api.HandleFunc("/apps/{name}/detail", s.handleAppDetail).Methods("GET", "OPTIONS")
	api.HandleFunc("/apps/{name}/build", s.handleAppBuild).Methods("POST", "OPTIONS")
	api.HandleFunc("/apps/{name}/deploy", s.handleAppDeploy).Methods("POST", "OPTIONS")
	api.HandleFunc("/apps/{name}/destroy", s.handleAppDestroy).Methods("POST", "OPTIONS")
	api.HandleFunc("/platform", s.handlePlatformStatus).Methods("GET", "OPTIONS")
	api.HandleFunc("/platform/up", s.handlePlatformUp).Methods("POST", "OPTIONS")
	api.HandleFunc("/platform/down", s.handlePlatformDown).Methods("POST", "OPTIONS")
	api.HandleFunc("/platform/component/{category}/{name}", s.handlePlatformComponentDetail).Methods("GET", "OPTIONS")
	api.HandleFunc("/platform/component/{category}/{name}/up", s.handleComponentUp).Methods("POST", "OPTIONS")
	api.HandleFunc("/platform/component/{category}/{name}/down", s.handleComponentDown).Methods("POST", "OPTIONS")
	api.HandleFunc("/dashboards", s.handleDashboardURLs).Methods("GET", "OPTIONS")
	api.HandleFunc("/scenarios", s.handleListScenarios).Methods("GET", "OPTIONS")
	api.HandleFunc("/scenarios/{name}", s.handleScenarioInfo).Methods("GET", "OPTIONS")
	api.HandleFunc("/scenarios/{name}/up", s.handleScenarioUp).Methods("POST", "OPTIONS")
	api.HandleFunc("/scenarios/{name}/down", s.handleScenarioDown).Methods("POST", "OPTIONS")
	api.HandleFunc("/scenarios/{name}/verify", s.handleScenarioVerify).Methods("POST", "OPTIONS")
	api.HandleFunc("/incidents", s.handleListIncidents).Methods("GET", "OPTIONS")
	api.HandleFunc("/incidents/inject-random", s.handleIncidentInjectRandom).Methods("POST", "OPTIONS")
	api.HandleFunc("/incidents/status", s.handleIncidentStatus).Methods("GET", "OPTIONS")
	api.HandleFunc("/incidents/resolve", s.handleIncidentResolve).Methods("POST", "OPTIONS")
	api.HandleFunc("/incidents/hint", s.handleIncidentHint).Methods("POST", "OPTIONS")
	api.HandleFunc("/incidents/history", s.handleIncidentHistory).Methods("GET", "OPTIONS")
	api.HandleFunc("/incidents/{name}/inject", s.handleIncidentInject).Methods("POST", "OPTIONS")
	api.HandleFunc("/lab/snapshots", s.handleLabSnapshots).Methods("GET", "OPTIONS")
	api.HandleFunc("/lab/snapshots/{name}", s.handleLabSnapshotTake).Methods("POST", "OPTIONS")
	api.HandleFunc("/lab/snapshots/{name}", s.handleLabSnapshotDelete).Methods("DELETE", "OPTIONS")
	api.HandleFunc("/lab/snapshots/{name}/restore", s.handleLabRestore).Methods("POST", "OPTIONS")
	api.HandleFunc("/lab/reset", s.handleLabReset).Methods("POST", "OPTIONS")
	api.HandleFunc("/results", s.handleResults).Methods("GET", "OPTIONS")
	api.HandleFunc("/leaderboard", s.handleLeaderboard).Methods("GET", "OPTIONS")
	api.HandleFunc("/results/{kind}", s.handleResultsByKind).Methods("GET", "OPTIONS")
	api.HandleFunc("/progress", s.handleProgress).Methods("GET", "OPTIONS")
	api.HandleFunc("/challenges", s.handleListChallenges).Methods("GET", "OPTIONS")
	api.HandleFunc("/challenges/status", s.handleChallengeStatus).Methods("GET", "OPTIONS")
	api.HandleFunc("/challenges/history", s.handleChallengeHistory).Methods("GET", "OPTIONS")
	api.HandleFunc("/challenges/complete", s.handleChallengeMarkComplete).Methods("POST", "OPTIONS")
	api.HandleFunc("/challenges/{name}", s.handleChallengeInfo).Methods("GET", "OPTIONS")
	api.HandleFunc("/learn/paths", s.handleLearnPaths).Methods("GET", "OPTIONS")
	api.HandleFunc("/learn/paths/{name}", s.handleLearnPath).Methods("GET", "OPTIONS")
	api.HandleFunc("/learn/paths/{name}/start", s.handleLearnStart).Methods("POST", "OPTIONS")
	api.HandleFunc("/learn/paths/{name}/reset", s.handleLearnReset).Methods("POST", "OPTIONS")
	api.HandleFunc("/learn/paths/{name}/progress", s.handleLearnProgress).Methods("GET", "OPTIONS")
	api.HandleFunc("/learn/paths/{name}/complete", s.handleLearnMarkComplete).Methods("POST", "OPTIONS")
	api.HandleFunc("/services", s.handleListServices).Methods("GET", "OPTIONS")
	api.HandleFunc("/services/{name}/up", s.handleServiceUp).Methods("POST", "OPTIONS")
	api.HandleFunc("/services/{name}/down", s.handleServiceDown).Methods("POST", "OPTIONS")
	api.HandleFunc("/traffic", s.handleTrafficInfo).Methods("GET", "OPTIONS")
	api.HandleFunc("/traffic/start", s.handleTrafficStart).Methods("POST", "OPTIONS")
	api.HandleFunc("/traffic/stop", s.handleTrafficStop).Methods("POST", "OPTIONS")
	api.HandleFunc("/runs", s.handleRunsList).Methods("GET", "OPTIONS")
	api.HandleFunc("/runs/{id}", s.handleRunGet).Methods("GET", "OPTIONS")
	api.HandleFunc("/runs/{id}/logs", s.handleRunLogs).Methods("GET", "OPTIONS")
	api.HandleFunc("/runs/{id}/cancel", s.handleRunCancel).Methods("POST", "OPTIONS")
	api.HandleFunc("/runtimes", s.handleListRuntimes).Methods("GET", "OPTIONS")
	api.HandleFunc("/runtimes/{name}/activate", s.handleRuntimeActivate).Methods("POST", "OPTIONS")
	api.HandleFunc("/runtimes/{name}/deactivate", s.handleRuntimeDeactivate).Methods("POST", "OPTIONS")

	api.HandleFunc("/ws", s.handleWebSocket)
	api.HandleFunc("/stream", s.handleStreamSSE).Methods("GET", "OPTIONS")
}

func originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	oh := u.Hostname()
	if oh == "localhost" || oh == "127.0.0.1" || oh == "::1" {
		return true
	}
	rh := r.Host
	if h, _, err := net.SplitHostPort(rh); err == nil {
		rh = h
	}
	return strings.EqualFold(oh, rh)
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && originAllowed(r) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		}
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		if (r.Method == "POST" || r.Method == "DELETE") && origin != "" && !originAllowed(r) {
			respondError(w, r, http.StatusForbidden, "forbidden_origin", "cross-origin requests are not allowed")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func jsonMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		next.ServeHTTP(w, r)
	})
}

type ErrorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, r *http.Request, status int, code, msg string) {
	if apiVersionFrom(r.Context()) == apiV2 {
		respondProblem(w, r, status, code, msg)
		return
	}
	respondJSON(w, status, ErrorResponse{Error: msg, Code: code})
}
