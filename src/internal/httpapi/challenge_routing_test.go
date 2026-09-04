// SPDX-License-Identifier: Apache-2.0
package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The literal challenge routes must be registered before the "/{name}" wildcard.
// gorilla/mux matches in registration order, so if "/challenges/{name}" comes
// first it swallows "/challenges/status" and "/challenges/history" (name=
// "status"/"history"), the info lookup fails, and the tab 404s. This exercises
// the real router (not the handlers directly) so the ordering is actually tested.
func TestChallengeRoutes_LiteralsNotShadowedByWildcard(t *testing.T) {
	s := newChallengeServer(t)
	s.setupRoutes()

	for _, path := range []string{"/api/v2/challenges/status", "/api/v2/challenges/history"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)
		if w.Code == http.StatusNotFound {
			t.Errorf("GET %s returned 404 — the /{name} wildcard is shadowing the literal route", path)
			continue
		}
		if w.Code != http.StatusOK {
			t.Errorf("GET %s: got %d, want 200", path, w.Code)
		}
	}
}
