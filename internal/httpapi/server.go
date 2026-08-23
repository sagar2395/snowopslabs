// SPDX-License-Identifier: Apache-2.0
package httpapi

import (
	"encoding/json"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"

	"github.com/sagar2395/snowopslabs/internal/auth"
	"github.com/sagar2395/snowopslabs/internal/config"
	"github.com/sagar2395/snowopslabs/internal/executor"
	"github.com/sagar2395/snowopslabs/internal/incident"
	"github.com/sagar2395/snowopslabs/internal/platform"
	"github.com/sagar2395/snowopslabs/internal/runtime"
	"github.com/sagar2395/snowopslabs/internal/scenario"
	"github.com/sagar2395/snowopslabs/internal/services"
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

	// Auth (task 062). authEnabled mirrors LABCTL_AUTH at construction time;
	// when false, the middleware is a pass-through and behaviour is unchanged.
	authEnabled bool
	users       *auth.Store
	sessions    *auth.SessionStore
	userLoadErr error
}

// NewServer creates a new API server. The embeddedUI parameter should be the
// embedded ui/dist filesystem (from go:embed). If nil or empty, the server
// falls back to serving UI files from the project's ui/dist/ directory.
func NewServer(cfg *config.Config, exec *executor.Executor, registry *platform.Registry, scenes *scenario.Engine, incidents *incident.Engine, svcs *services.Registry, rtm *runtime.Manager, embeddedUI fs.FS) *Server {
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
	if auth.Enabled() {
		s.authEnabled = true
		s.sessions = auth.NewSessionStore(0)
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
	srv := &http.Server{
		Addr:         addr,
		Handler:      s.router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	return srv.ListenAndServe()
}

func (s *Server) setupRoutes() {
	s.router = mux.NewRouter()

	// API routes
	api := s.router.PathPrefix("/api").Subrouter()
	api.Use(corsMiddleware)
	api.Use(jsonMiddleware)
	// Auth middleware is a pass-through when LABCTL_AUTH is off, so the local
	// experience is unchanged. When on, it gates every /api route except the
	// auth endpoints below and enforces operator-only mutations.
	api.Use(s.authMiddleware)

	// Auth endpoints (always registered; meaningful only when auth is enabled).
	api.HandleFunc("/auth/me", s.handleAuthMe).Methods("GET", "OPTIONS")
	api.HandleFunc("/auth/login", s.handleAuthLogin).Methods("POST", "OPTIONS")
	api.HandleFunc("/auth/logout", s.handleAuthLogout).Methods("POST", "OPTIONS")

	api.HandleFunc("/status", s.handleStatus).Methods("GET", "OPTIONS")
	api.HandleFunc("/jobs", s.handleJobs).Methods("GET", "OPTIONS")
	api.HandleFunc("/apps", s.handleListApps).Methods("GET", "OPTIONS")
	api.HandleFunc("/apps/{name}/build", s.handleAppBuild).Methods("POST", "OPTIONS")
	api.HandleFunc("/apps/{name}/deploy", s.handleAppDeploy).Methods("POST", "OPTIONS")
	api.HandleFunc("/apps/{name}/destroy", s.handleAppDestroy).Methods("POST", "OPTIONS")
	api.HandleFunc("/platform", s.handlePlatformStatus).Methods("GET", "OPTIONS")
	api.HandleFunc("/platform/up", s.handlePlatformUp).Methods("POST", "OPTIONS")
	api.HandleFunc("/platform/down", s.handlePlatformDown).Methods("POST", "OPTIONS")
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
	// Literal paths MUST be registered before the "/{name}" wildcard: gorilla/mux
	// matches in registration order, so "/challenges/status" and
	// "/challenges/history" would otherwise be swallowed by "/{name}" (name=
	// "status"/"history") and 404 when the lookup fails.
	api.HandleFunc("/challenges/status", s.handleChallengeStatus).Methods("GET", "OPTIONS")
	api.HandleFunc("/challenges/history", s.handleChallengeHistory).Methods("GET", "OPTIONS")
	api.HandleFunc("/challenges/complete", s.handleChallengeMarkComplete).Methods("POST", "OPTIONS")
	api.HandleFunc("/challenges/{name}", s.handleChallengeInfo).Methods("GET", "OPTIONS")
	api.HandleFunc("/learn/paths", s.handleLearnPaths).Methods("GET", "OPTIONS")
	api.HandleFunc("/learn/paths/{name}", s.handleLearnPath).Methods("GET", "OPTIONS")
	api.HandleFunc("/learn/paths/{name}/start", s.handleLearnStart).Methods("POST", "OPTIONS")
	api.HandleFunc("/learn/paths/{name}/progress", s.handleLearnProgress).Methods("GET", "OPTIONS")
	api.HandleFunc("/learn/paths/{name}/complete", s.handleLearnMarkComplete).Methods("POST", "OPTIONS")
	api.HandleFunc("/services", s.handleListServices).Methods("GET", "OPTIONS")
	api.HandleFunc("/services/{name}/up", s.handleServiceUp).Methods("POST", "OPTIONS")
	api.HandleFunc("/services/{name}/down", s.handleServiceDown).Methods("POST", "OPTIONS")
	api.HandleFunc("/runtimes", s.handleListRuntimes).Methods("GET", "OPTIONS")
	api.HandleFunc("/runtimes/{name}/activate", s.handleRuntimeActivate).Methods("POST", "OPTIONS")
	api.HandleFunc("/runtimes/{name}/deactivate", s.handleRuntimeDeactivate).Methods("POST", "OPTIONS")

	api.HandleFunc("/ws", s.handleWebSocket)

	// Serve UI — use embedded FS if available, fall back to filesystem for dev
	var uiHandler http.Handler
	if s.uiFS != nil {
		if _, err := fs.Stat(s.uiFS, "index.html"); err == nil {
			uiHandler = http.FileServer(http.FS(s.uiFS))
		}
	}
	if uiHandler == nil {
		uiHandler = http.FileServer(http.Dir(s.cfg.ProjectRoot + "/ui/dist"))
	}
	s.router.PathPrefix("/").Handler(uiHandler)
}

// originAllowed reports whether a browser-supplied Origin header is trusted:
// no Origin (curl, CLI clients), localhost origins (Vite dev server), or an
// origin whose host matches the request host (the embedded UI itself).
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
		// Reject cross-site state-changing requests. Browsers always attach
		// an Origin header to cross-origin POSTs, so a malicious website
		// can't trigger cluster actions on localhost; CLI clients send no
		// Origin and are unaffected.
		if (r.Method == "POST" || r.Method == "DELETE") && origin != "" && !originAllowed(r) {
			respondError(w, http.StatusForbidden, "forbidden_origin", "cross-origin requests are not allowed")
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

// ErrorResponse is the standard JSON envelope for all 4xx/5xx responses.
type ErrorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, code, msg string) {
	respondJSON(w, status, ErrorResponse{Error: msg, Code: code})
}
