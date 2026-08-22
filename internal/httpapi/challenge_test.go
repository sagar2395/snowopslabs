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

const challengeTestYAML = `
name: test-challenge
displayName: "Test Challenge"
description: "A test challenge"
category: workload
parTime: "10m"
setup:
  type: incident
  ref: bad-deploy-rollout
grading:
  useDetectionCheck: true
hintPenalty: 5
`

func newChallengeServer(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()
	cDir := filepath.Join(root, "challenges", "test-challenge")
	if err := os.MkdirAll(cDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cDir, "challenge.yaml"), []byte(challengeTestYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	return &Server{
		cfg: &config.Config{ProjectRoot: root, DomainSuffix: "k3d.local"},
	}
}

func TestHandleListChallenges(t *testing.T) {
	s := newChallengeServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/challenges", nil)
	w := httptest.NewRecorder()
	s.handleListChallenges(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d, body: %s", w.Code, w.Body)
	}
	if w.Body.String() == "" {
		t.Error("expected non-empty body")
	}
}

func TestHandleChallengeInfo_Found(t *testing.T) {
	s := newChallengeServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/challenges/test-challenge", nil)
	req = setVars(req, map[string]string{"name": "test-challenge"})
	w := httptest.NewRecorder()
	s.handleChallengeInfo(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d, body: %s", w.Code, w.Body)
	}
}

func TestHandleChallengeInfo_NotFound(t *testing.T) {
	s := newChallengeServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/challenges/nonexistent", nil)
	req = setVars(req, map[string]string{"name": "nonexistent"})
	w := httptest.NewRecorder()
	s.handleChallengeInfo(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHandleChallengeStatus_NoActive(t *testing.T) {
	s := newChallengeServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/challenges/status", nil)
	w := httptest.NewRecorder()
	s.handleChallengeStatus(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if body == "" {
		t.Error("expected JSON body")
	}
}

func TestHandleChallengeHistory_Empty(t *testing.T) {
	s := newChallengeServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/challenges/history", nil)
	w := httptest.NewRecorder()
	s.handleChallengeHistory(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
}
