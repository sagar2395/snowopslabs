// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"net/http"
	"testing"
	"time"

	"github.com/sagar2395/snowopslabs/internal/store"
	"github.com/sagar2395/snowopslabs/internal/toolchain"
)

// streamingRoutes hold a connection open, so they have their own tests
// (stream_test.go) instead of a row.
var streamingRoutes = []string{"GET /ws", "GET /stream"}

const (
	get    = http.MethodGet
	post   = http.MethodPost
	del    = http.MethodDelete
	accept = http.StatusAccepted
	ok     = http.StatusOK
)

// contract is the table: one row per route.
var contract = []contractRow{
	// ── auth ──
	{method: get, route: "/auth/me", status: ok},
	{method: post, route: "/auth/login", authOn: true, body: `{"username":"olive","password":"pw"}`, status: ok,
		badBody: `{"username":"olive","password":"wrong"}`, problem: "invalid_credentials", code: http.StatusUnauthorized},
	{method: post, route: "/auth/logout", authOn: true, status: ok},

	// ── lab overview ──
	{method: get, route: "/status", status: ok},
	{method: get, route: "/capacity", status: ok, prepare: dockerWith4GB},
	{method: get, route: "/jobs", status: ok},
	{method: get, route: "/dashboards", status: ok},

	// ── apps ──
	{method: get, route: "/apps", status: ok},
	{method: get, route: "/apps/{name}/detail", path: "/apps/demo-app/detail", status: ok,
		badPath: "/apps/ghost/detail", problem: "not_found", code: http.StatusNotFound},
	{method: post, route: "/apps/{name}/build", path: "/apps/demo-app/build", status: accept, operatorOnly: true,
		badPath: "/apps/ghost/build", problem: "not_found", code: http.StatusNotFound},
	{method: post, route: "/apps/{name}/deploy", path: "/apps/demo-app/deploy", status: accept, operatorOnly: true,
		badPath: "/apps/ghost/deploy", problem: "not_found", code: http.StatusNotFound},
	{method: post, route: "/apps/{name}/destroy", path: "/apps/demo-app/destroy", status: accept, operatorOnly: true,
		badPath: "/apps/ghost/destroy", problem: "not_found", code: http.StatusNotFound},

	// ── platform ──
	{method: get, route: "/platform", status: ok},
	{method: post, route: "/platform/up", status: accept, operatorOnly: true},
	{method: post, route: "/platform/down", status: accept, operatorOnly: true},
	{method: get, route: "/platform/component/{category}/{name}", path: "/platform/component/ingress/traefik", status: ok,
		badPath: "/platform/component/ingress/ghost", problem: "not_found", code: http.StatusNotFound},
	{method: post, route: "/platform/component/{category}/{name}/up", path: "/platform/component/ingress/traefik/up", status: accept, operatorOnly: true,
		badPath: "/platform/component/ingress/ghost/up", problem: "not_found", code: http.StatusNotFound},
	{method: post, route: "/platform/component/{category}/{name}/down", path: "/platform/component/ingress/traefik/down", status: accept, operatorOnly: true,
		badPath: "/platform/component/ingress/ghost/down", problem: "not_found", code: http.StatusNotFound},

	// ── scenarios ──
	{method: get, route: "/scenarios", status: ok},
	{method: get, route: "/scenarios/{name}", path: "/scenarios/demo-scenario", status: ok,
		badPath: "/scenarios/ghost", problem: "not_found", code: http.StatusNotFound},
	{method: post, route: "/scenarios/{name}/up", path: "/scenarios/demo-scenario/up", status: accept,
		badPath: "/scenarios/ghost/up", problem: "not_found", code: http.StatusNotFound},
	{method: post, route: "/scenarios/{name}/down", path: "/scenarios/demo-scenario/down", status: accept,
		badPath: "/scenarios/ghost/down", problem: "not_found", code: http.StatusNotFound},
	{method: post, route: "/scenarios/{name}/verify", path: "/scenarios/demo-scenario/verify", status: ok,
		badPath: "/scenarios/ghost/verify", problem: "not_found", code: http.StatusNotFound},

	// ── incidents ──
	{method: get, route: "/incidents", status: ok},
	{method: get, route: "/incidents/status", status: ok},
	{method: get, route: "/incidents/history", status: ok, prepare: playedLab},
	{method: post, route: "/incidents/{name}/inject", path: "/incidents/demo-fault/inject?app=demo-app", status: ok, operatorOnly: true,
		badPath: "/incidents/ghost/inject", problem: "not_found", code: http.StatusNotFound},
	{method: post, route: "/incidents/inject-random", path: "/incidents/inject-random?app=demo-app&seed=1", status: ok, operatorOnly: true,
		badPath: "/incidents/inject-random?app=demo-app&category=storage", problem: "no_eligible_fault", code: http.StatusConflict},
	{method: post, route: "/incidents/hint", status: ok, prepare: injectDemoFault,
		problem: "no_active_incident", code: http.StatusConflict},
	{method: post, route: "/incidents/resolve", status: ok, operatorOnly: true, prepare: injectDemoFault,
		problem: "no_active_incident", code: http.StatusConflict},

	// ── lab snapshots ──
	{method: get, route: "/lab/snapshots", status: ok, prepare: takeSnapshot},
	{method: post, route: "/lab/snapshots/{name}", path: "/lab/snapshots/before-drill", status: ok, operatorOnly: true,
		badPath: "/lab/snapshots/bad%20name", problem: "invalid_input", code: http.StatusBadRequest},
	{method: del, route: "/lab/snapshots/{name}", path: "/lab/snapshots/before-drill", status: ok, operatorOnly: true, prepare: takeSnapshot,
		badPath: "/lab/snapshots/ghost", problem: "not_found", code: http.StatusNotFound},
	{method: post, route: "/lab/snapshots/{name}/restore", path: "/lab/snapshots/before-drill/restore", status: accept, operatorOnly: true, prepare: takeSnapshot,
		badPath: "/lab/snapshots/ghost/restore", problem: "not_found", code: http.StatusNotFound},
	{method: post, route: "/lab/reset", path: "/lab/reset?confirm=true", status: accept, operatorOnly: true,
		badPath: "/lab/reset", problem: "confirmation_required", code: http.StatusBadRequest},

	// ── results ──
	{method: get, route: "/results", status: ok, prepare: playedLab},
	{method: get, route: "/results/{kind}", path: "/results/incident", status: ok, prepare: playedLab,
		badPath: "/results/scenario", problem: "invalid_kind", code: http.StatusBadRequest},
	{method: get, route: "/leaderboard", status: ok, prepare: playedLab},
	{method: get, route: "/progress", status: ok, prepare: playedLab},
	{method: get, route: "/comparisons", status: ok, prepare: storedComparison},
	{method: get, route: "/comparisons/{id}", status: ok, prepare: oneComparison,
		badPath: "/comparisons/ghost", problem: "not_found", code: http.StatusNotFound},

	// ── challenges ──
	{method: get, route: "/challenges", status: ok},
	{method: get, route: "/challenges/status", status: ok},
	{method: get, route: "/challenges/history", status: ok, prepare: playedLab},
	{method: get, route: "/challenges/{name}", path: "/challenges/demo-challenge", status: ok,
		badPath: "/challenges/ghost", problem: "not_found", code: http.StatusNotFound},
	{method: post, route: "/challenges/complete", status: ok, prepare: startDemoChallenge,
		problem: "no_active_challenge", code: http.StatusConflict},

	// ── learning paths ──
	{method: get, route: "/learn/paths", status: ok},
	{method: get, route: "/learn/paths/{name}", path: "/learn/paths/demo-path", status: ok,
		badPath: "/learn/paths/ghost", problem: "not_found", code: http.StatusNotFound},
	{method: get, route: "/learn/paths/{name}/progress", path: "/learn/paths/demo-path/progress", status: ok,
		badPath: "/learn/paths/ghost/progress", problem: "not_found", code: http.StatusNotFound},
	{method: post, route: "/learn/paths/{name}/start", path: "/learn/paths/demo-path/start", status: ok,
		badPath: "/learn/paths/ghost/start", problem: "not_found", code: http.StatusNotFound},
	{method: post, route: "/learn/paths/{name}/reset", path: "/learn/paths/demo-path/reset", status: ok,
		badPath: "/learn/paths/ghost/reset", problem: "not_found", code: http.StatusNotFound},
	{method: post, route: "/learn/paths/{name}/complete", path: "/learn/paths/demo-path/complete", body: `{"moduleIdx":0}`, status: ok, prepare: startDemoPath,
		badBody: `{"moduleIdx":0}`, problem: "path_not_started", code: http.StatusConflict},

	// ── services ──
	{method: get, route: "/services", status: ok},
	{method: post, route: "/services/{name}/up", path: "/services/pager/up", status: accept, operatorOnly: true,
		badPath: "/services/ghost/up", problem: "not_found", code: http.StatusNotFound},
	{method: post, route: "/services/{name}/down", path: "/services/pager/down", status: accept, operatorOnly: true,
		badPath: "/services/ghost/down", problem: "not_found", code: http.StatusNotFound},

	// ── traffic ──
	{method: get, route: "/traffic", status: ok},
	{method: post, route: "/traffic/start", body: `{"profile":"steady","rps":10}`, status: accept, operatorOnly: true,
		badBody: `{"profile":"ghost","rps":10}`, problem: "invalid_input", code: http.StatusBadRequest},
	{method: post, route: "/traffic/stop", status: accept, operatorOnly: true},

	// ── runs ──
	{method: get, route: "/runs", status: ok, prepare: oneFinishedRun},
	{method: get, route: "/runs/{id}", path: "/runs/run-1", status: ok, prepare: oneFinishedRun,
		badPath: "/runs/ghost", problem: "not_found", code: http.StatusNotFound},
	{method: get, route: "/runs/{id}/logs", path: "/runs/run-1/logs", status: ok, prepare: oneFinishedRun,
		badPath: "/runs/ghost/logs", problem: "not_found", code: http.StatusNotFound},
	{method: post, route: "/runs/{id}/cancel", path: "/runs/run-1/cancel", status: ok, operatorOnly: true, prepare: oneRunningRun,
		badPath: "/runs/ghost/cancel", problem: "not_found", code: http.StatusNotFound},

	// ── runtimes ──
	{method: get, route: "/runtimes", status: ok},
	{method: post, route: "/runtimes/{name}/activate", path: "/runtimes/k3d/activate", status: accept, operatorOnly: true,
		badPath: "/runtimes/ghost/activate", problem: "not_found", code: http.StatusNotFound},
	{method: post, route: "/runtimes/{name}/deactivate", path: "/runtimes/k3d/deactivate", status: accept, operatorOnly: true,
		badPath: "/runtimes/ghost/deactivate", problem: "not_found", code: http.StatusNotFound},
}

// dockerWith4GB makes the docker CLI report a 2-CPU, 4 GiB engine.
func dockerWith4GB(_ *testing.T, s *Server) string {
	s.docker = toolchain.NewFake().
		WhenArgsContain("cat /proc/meminfo", "MemTotal: 4194304 kB\nMemAvailable: 2146304 kB\n", 0).
		WhenArgsContain("info", "2 4294967296\n", 0).
		WhenArgsContain("ps -q", "server-0\n", 0)
	return ""
}

// injectDemoFault leaves the demo fault active.
func injectDemoFault(t *testing.T, s *Server) string {
	t.Helper()
	w := do(s, post, "/incidents/demo-fault/inject?app=demo-app", "", nil)
	waitForJobs(t, s)
	if w.Code >= http.StatusBadRequest {
		t.Fatalf("injecting the demo fault = %d: %s", w.Code, w.Body)
	}
	return ""
}

// startDemoChallenge leaves the demo challenge running.
func startDemoChallenge(t *testing.T, s *Server) string {
	t.Helper()
	if _, err := challengeEngine(s).Start(labChallenge, false); err != nil {
		t.Fatalf("starting the demo challenge: %v", err)
	}
	return ""
}

// startDemoPath leaves the demo learning path started.
func startDemoPath(t *testing.T, s *Server) string {
	t.Helper()
	w := do(s, post, "/learn/paths/demo-path/start", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("starting the demo path = %d: %s", w.Code, w.Body)
	}
	return ""
}

// playedLab leaves a lab with history: an incident injected and resolved, the
// demo challenge passed, and the demo path's one module completed, all by the
// user "learner".
func playedLab(t *testing.T, s *Server) string {
	t.Helper()
	t.Setenv("USER", "learner")
	steps := []struct{ method, path, body string }{
		{post, "/incidents/" + labFault + "/inject?app=" + labApp, ""},
		{post, "/incidents/resolve", ""},
		{post, "/learn/paths/" + labPath + "/start", ""},
		{post, "/learn/paths/" + labPath + "/complete", `{"moduleIdx":0}`},
	}
	for _, step := range steps {
		w := do(s, step.method, step.path, step.body, nil)
		waitForJobs(t, s)
		if w.Code >= http.StatusBadRequest {
			t.Fatalf("%s %s = %d: %s", step.method, step.path, w.Code, w.Body)
		}
	}
	startDemoChallenge(t, s)
	if w := do(s, post, "/challenges/complete", "", nil); w.Code != http.StatusOK {
		t.Fatalf("passing the demo challenge = %d: %s", w.Code, w.Body)
	}
	return ""
}

// takeSnapshot leaves a snapshot named before-drill.
func takeSnapshot(t *testing.T, s *Server) string {
	t.Helper()
	w := do(s, post, "/lab/snapshots/before-drill", "", nil)
	waitForJobs(t, s)
	if w.Code >= http.StatusBadRequest {
		t.Fatalf("taking a snapshot = %d: %s", w.Code, w.Body)
	}
	return ""
}

// oneComparison stores a comparison report and returns the path that reads it.
func oneComparison(t *testing.T, s *Server) string {
	t.Helper()
	return "/comparisons/" + writeComparison(t, s.cfg.StateDir)
}

// storedComparison stores a comparison report, for the route that lists them.
func storedComparison(t *testing.T, s *Server) string {
	t.Helper()
	oneComparison(t, s)
	return ""
}

// oneFinishedRun stores a succeeded run named run-1 with two log lines.
func oneFinishedRun(t *testing.T, s *Server) string {
	t.Helper()
	seedRun(t, s.runStore, "run-1", "lab.up", store.StatusSucceeded)
	return ""
}

// oneRunningRun stores a run named run-1 that is still running.
func oneRunningRun(t *testing.T, s *Server) string {
	t.Helper()
	ctx := t.Context()
	now := time.Now()
	if err := s.runStore.CreateRun(ctx, store.Run{ID: "run-1", Kind: "lab.up", Target: "k3d", Status: store.StatusQueued, QueuedAt: now}); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if err := s.runStore.StartRun(ctx, "run-1", now); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	return ""
}
