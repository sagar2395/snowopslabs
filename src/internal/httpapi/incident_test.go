// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sagar2395/snowopslabs/internal/config"
	"github.com/sagar2395/snowopslabs/internal/executor"
	"github.com/sagar2395/snowopslabs/internal/incident"
	"github.com/sagar2395/snowopslabs/internal/store"
)

const apiTestFaultYAML = `name: api-fault
displayName: "API Test Fault"
description: "test"
category: workload
severity: low
target:
  namespace: go-api
  workload: go-api
prerequisites:
  apps: [go-api]
detection:
  name: marker-gone
  type: script
  script: checks/resolved.sh
  timeoutSeconds: 5
`

func newIncidentServer(t *testing.T) (*Server, string) {
	t.Helper()
	root := t.TempDir()

	appDir := filepath.Join(root, "apps", "go-api")
	os.MkdirAll(appDir, 0755)
	os.WriteFile(filepath.Join(appDir, "app.env"), []byte("APP_NAME=go-api\n"), 0644)

	dir := filepath.Join(root, "incidents", "api-fault")
	os.MkdirAll(filepath.Join(dir, "checks"), 0755)
	files := map[string]string{
		"fault.yaml":         apiTestFaultYAML,
		"inject.sh":          "#!/usr/bin/env bash\ntouch \"$(dirname \"$0\")/BROKEN\"\n",
		"resolve.sh":         "#!/usr/bin/env bash\nrm -f \"$(dirname \"$0\")/BROKEN\"\ntouch \"$(dirname \"$0\")/RESOLVED\"\n",
		"checks/resolved.sh": "#!/usr/bin/env bash\n[ ! -f \"$(dirname \"$0\")/../BROKEN\" ]\n",
		"hints.md":           "## Hint 1\nx\n## Hint 2\ny\n## Hint 3\nz\n",
		"solution.md":        "# Solution\n",
	}
	for f, content := range files {
		os.WriteFile(filepath.Join(dir, f), []byte(content), 0755)
	}

	return &Server{
		cfg:       &config.Config{ProjectRoot: root, StateDir: filepath.Join(root, ".labctl"), DomainSuffix: "k3d.local"},
		exec:      executor.New(root),
		incidents: incident.NewEngine(root, "k3d.local"),
	}, root
}

func TestHandleIncidentInject_FullLoop(t *testing.T) {
	s, root := newIncidentServer(t)

	// Inject.
	req := httptest.NewRequest(http.MethodPost, "/api/incidents/api-fault/inject", nil)
	req = setVars(req, map[string]string{"name": "api-fault"})
	w := httptest.NewRecorder()
	s.handleIncidentInject(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("inject: got %d (%s)", w.Code, w.Body.String())
	}

	// Second inject without force → 409.
	w = httptest.NewRecorder()
	s.handleIncidentInject(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("second inject: got %d, want 409", w.Code)
	}

	// Status: not resolved.
	w = httptest.NewRecorder()
	s.handleIncidentStatus(w, httptest.NewRequest(http.MethodGet, "/api/incidents/status", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"resolved":false`) {
		t.Fatalf("status: %d %s", w.Code, w.Body.String())
	}

	// Fix manually, status flips to resolved.
	if err := os.Remove(filepath.Join(root, "incidents", "api-fault", "BROKEN")); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	s.handleIncidentStatus(w, httptest.NewRequest(http.MethodGet, "/api/incidents/status", nil))
	if !strings.Contains(w.Body.String(), `"resolved":true`) {
		t.Fatalf("expected resolved, got %s", w.Body.String())
	}

	// No active incident anymore.
	w = httptest.NewRecorder()
	s.handleIncidentStatus(w, httptest.NewRequest(http.MethodGet, "/api/incidents/status", nil))
	if !strings.Contains(w.Body.String(), `"active":null`) {
		t.Fatalf("expected no active incident, got %s", w.Body.String())
	}
}

func TestHandleIncidentStatus_TidiesUpAfterAFix(t *testing.T) {
	tests := []struct {
		name         string
		fixByHand    bool
		wantResolved bool
	}{
		{name: "an unfixed fault is left alone", fixByHand: false, wantResolved: false},
		{name: "a hand fix runs resolve.sh to tidy up", fixByHand: true, wantResolved: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, root := newIncidentServer(t)
			dir := filepath.Join(root, "incidents", "api-fault")

			req := setVars(httptest.NewRequest(http.MethodPost, "/api/incidents/api-fault/inject", nil), map[string]string{"name": "api-fault"})
			s.handleIncidentInject(httptest.NewRecorder(), req)
			if tt.fixByHand {
				os.Remove(filepath.Join(dir, "BROKEN"))
			}

			w := httptest.NewRecorder()
			s.handleIncidentStatus(w, httptest.NewRequest(http.MethodGet, "/api/incidents/status", nil))
			if w.Code != http.StatusOK {
				t.Fatalf("status: %d %s", w.Code, w.Body.String())
			}
			_, err := os.Stat(filepath.Join(dir, "RESOLVED"))
			if ran := err == nil; ran != tt.wantResolved {
				t.Fatalf("resolve.sh ran = %v, want %v (body %s)", ran, tt.wantResolved, w.Body.String())
			}
		})
	}
}

func TestHandleIncidentInject_Errors(t *testing.T) {
	s, _ := newIncidentServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/incidents/x/inject", nil)
	req = setVars(req, map[string]string{"name": "../evil"})
	w := httptest.NewRecorder()
	s.handleIncidentInject(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid name: got %d", w.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/incidents/ghost/inject", nil)
	req = setVars(req, map[string]string{"name": "ghost"})
	w = httptest.NewRecorder()
	s.handleIncidentInject(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown fault: got %d", w.Code)
	}
}

func TestHandleIncidentInject_SilentHidesFault(t *testing.T) {
	s, root := newIncidentServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/incidents/api-fault/inject?silent=true", nil)
	req = setVars(req, map[string]string{"name": "api-fault"})
	w := httptest.NewRecorder()
	s.handleIncidentInject(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("inject: %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "api-fault") {
		t.Fatalf("silent inject must not reveal the fault: %s", w.Body.String())
	}

	// Status in silent mode hides the identity while unresolved.
	w = httptest.NewRecorder()
	s.handleIncidentStatus(w, httptest.NewRequest(http.MethodGet, "/api/incidents/status", nil))
	if strings.Contains(w.Body.String(), "api-fault") {
		t.Fatalf("silent status must not reveal the fault: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "(hidden)") {
		t.Fatalf("expected hidden marker: %s", w.Body.String())
	}
	for _, leak := range []string{"marker-gone", "BROKEN", "FAIL:"} {
		if strings.Contains(w.Body.String(), leak) {
			t.Fatalf("silent status leaked detection detail %q: %s", leak, w.Body.String())
		}
	}

	// A passing silent check can reveal the fault because the learner has
	// already repaired it.
	os.Remove(filepath.Join(root, "incidents", "api-fault", "BROKEN"))
	w = httptest.NewRecorder()
	s.handleIncidentStatus(w, httptest.NewRequest(http.MethodGet, "/api/incidents/status", nil))
	if !strings.Contains(w.Body.String(), `"resolved":true`) || !strings.Contains(w.Body.String(), `"name":"marker-gone"`) {
		t.Fatalf("resolved silent status did not reveal the passing check: %s", w.Body.String())
	}
}

func TestHandleIncidentResolve(t *testing.T) {
	s, _ := newIncidentServer(t)

	// Resolve with nothing active → 409.
	w := httptest.NewRecorder()
	s.handleIncidentResolve(w, httptest.NewRequest(http.MethodPost, "/api/incidents/resolve", nil))
	if w.Code != http.StatusConflict {
		t.Fatalf("resolve with nothing active: got %d", w.Code)
	}

	// Inject then resolve.
	req := httptest.NewRequest(http.MethodPost, "/api/incidents/api-fault/inject", nil)
	req = setVars(req, map[string]string{"name": "api-fault"})
	s.handleIncidentInject(httptest.NewRecorder(), req)

	w = httptest.NewRecorder()
	s.handleIncidentResolve(w, httptest.NewRequest(http.MethodPost, "/api/incidents/resolve", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("resolve: got %d (%s)", w.Code, w.Body.String())
	}
}

func TestHandleIncidentHintAndHistory(t *testing.T) {
	s, root := newIncidentServer(t)

	// Hint with nothing active → 409.
	w := httptest.NewRecorder()
	s.handleIncidentHint(w, httptest.NewRequest(http.MethodPost, "/api/incidents/hint", nil))
	if w.Code != http.StatusConflict {
		t.Fatalf("hint without active: got %d", w.Code)
	}

	// Inject, take all three hints, then exhaust them.
	req := httptest.NewRequest(http.MethodPost, "/api/incidents/api-fault/inject", nil)
	req = setVars(req, map[string]string{"name": "api-fault"})
	s.handleIncidentInject(httptest.NewRecorder(), req)

	for i := 1; i <= 3; i++ {
		w = httptest.NewRecorder()
		s.handleIncidentHint(w, httptest.NewRequest(http.MethodPost, "/api/incidents/hint", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("hint %d: got %d (%s)", i, w.Code, w.Body.String())
		}
		var h struct{ Index, Total int }
		json.Unmarshal(w.Body.Bytes(), &h)
		if h.Index != i || h.Total != 3 {
			t.Fatalf("hint %d: %+v", i, h)
		}
	}
	w = httptest.NewRecorder()
	s.handleIncidentHint(w, httptest.NewRequest(http.MethodPost, "/api/incidents/hint", nil))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "no_more_hints") {
		t.Fatalf("exhausted hints: %d %s", w.Code, w.Body.String())
	}

	// Resolve manually and read history: 1 record, 3 hints used.
	os.Remove(filepath.Join(root, "incidents", "api-fault", "BROKEN"))
	s.handleIncidentStatus(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/incidents/status", nil))

	w = httptest.NewRecorder()
	s.handleIncidentHistory(w, httptest.NewRequest(http.MethodGet, "/api/incidents/history", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("history: %d", w.Code)
	}
	var recs []struct {
		Fault      string `json:"fault"`
		HintsUsed  int    `json:"hintsUsed"`
		ResolvedBy string `json:"resolvedBy"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &recs); err != nil || len(recs) != 1 {
		t.Fatalf("history response: %v %s", err, w.Body.String())
	}
	if recs[0].HintsUsed != 3 || recs[0].ResolvedBy != "manual" {
		t.Fatalf("record: %+v", recs[0])
	}
}

func TestHandleIncidentHistory_EmptyIsJSONArray(t *testing.T) {
	s, _ := newIncidentServer(t)
	w := httptest.NewRecorder()
	s.handleIncidentHistory(w, httptest.NewRequest(http.MethodGet, "/api/incidents/history", nil))
	var recs []json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &recs); err != nil {
		t.Fatalf("must be a JSON array even when empty: %s", w.Body.String())
	}
}

func TestHandleListIncidents(t *testing.T) {
	s, _ := newIncidentServer(t)
	w := httptest.NewRecorder()
	s.handleListIncidents(w, httptest.NewRequest(http.MethodGet, "/api/incidents", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d", w.Code)
	}
	var resp struct {
		Faults []json.RawMessage `json:"faults"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || len(resp.Faults) != 1 {
		t.Fatalf("list response: %v %s", err, w.Body.String())
	}
}

// In silent mode the list never says which fault is active: the name stays on
// the server until the detection check passes.
func TestListIncidents_SilentModeHidesTheActiveFault(t *testing.T) {
	s := newContractLab(t)
	w := do(s, http.MethodPost, "/incidents/"+labFault+"/inject?app="+labApp+"&silent=true", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("silent inject = %d: %s", w.Code, w.Body)
	}

	var list struct {
		Active struct {
			Fault  string `json:"fault"`
			Silent bool   `json:"silent"`
		} `json:"active"`
	}
	body := do(s, http.MethodGet, "/incidents", "", nil).Body.Bytes()
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decoding %s: %v", body, err)
	}
	if list.Active.Fault == labFault || !list.Active.Silent {
		t.Errorf("active = %+v, want the fault's name hidden and silent=true", list.Active)
	}

	// Once it is not silent, the list names it again.
	if w := do(s, http.MethodPost, "/incidents/"+labFault+"/inject?app="+labApp+"&force=true", "", nil); w.Code != http.StatusOK {
		t.Fatalf("inject = %d: %s", w.Code, w.Body)
	}
	body = do(s, http.MethodGet, "/incidents", "", nil).Body.Bytes()
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	if list.Active.Fault != labFault {
		t.Errorf("active fault = %q for an incident that is not silent, want %q", list.Active.Fault, labFault)
	}
}

// A second inject while a silent incident is active is refused without naming
// it, on both inject routes, so nobody can read the answer out of the 409.
func TestInject_SecondInjectDoesNotNameASilentIncident(t *testing.T) {
	s := newContractLab(t)
	if w := do(s, http.MethodPost, "/incidents/"+labFault+"/inject?app="+labApp+"&silent=true", "", nil); w.Code != http.StatusOK {
		t.Fatalf("silent inject = %d: %s", w.Code, w.Body)
	}
	for _, path := range []string{"/incidents/inject-random?app=" + labApp + "&seed=3", "/incidents/" + labFault + "/inject?app=" + labApp} {
		w := do(s, http.MethodPost, path, "", nil)
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "incident_active") {
			t.Fatalf("POST %s = %d %s, want 409 incident_active", path, w.Code, w.Body)
		}
		var problem struct {
			Detail string `json:"detail"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &problem); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(problem.Detail, labFault) || !strings.Contains(problem.Detail, "not named") {
			t.Errorf("POST %s detail = %q, want it refused without naming the silent incident", path, problem.Detail)
		}
	}
}

// A silent inject from the web UI broadcasts nothing that names the fault: no
// job label and no script output.
func TestInject_SilentBroadcastsNothing(t *testing.T) {
	s := newContractLab(t)
	writeLabFile(t, filepath.Join(s.cfg.ProjectRoot, "incidents", labFault, "inject.sh"),
		script+"echo 'breaking the demo-fault selector'\n")
	events := s.exec.Broadcast.Subscribe()
	defer s.exec.Broadcast.Unsubscribe(events)

	w := do(s, http.MethodPost, "/incidents/"+labFault+"/inject?app="+labApp+"&silent=true", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("silent inject = %d: %s", w.Code, w.Body)
	}
	for {
		select {
		case ev := <-events:
			if strings.Contains(fmt.Sprintf("%+v", ev), labFault) {
				t.Fatalf("a broadcast names the silent fault: %+v", ev)
			}
		default:
			return
		}
	}
}

// The history of a silent inject withholds its steps and output.
func TestRuns_HiddenRunWithholdsItsOutput(t *testing.T) {
	s := newContractLab(t)
	now := time.Now()
	if err := s.runStore.CreateRun(t.Context(), store.Run{ID: "run-9", Kind: "incident.inject", Target: store.HiddenTarget, Status: store.StatusQueued, QueuedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.runStore.AppendLogs(t.Context(), "run-9", []store.LogLine{{At: now, Stream: store.StreamStdout, Text: "breaking demo-fault"}}); err != nil {
		t.Fatal(err)
	}

	if body := do(s, http.MethodGet, "/runs", "", nil).Body.String(); !strings.Contains(body, `"target":"(hidden)"`) {
		t.Errorf("the run list does not show the hidden target: %s", body)
	}
	detail := do(s, http.MethodGet, "/runs/run-9", "", nil)
	if !strings.Contains(detail.Body.String(), `"steps":[]`) {
		t.Errorf("the hidden run's steps were sent: %s", detail.Body)
	}
	assertProblem(t, do(s, http.MethodGet, "/runs/run-9/logs", "", nil), http.StatusForbidden, "hidden_run", apiPrefix+"/runs/run-9/logs")
}
