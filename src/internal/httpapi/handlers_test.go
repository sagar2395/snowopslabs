// SPDX-License-Identifier: Apache-2.0
package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/sagar2395/snowopslabs/internal/executor"
)

func TestIsValidName(t *testing.T) {
	valid := []string{
		"go-api", "echo-server", "my_app", "app123", "a",
		"observability-sre", "chaos-engineering",
		strings.Repeat("a", 64),
	}
	for _, name := range valid {
		if !isValidName(name) {
			t.Errorf("isValidName(%q) = false, want true", name)
		}
	}

	invalid := []string{
		"",
		"../go-api",
		"app;rm -rf /",
		"app name",
		"a/b",
		"app$(whoami)",
		strings.Repeat("a", 65),
		"app\x00null",
	}
	for _, name := range invalid {
		if isValidName(name) {
			t.Errorf("isValidName(%q) = true, want false", name)
		}
	}
}

// TestPlatformComponentDetail_EmptyMarshalsAsArrays guards the platform details
// view: the UI reads .provides.length etc., so a nil slice serialized as JSON
// null would crash it. The handler routes empty lists through nonNilStrings (and
// seeds usedInScenarios non-nil), so the payload must carry [] everywhere.
func TestPlatformComponentDetail_EmptyMarshalsAsArrays(t *testing.T) {
	detail := PlatformComponentDetail{
		Provides:        nonNilStrings(nil),
		Ports:           nonNilStrings(nil),
		Dependencies:    nonNilStrings(nil),
		Resources:       nonNilStrings(nil),
		InstallCommands: nonNilStrings(nil),
		UsedInScenarios: []ScenarioRef{},
	}
	b, err := json.Marshal(detail)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(b)
	for _, field := range []string{"provides", "ports", "dependencies", "resources", "installCommands", "usedInScenarios"} {
		if strings.Contains(body, `"`+field+`":null`) {
			t.Errorf("%q marshaled as null; want []: %s", field, body)
		}
	}
}

// setVars injects gorilla/mux path variables into a request for handler testing.
func setVars(r *http.Request, vars map[string]string) *http.Request {
	return mux.SetURLVars(r, vars)
}

func TestHandleAppDeploy_InvalidName(t *testing.T) {
	s := &Server{}
	for _, bad := range []string{"../../../etc/passwd", "app;rm", ""} {
		req := httptest.NewRequest(http.MethodPost, "/api/apps/x/deploy", nil)
		req = setVars(req, map[string]string{"name": bad})
		w := httptest.NewRecorder()
		s.handleAppDeploy(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("handleAppDeploy(%q): got %d, want 400", bad, w.Code)
		}
	}
}

func TestHandleScenarioUp_InvalidName(t *testing.T) {
	s := &Server{}
	for _, bad := range []string{"../secret", "name with spaces", ""} {
		req := httptest.NewRequest(http.MethodPost, "/api/scenarios/x/up", nil)
		req = setVars(req, map[string]string{"name": bad})
		w := httptest.NewRecorder()
		s.handleScenarioUp(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("handleScenarioUp(%q): got %d, want 400", bad, w.Code)
		}
	}
}

func TestHandleComponentUp_InvalidName(t *testing.T) {
	s := &Server{}
	cases := []struct{ cat, name string }{
		{"../etc", "traefik"},
		{"ingress", "tra;efik"},
		{"", "traefik"},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodPost, "/api/platform/component/x/y/up", nil)
		req = setVars(req, map[string]string{"category": c.cat, "name": c.name})
		w := httptest.NewRecorder()
		s.handleComponentUp(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("handleComponentUp(%q/%q): got %d, want 400", c.cat, c.name, w.Code)
		}
	}
}

// ---------------------------------------------------------------------------
// Structured error response tests (task 022)
// ---------------------------------------------------------------------------

func TestRespondError_HasCodeField(t *testing.T) {
	w := httptest.NewRecorder()
	// No version tag on the request → the backward-compatible v1 {error,code}
	// envelope, which is what this test asserts.
	r := httptest.NewRequest(http.MethodGet, "/api/apps", nil)
	respondError(w, r, http.StatusBadRequest, "invalid_input", "bad value")

	if w.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusBadRequest)
	}

	var body problemDetail
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Type != problemType("invalid_input") {
		t.Errorf("type: got %q, want %q", body.Type, problemType("invalid_input"))
	}
	if body.Detail != "bad value" {
		t.Errorf("detail: got %q, want %q", body.Detail, "bad value")
	}
}

func TestHandleAppDeploy_InvalidName_HasCode(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodPost, "/api/apps/x/deploy", nil)
	req = setVars(req, map[string]string{"name": "../evil"})
	w := httptest.NewRecorder()
	s.handleAppDeploy(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusBadRequest)
	}
	var body problemDetail
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Type != problemType("invalid_input") {
		t.Errorf("type: got %q, want %q", body.Type, problemType("invalid_input"))
	}
}

// ---------------------------------------------------------------------------
// Job ID in 202 response tests (task 017)
// ---------------------------------------------------------------------------

func TestHandleAppBuild_AcceptedIncludesJobID(t *testing.T) {
	exec := executor.New(t.TempDir())
	s := &Server{exec: exec}

	req := httptest.NewRequest(http.MethodPost, "/api/apps/go-api/build", nil)
	req = setVars(req, map[string]string{"name": "go-api"})
	w := httptest.NewRecorder()
	s.handleAppBuild(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusAccepted)
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["jobId"] == "" {
		t.Error("expected non-empty jobId in 202 response")
	}
	if body["status"] != "accepted" {
		t.Errorf("status field: got %q, want %q", body["status"], "accepted")
	}
}

func TestHandleAppDeploy_AcceptedIncludesJobID(t *testing.T) {
	exec := executor.New(t.TempDir())
	s := &Server{exec: exec}

	req := httptest.NewRequest(http.MethodPost, "/api/apps/go-api/deploy", nil)
	req = setVars(req, map[string]string{"name": "go-api"})
	w := httptest.NewRecorder()
	s.handleAppDeploy(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusAccepted)
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["jobId"] == "" {
		t.Error("expected non-empty jobId in 202 response")
	}
}

func TestAppURL(t *testing.T) {
	cases := []struct {
		name, app, domain string
		deployed          bool
		want              string
	}{
		{"deployed with domain", "echo-server", "k3d.local", true, "http://echo-server.k3d.local"},
		{"not deployed", "echo-server", "k3d.local", false, ""},
		{"no domain suffix", "echo-server", "", true, ""},
		{"go-api", "go-api", "kind.local", true, "http://go-api.kind.local"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := appURL(c.app, c.domain, c.deployed); got != c.want {
				t.Errorf("appURL(%q,%q,%v) = %q, want %q", c.app, c.domain, c.deployed, got, c.want)
			}
		})
	}
}
