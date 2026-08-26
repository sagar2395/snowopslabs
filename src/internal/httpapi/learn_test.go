// SPDX-License-Identifier: Apache-2.0
package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/sagar2395/snowopslabs/internal/config"
)

const learnTestPathYAML = `
name: test-path
displayName: "Test Path"
description: "A test path"
modules:
  - name: step-one
    displayName: "Step One"
    action:
      type: command
      ref: "labctl runtime up"
    check:
      name: cluster-up
      type: script
      script: checks/cluster-up.sh
      timeoutSeconds: 30
`

func newLearnServer(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()
	pathDir := filepath.Join(root, "learn", "test-path")
	if err := os.MkdirAll(pathDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pathDir, "path.yaml"), []byte(learnTestPathYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	return &Server{
		cfg: &config.Config{ProjectRoot: root, DomainSuffix: "k3d.local"},
	}
}

func TestHandleLearnPaths(t *testing.T) {
	s := newLearnServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/learn/paths", nil)
	w := httptest.NewRecorder()
	s.handleLearnPaths(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d, body: %s", w.Code, w.Body)
	}
	body := w.Body.String()
	if body == "" {
		t.Error("expected non-empty response body")
	}
}

func TestHandleLearnPath_NotFound(t *testing.T) {
	s := newLearnServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/learn/paths/nonexistent", nil)
	req = setVars(req, map[string]string{"name": "nonexistent"})
	w := httptest.NewRecorder()
	s.handleLearnPath(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHandleLearnPath_Found(t *testing.T) {
	s := newLearnServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/learn/paths/test-path", nil)
	req = setVars(req, map[string]string{"name": "test-path"})
	w := httptest.NewRecorder()
	s.handleLearnPath(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d, body: %s", w.Code, w.Body)
	}
}

func TestHandleLearnStart(t *testing.T) {
	s := newLearnServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/learn/paths/test-path/start", nil)
	req = setVars(req, map[string]string{"name": "test-path"})
	w := httptest.NewRecorder()
	s.handleLearnStart(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("start: status %d, body: %s", w.Code, w.Body)
	}
}

func TestHandleLearnProgress_NotStarted(t *testing.T) {
	s := newLearnServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/learn/paths/test-path/progress", nil)
	req = setVars(req, map[string]string{"name": "test-path"})
	w := httptest.NewRecorder()
	s.handleLearnProgress(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("progress: status %d, body: %s", w.Code, w.Body)
	}
	body := w.Body.String()
	if body == "" {
		t.Error("expected JSON body")
	}
}

func TestHandleLearnProgress_AfterStart(t *testing.T) {
	s := newLearnServer(t)
	// Start the path first.
	req := httptest.NewRequest(http.MethodPost, "/api/learn/paths/test-path/start", nil)
	req = setVars(req, map[string]string{"name": "test-path"})
	httptest.NewRecorder() // discard
	s.handleLearnStart(httptest.NewRecorder(), req)

	// Now check progress.
	req2 := httptest.NewRequest(http.MethodGet, "/api/learn/paths/test-path/progress", nil)
	req2 = setVars(req2, map[string]string{"name": "test-path"})
	w := httptest.NewRecorder()
	s.handleLearnProgress(w, req2)
	if w.Code != http.StatusOK {
		t.Fatalf("progress: status %d, body: %s", w.Code, w.Body)
	}
}
