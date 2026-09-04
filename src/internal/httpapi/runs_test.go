// SPDX-License-Identifier: Apache-2.0
package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"github.com/sagar2395/snowopslabs/internal/store"
)

func runsTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "runs.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return &Server{runStore: st}, st
}

// seedRun inserts a run with the given status, plus a couple of log lines.
func seedRun(t *testing.T, st *store.Store, id, kind string, status store.Status) {
	t.Helper()
	ctx := context.Background()
	now := time.Now()
	if err := st.CreateRun(ctx, store.Run{ID: id, Kind: kind, Target: "k3d", Status: store.StatusQueued, QueuedAt: now}); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if err := st.StartRun(ctx, id, now); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if _, err := st.AppendLogs(ctx, id, []store.LogLine{
		{At: now, Stream: store.StreamStdout, Text: "line one"},
		{At: now, Stream: store.StreamStdout, Text: "line two"},
	}); err != nil {
		t.Fatalf("AppendLogs: %v", err)
	}
	if status.Terminal() {
		if err := st.FinishRun(ctx, id, status, nil, "", now, time.Second); err != nil {
			t.Fatalf("FinishRun: %v", err)
		}
	}
}

func TestHandleRunsList(t *testing.T) {
	s, st := runsTestServer(t)
	seedRun(t, st, "run_a", "lab.up", store.StatusSucceeded)
	seedRun(t, st, "run_b", "lab.down", store.StatusRunning)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/runs", nil)
	w := httptest.NewRecorder()
	s.handleRunsList(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	var page struct {
		Items []runView `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v (body %s)", err, w.Body.String())
	}
	if len(page.Items) != 2 {
		t.Fatalf("got %d runs, want 2", len(page.Items))
	}
}

func TestHandleRunsList_FilterByStatus(t *testing.T) {
	s, st := runsTestServer(t)
	seedRun(t, st, "run_a", "lab.up", store.StatusSucceeded)
	seedRun(t, st, "run_b", "lab.down", store.StatusRunning)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/runs?status=running", nil)
	w := httptest.NewRecorder()
	s.handleRunsList(w, req)

	var page struct {
		Items []runView `json:"items"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &page)
	if len(page.Items) != 1 || page.Items[0].ID != "run_b" {
		t.Fatalf("status filter = %+v, want only run_b", page.Items)
	}
}

func TestHandleRunsList_BadStatus(t *testing.T) {
	s, _ := runsTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v2/runs?status=nope", nil)
	w := httptest.NewRecorder()
	s.handleRunsList(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandleRunGet_WithSteps(t *testing.T) {
	s, st := runsTestServer(t)
	seedRun(t, st, "run_a", "lab.up", store.StatusSucceeded)
	if _, err := st.StartStep(context.Background(), "run_a", "provision", time.Now()); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v2/runs/run_a", nil)
	req = mux.SetURLVars(req, map[string]string{"id": "run_a"})
	w := httptest.NewRecorder()
	s.handleRunGet(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	var got runDetailView
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "run_a" || len(got.Steps) != 1 || got.Steps[0].Name != "provision" {
		t.Fatalf("detail = %+v, want run_a with one 'provision' step", got)
	}
}

func TestHandleRunGet_NotFound(t *testing.T) {
	s, _ := runsTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v2/runs/ghost", nil)
	req = mux.SetURLVars(req, map[string]string{"id": "ghost"})
	w := httptest.NewRecorder()
	s.handleRunGet(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestHandleRunLogs_CursorForward(t *testing.T) {
	s, st := runsTestServer(t)
	seedRun(t, st, "run_a", "lab.up", store.StatusSucceeded)

	// First read from the start returns both lines and a non-zero nextAfter.
	req := httptest.NewRequest(http.MethodGet, "/api/v2/runs/run_a/logs", nil)
	req = mux.SetURLVars(req, map[string]string{"id": "run_a"})
	w := httptest.NewRecorder()
	s.handleRunLogs(w, req)

	var first runLogsView
	if err := json.Unmarshal(w.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Lines) != 2 || first.NextAfter == 0 {
		t.Fatalf("first read = %+v, want 2 lines and a cursor", first)
	}

	// Reading from the cursor returns nothing more, and Done is true (terminal).
	req2 := httptest.NewRequest(http.MethodGet, "/api/v2/runs/run_a/logs?after="+itoa(first.NextAfter), nil)
	req2 = mux.SetURLVars(req2, map[string]string{"id": "run_a"})
	w2 := httptest.NewRecorder()
	s.handleRunLogs(w2, req2)

	var second runLogsView
	if err := json.Unmarshal(w2.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if len(second.Lines) != 0 || !second.Done {
		t.Fatalf("second read = %+v, want no lines and done=true", second)
	}
}

func TestHandleRunLogs_BadCursor(t *testing.T) {
	s, st := runsTestServer(t)
	seedRun(t, st, "run_a", "lab.up", store.StatusSucceeded)
	req := httptest.NewRequest(http.MethodGet, "/api/v2/runs/run_a/logs?after=-5", nil)
	req = mux.SetURLVars(req, map[string]string{"id": "run_a"})
	w := httptest.NewRecorder()
	s.handleRunLogs(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandleRunCancel(t *testing.T) {
	s, st := runsTestServer(t)
	seedRun(t, st, "run_a", "lab.up", store.StatusRunning)

	req := httptest.NewRequest(http.MethodPost, "/api/v2/runs/run_a/cancel", nil)
	req = mux.SetURLVars(req, map[string]string{"id": "run_a"})
	w := httptest.NewRecorder()
	s.handleRunCancel(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	rec, _ := st.GetRun(context.Background(), "run_a")
	if rec.Status != store.StatusCancelled {
		t.Fatalf("run status = %s, want cancelled", rec.Status)
	}
}

func TestHandleRunCancel_AlreadyFinished(t *testing.T) {
	s, st := runsTestServer(t)
	seedRun(t, st, "run_a", "lab.up", store.StatusSucceeded)

	req := httptest.NewRequest(http.MethodPost, "/api/v2/runs/run_a/cancel", nil)
	req = mux.SetURLVars(req, map[string]string{"id": "run_a"})
	w := httptest.NewRecorder()
	s.handleRunCancel(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
}

func TestRunEndpoints_ServiceUnavailableWithoutStore(t *testing.T) {
	s := &Server{} // no runStore
	req := httptest.NewRequest(http.MethodGet, "/api/v2/runs", nil)
	w := httptest.NewRecorder()
	s.handleRunsList(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
