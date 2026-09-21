// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// gorilla/mux matches routes in registration order, so "/challenges/status"
// and "/challenges/history" must be registered before "/challenges/{name}".
// The test goes through the real router so the order is what gets tested.
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
