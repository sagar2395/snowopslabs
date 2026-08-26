// SPDX-License-Identifier: Apache-2.0
package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHandleAppDestroy_InvalidName verifies that handleAppDestroy returns 400
// for invalid app names.
func TestHandleAppDestroy_InvalidName(t *testing.T) {
	tests := []struct {
		name string
		bad  string
	}{
		{"path traversal", "../../../etc/passwd"},
		{"semicolon injection", "app;rm"},
		{"empty", ""},
		{"spaces", "app name"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{}
			req := httptest.NewRequest(http.MethodPost, "/api/apps/x/destroy", nil)
			req = setVars(req, map[string]string{"name": tt.bad})
			w := httptest.NewRecorder()
			s.handleAppDestroy(w, req)
			if w.Code != http.StatusBadRequest {
				t.Errorf("handleAppDestroy(%q): got %d, want 400", tt.bad, w.Code)
			}
		})
	}
}

// TestHandleAppBuild_InvalidName verifies that handleAppBuild returns 400
// for invalid app names.
func TestHandleAppBuild_InvalidName(t *testing.T) {
	tests := []struct {
		name string
		bad  string
	}{
		{"path traversal", "../evil"},
		{"command injection", "$(whoami)"},
		{"empty", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{}
			req := httptest.NewRequest(http.MethodPost, "/api/apps/x/build", nil)
			req = setVars(req, map[string]string{"name": tt.bad})
			w := httptest.NewRecorder()
			s.handleAppBuild(w, req)
			if w.Code != http.StatusBadRequest {
				t.Errorf("handleAppBuild(%q): got %d, want 400", tt.bad, w.Code)
			}
		})
	}
}

// TestHandleScenarioDown_InvalidName verifies that handleScenarioDown returns
// 400 for invalid scenario names.
func TestHandleScenarioDown_InvalidName(t *testing.T) {
	tests := []struct {
		name string
		bad  string
	}{
		{"path traversal", "../secret"},
		{"spaces", "name with spaces"},
		{"empty", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{}
			req := httptest.NewRequest(http.MethodPost, "/api/scenarios/x/down", nil)
			req = setVars(req, map[string]string{"name": tt.bad})
			w := httptest.NewRecorder()
			s.handleScenarioDown(w, req)
			if w.Code != http.StatusBadRequest {
				t.Errorf("handleScenarioDown(%q): got %d, want 400", tt.bad, w.Code)
			}
		})
	}
}

// TestHandleScenarioVerify_TableInvalidNames verifies that handleScenarioVerify
// returns 400 for a range of invalid scenario names.
func TestHandleScenarioVerify_TableInvalidNames(t *testing.T) {
	tests := []struct {
		name string
		bad  string
	}{
		{"path traversal", "../secret"},
		{"slash", "a/b"},
		{"empty", ""},
		{"spaces", "name with spaces"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{}
			req := httptest.NewRequest(http.MethodPost, "/api/scenarios/x/verify", nil)
			req = setVars(req, map[string]string{"name": tt.bad})
			w := httptest.NewRecorder()
			s.handleScenarioVerify(w, req)
			if w.Code != http.StatusBadRequest {
				t.Errorf("handleScenarioVerify(%q): got %d, want 400", tt.bad, w.Code)
			}
		})
	}
}

// TestHandleServiceUp_InvalidName verifies that handleServiceUp returns 400
// for invalid service names.
func TestHandleServiceUp_InvalidName(t *testing.T) {
	tests := []struct {
		name string
		bad  string
	}{
		{"path traversal", "../evil"},
		{"injection", "redis;rm"},
		{"empty", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{}
			req := httptest.NewRequest(http.MethodPost, "/api/services/x/up", nil)
			req = setVars(req, map[string]string{"name": tt.bad})
			w := httptest.NewRecorder()
			s.handleServiceUp(w, req)
			if w.Code != http.StatusBadRequest {
				t.Errorf("handleServiceUp(%q): got %d, want 400", tt.bad, w.Code)
			}
		})
	}
}

// TestHandleServiceDown_InvalidName verifies that handleServiceDown returns
// 400 for invalid service names.
func TestHandleServiceDown_InvalidName(t *testing.T) {
	tests := []struct {
		name string
		bad  string
	}{
		{"path traversal", "../evil"},
		{"empty", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{}
			req := httptest.NewRequest(http.MethodPost, "/api/services/x/down", nil)
			req = setVars(req, map[string]string{"name": tt.bad})
			w := httptest.NewRecorder()
			s.handleServiceDown(w, req)
			if w.Code != http.StatusBadRequest {
				t.Errorf("handleServiceDown(%q): got %d, want 400", tt.bad, w.Code)
			}
		})
	}
}

// TestHandleComponentDown_InvalidName verifies that handleComponentDown returns
// 400 for invalid category or component names.
func TestHandleComponentDown_InvalidName(t *testing.T) {
	tests := []struct {
		name     string
		category string
		comp     string
	}{
		{"bad category", "../etc", "traefik"},
		{"bad component", "ingress", "tra;efik"},
		{"empty category", "", "traefik"},
		{"empty component", "ingress", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{}
			req := httptest.NewRequest(http.MethodPost, "/api/platform/component/x/y/down", nil)
			req = setVars(req, map[string]string{"category": tt.category, "name": tt.comp})
			w := httptest.NewRecorder()
			s.handleComponentDown(w, req)
			if w.Code != http.StatusBadRequest {
				t.Errorf("handleComponentDown(%q/%q): got %d, want 400", tt.category, tt.comp, w.Code)
			}
		})
	}
}

// TestHandleScenarioInfo_InvalidName verifies that handleScenarioInfo returns
// 400 for invalid scenario names.
func TestHandleScenarioInfo_InvalidName(t *testing.T) {
	tests := []struct {
		name string
		bad  string
	}{
		{"path traversal", "../../etc/passwd"},
		{"slash", "a/b"},
		{"empty", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{}
			req := httptest.NewRequest(http.MethodGet, "/api/scenarios/x", nil)
			req = setVars(req, map[string]string{"name": tt.bad})
			w := httptest.NewRecorder()
			s.handleScenarioInfo(w, req)
			if w.Code != http.StatusBadRequest {
				t.Errorf("handleScenarioInfo(%q): got %d, want 400", tt.bad, w.Code)
			}
		})
	}
}
