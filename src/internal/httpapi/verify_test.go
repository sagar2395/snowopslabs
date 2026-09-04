// SPDX-License-Identifier: Apache-2.0
package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/sagar2395/snowopslabs/internal/config"
	"github.com/sagar2395/snowopslabs/internal/scenario"
)

// newScenarioServer builds a Server whose scenario engine scans a temp
// project root containing the given scenario.yaml contents.
func newScenarioServer(t *testing.T, scenarios map[string]string) *Server {
	t.Helper()
	root := t.TempDir()
	for name, yaml := range scenarios {
		dir := filepath.Join(root, "scenarios", name)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "scenario.yaml"), []byte(yaml), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return &Server{
		cfg:    &config.Config{ProjectRoot: root, DomainSuffix: "k3d.local", MonitoringNamespace: "monitoring"},
		scenes: scenario.NewEngine(root, "k3d.local", "k3d"),
	}
}

func TestHandleScenarioVerify_InvalidName(t *testing.T) {
	s := &Server{}
	for _, bad := range []string{"../secret", "name with spaces", ""} {
		req := httptest.NewRequest(http.MethodPost, "/api/scenarios/x/verify", nil)
		req = setVars(req, map[string]string{"name": bad})
		w := httptest.NewRecorder()
		s.handleScenarioVerify(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("handleScenarioVerify(%q): got %d, want 400", bad, w.Code)
		}
	}
}

func TestHandleScenarioVerify_NotFound(t *testing.T) {
	s := newScenarioServer(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/scenarios/ghost/verify", nil)
	req = setVars(req, map[string]string{"name": "ghost"})
	w := httptest.NewRecorder()
	s.handleScenarioVerify(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404", w.Code)
	}
}

func TestHandleScenarioVerify_NoChecks(t *testing.T) {
	s := newScenarioServer(t, map[string]string{
		"plain": "name: plain\ndisplayName: Plain\ncategory: testing\ncomponents: []\n",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/scenarios/plain/verify", nil)
	req = setVars(req, map[string]string{"name": "plain"})
	w := httptest.NewRecorder()
	s.handleScenarioVerify(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", w.Code)
	}
	var resp problemDetail
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding error response: %v", err)
	}
	if resp.Type != problemType("no_checks") {
		t.Errorf("problem type: got %q, want %q", resp.Type, problemType("no_checks"))
	}
}

func TestHandleScenarioVerify_RunsChecksAndReportsResults(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthy" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer backend.Close()

	yaml := fmt.Sprintf(`name: checked
displayName: Checked
category: testing
components: []
checks:
  - name: good
    type: http
    url: "%s/healthy"
    timeoutSeconds: 2
  - name: bad
    type: http
    url: "%s/broken"
    timeoutSeconds: 2
`, backend.URL, backend.URL)

	s := newScenarioServer(t, map[string]string{"checked": yaml})
	req := httptest.NewRequest(http.MethodPost, "/api/scenarios/checked/verify", nil)
	req = setVars(req, map[string]string{"name": "checked"})
	w := httptest.NewRecorder()
	s.handleScenarioVerify(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (body: %s)", w.Code, w.Body.String())
	}

	var resp struct {
		Scenario string `json:"scenario"`
		Passed   bool   `json:"passed"`
		Results  []struct {
			Name string `json:"name"`
			Pass bool   `json:"pass"`
		} `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if resp.Passed {
		t.Error("passed must be false when any check fails")
	}
	if len(resp.Results) != 2 {
		t.Fatalf("results: got %d, want 2", len(resp.Results))
	}
	if !resp.Results[0].Pass || resp.Results[1].Pass {
		t.Errorf("expected [pass, fail], got [%v, %v]", resp.Results[0].Pass, resp.Results[1].Pass)
	}
}
