// SPDX-License-Identifier: Apache-2.0
package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sagar2395/snowopslabs/internal/config"
	"github.com/sagar2395/snowopslabs/internal/lab"
	"github.com/sagar2395/snowopslabs/internal/platform"
	"github.com/sagar2395/snowopslabs/internal/scenario"
)

func newLabServer(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()
	return &Server{
		cfg:      &config.Config{ProjectRoot: root, DomainSuffix: "k3d.local", Profile: "k3d"},
		registry: platform.NewRegistry(root),
		scenes:   scenario.NewEngine(root, "k3d.local", "k3d"),
	}
}

func TestHandleLabSnapshotTake_InvalidName(t *testing.T) {
	s := &Server{}
	for _, bad := range []string{"../escape", "name with spaces", ""} {
		req := httptest.NewRequest(http.MethodPost, "/api/lab/snapshots/x", nil)
		req = setVars(req, map[string]string{"name": bad})
		w := httptest.NewRecorder()
		s.handleLabSnapshotTake(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("take(%q): got %d, want 400", bad, w.Code)
		}
	}
}

func TestHandleLabSnapshots_EmptyIsJSONArray(t *testing.T) {
	s := newLabServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/lab/snapshots", nil)
	w := httptest.NewRecorder()
	s.handleLabSnapshots(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", w.Code)
	}
	var snaps []lab.Snapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snaps); err != nil {
		t.Fatalf("response must be a JSON array even when empty: %v (%s)", err, w.Body.String())
	}
}

func TestHandleLabSnapshotTakeAndList(t *testing.T) {
	s := newLabServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/lab/snapshots/before-test", nil)
	req = setVars(req, map[string]string{"name": "before-test"})
	w := httptest.NewRecorder()
	s.handleLabSnapshotTake(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("take: got %d (%s)", w.Code, w.Body.String())
	}

	saved, err := lab.NewStore(s.cfg.ProjectRoot).Load("before-test")
	if err != nil {
		t.Fatalf("snapshot not persisted: %v", err)
	}
	if saved.Profile != "k3d" || time.Since(saved.TakenAt) > time.Minute {
		t.Fatalf("snapshot fields wrong: %+v", saved)
	}
}

func TestHandleLabRestore_NotFound(t *testing.T) {
	s := newLabServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/lab/snapshots/ghost/restore", nil)
	req = setVars(req, map[string]string{"name": "ghost"})
	w := httptest.NewRecorder()
	s.handleLabRestore(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404", w.Code)
	}
}

func TestHandleLabReset_RequiresConfirmation(t *testing.T) {
	s := newLabServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/lab/reset", nil)
	w := httptest.NewRecorder()
	s.handleLabReset(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("reset without confirm: got %d, want 400", w.Code)
	}
	var resp problemDetail
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Type != problemType("confirmation_required") {
		t.Errorf("problem type: got %q, want %q", resp.Type, problemType("confirmation_required"))
	}
}

func TestHandleLabSnapshotDelete(t *testing.T) {
	s := newLabServer(t)
	store := lab.NewStore(s.cfg.ProjectRoot)
	if err := store.Save(&lab.Snapshot{Name: "doomed", TakenAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/lab/snapshots/doomed", nil)
	req = setVars(req, map[string]string{"name": "doomed"})
	w := httptest.NewRecorder()
	s.handleLabSnapshotDelete(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete: got %d", w.Code)
	}
	if _, err := store.Load("doomed"); err == nil {
		t.Fatal("snapshot should be deleted")
	}

	// Deleting again → 404.
	w = httptest.NewRecorder()
	s.handleLabSnapshotDelete(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("second delete: got %d, want 404", w.Code)
	}
}
