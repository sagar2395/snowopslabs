// SPDX-License-Identifier: Apache-2.0
package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Under /api/v2 an error must be RFC 7807 problem+json: the problem media type,
// a stable machine `type` slug, the status, a human detail, the request path as
// `instance`, and the correlation ID. This drives the real router so the version
// middleware actually tags the request.
func TestErrorEnvelope_V2IsProblemJSON(t *testing.T) {
	s := newChallengeServer(t) // authEnabled == false → /auth/login yields auth_disabled
	s.setupRoutes()

	req := httptest.NewRequest(http.MethodPost, "/api/v2/auth/login", strings.NewReader("{}"))
	req.Header.Set(requestIDHeader, "req-7807")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != problemContentType {
		t.Fatalf("content-type: got %q, want %q", ct, problemContentType)
	}

	var p problemDetail
	if err := json.NewDecoder(w.Body).Decode(&p); err != nil {
		t.Fatalf("decode problem+json: %v", err)
	}
	if p.Type != problemTypeBase+"auth_disabled" {
		t.Errorf("type: got %q, want the auth_disabled slug URI", p.Type)
	}
	if p.Title != http.StatusText(http.StatusBadRequest) {
		t.Errorf("title: got %q, want %q", p.Title, http.StatusText(http.StatusBadRequest))
	}
	if p.Status != http.StatusBadRequest {
		t.Errorf("status field: got %d, want 400", p.Status)
	}
	if p.Detail == "" {
		t.Error("detail should carry the human-readable message")
	}
	if p.Instance != "/api/v2/auth/login" {
		t.Errorf("instance: got %q, want the request path", p.Instance)
	}
	if p.RequestID != "req-7807" {
		t.Errorf("requestId: got %q, want the correlation ID echoed", p.RequestID)
	}
}

// The unversioned /api alias must keep the legacy {error,code} envelope so
// existing clients are unaffected by the v2 rollout.
func TestErrorEnvelope_V1StaysLegacy(t *testing.T) {
	s := newChallengeServer(t)
	s.setupRoutes()

	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type: got %q, want application/json (legacy)", ct)
	}
	var body ErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode legacy error: %v", err)
	}
	if body.Code != "auth_disabled" {
		t.Errorf("code: got %q, want auth_disabled", body.Code)
	}
	if body.Error == "" {
		t.Error("error message should be present")
	}
}

func TestProblemType(t *testing.T) {
	if got := problemType(""); got != "about:blank" {
		t.Errorf("empty code: got %q, want about:blank", got)
	}
	if got := problemType("not_found"); got != problemTypeBase+"not_found" {
		t.Errorf("code slug: got %q, want the namespaced URI", got)
	}
}

// The set of `type` slugs is a published contract. This test scans the handler
// sources for every slug passed to respondError and asserts it matches
// knownProblemSlugs exactly — so adding, renaming, or dropping a slug forces a
// deliberate edit to the registry (ADR 0006).
func TestProblemSlugs_StableSet(t *testing.T) {
	used := scanRespondErrorSlugs(t)

	known := make(map[string]bool, len(knownProblemSlugs))
	for _, s := range knownProblemSlugs {
		known[s] = true
	}

	for s := range used {
		if !known[s] {
			t.Errorf("slug %q is used in a handler but missing from knownProblemSlugs (add it, deliberately)", s)
		}
	}
	for s := range known {
		if !used[s] {
			t.Errorf("slug %q is in knownProblemSlugs but no longer used (remove it, deliberately)", s)
		}
	}

	// The registry must stay sorted and duplicate-free so diffs are readable.
	if !sort.StringsAreSorted(knownProblemSlugs) {
		t.Error("knownProblemSlugs must be sorted")
	}
}

// scanRespondErrorSlugs reads the package's non-test .go files and returns the
// set of code slugs (3rd argument) passed to respondError.
func scanRespondErrorSlugs(t *testing.T) map[string]bool {
	t.Helper()
	// respondError(w, r, <status>, "<slug>", ...) — capture the slug.
	re := regexp.MustCompile(`respondError\(w, r, [^,]+, "([a-z_]+)"`)
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	used := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, m := range re.FindAllStringSubmatch(string(data), -1) {
			used[m[1]] = true
		}
	}
	if len(used) == 0 {
		t.Fatal("found no respondError slugs — the scan regex is probably stale")
	}
	return used
}
