// SPDX-License-Identifier: Apache-2.0
package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sagar2395/snowopslabs/internal/config"
	"github.com/sagar2395/snowopslabs/internal/results"
)

func newResultsServer(t *testing.T) (*Server, *results.Store) {
	t.Helper()
	root := t.TempDir()
	s := &Server{
		cfg: &config.Config{ProjectRoot: root},
	}
	store := results.NewStore(filepath.Join(root, ".labctl", "history"))
	return s, store
}

func TestHandleResults_Empty(t *testing.T) {
	s, _ := newResultsServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/results", nil)
	w := httptest.NewRecorder()
	s.handleResults(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if w.Body.String() == "" {
		t.Error("expected non-empty body")
	}
}

func TestHandleResults_ReturnsSortedNewestFirst(t *testing.T) {
	s, store := newResultsServer(t)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	_ = store.Append(results.Record{Kind: results.KindIncident, Name: "old", StartedAt: now})
	_ = store.Append(results.Record{Kind: results.KindChallenge, Name: "new", StartedAt: now.Add(time.Hour)})
	req := httptest.NewRequest(http.MethodGet, "/api/results", nil)
	w := httptest.NewRecorder()
	s.handleResults(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	// "new" (newer) should appear before "old" in the body.
	newIdx := indexOf(body, "new")
	oldIdx := indexOf(body, "old")
	if newIdx > oldIdx {
		t.Errorf("expected newest-first ordering in response")
	}
}

func TestHandleResultsByKind_Valid(t *testing.T) {
	s, store := newResultsServer(t)
	_ = store.Append(results.Record{Kind: results.KindIncident, Name: "oom", StartedAt: time.Now()})
	_ = store.Append(results.Record{Kind: results.KindChallenge, Name: "restore", StartedAt: time.Now()})
	for _, kind := range []string{"incident", "challenge", "module"} {
		t.Run(kind, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/results/"+kind, nil)
			req = setVars(req, map[string]string{"kind": kind})
			w := httptest.NewRecorder()
			s.handleResultsByKind(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("kind=%s: status %d", kind, w.Code)
			}
		})
	}
}

func TestHandleResultsByKind_Invalid(t *testing.T) {
	s, _ := newResultsServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/results/badkind", nil)
	req = setVars(req, map[string]string{"kind": "badkind"})
	w := httptest.NewRecorder()
	s.handleResultsByKind(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleProgress_Empty(t *testing.T) {
	s, _ := newResultsServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/progress", nil)
	w := httptest.NewRecorder()
	s.handleProgress(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleProgress_WithModuleRecords(t *testing.T) {
	s, store := newResultsServer(t)
	_ = store.Append(results.Record{Kind: results.KindModule, Name: "k8s/init", StartedAt: time.Now()})
	_ = store.Append(results.Record{Kind: results.KindModule, Name: "k8s/deploy", StartedAt: time.Now()})
	req := httptest.NewRequest(http.MethodGet, "/api/progress", nil)
	w := httptest.NewRecorder()
	s.handleProgress(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if body == "" {
		t.Error("expected non-empty body")
	}
}

// TestHandleResults_StoreNotCreatedYet checks that an empty/missing store
// returns an empty JSON array, not a 500.
func TestHandleResults_StoreNotCreatedYet(t *testing.T) {
	root := t.TempDir()
	// Do NOT create .labctl/history — simulate a fresh install.
	s := &Server{cfg: &config.Config{ProjectRoot: root}}
	req := httptest.NewRequest(http.MethodGet, "/api/results", nil)
	w := httptest.NewRecorder()
	s.handleResults(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d, body: %s", w.Code, w.Body)
	}
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// Make sure we can create the results dir if tests run fresh.
func init() {
	_ = os.MkdirAll("/tmp/test-results", 0o755)
}

func TestHandleLeaderboard(t *testing.T) {
	s, store := newResultsServer(t)

	// Empty store -> 200 with an empty JSON array.
	req := httptest.NewRequest(http.MethodGet, "/api/leaderboard", nil)
	w := httptest.NewRecorder()
	s.handleLeaderboard(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if got := w.Body.String(); indexOf(got, "[") != 0 {
		t.Errorf("empty leaderboard should be a JSON array, got %q", got)
	}

	// Two users: alice outranks bob by score.
	_ = store.Append(results.Record{Kind: results.KindChallenge, User: "alice", Score: 90, Outcome: "passed"})
	_ = store.Append(results.Record{Kind: results.KindChallenge, User: "bob", Score: 40, Outcome: "passed"})

	w = httptest.NewRecorder()
	s.handleLeaderboard(w, httptest.NewRequest(http.MethodGet, "/api/leaderboard", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if indexOf(body, "alice") > indexOf(body, "bob") {
		t.Errorf("alice (90) should rank before bob (40): %s", body)
	}
}
