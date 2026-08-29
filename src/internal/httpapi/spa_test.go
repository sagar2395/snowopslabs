// SPDX-License-Identifier: Apache-2.0
package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/sagar2395/snowopslabs/internal/config"
)

// The SPA is served so that client-side routes survive a deep link or refresh:
// a request for a path that is not a real file returns index.html (the app
// shell), while real assets are served as-is and API routes are never shadowed.
func TestSPAHandler_FallsBackToIndexForClientRoutes(t *testing.T) {
	const indexBody = "<!doctype html><title>shell</title>"
	const assetBody = "console.log('app')"
	s := &Server{
		cfg: &config.Config{ProjectRoot: t.TempDir()},
		uiFS: fstest.MapFS{
			"index.html":    {Data: []byte(indexBody)},
			"assets/app.js": {Data: []byte(assetBody)},
		},
	}
	s.setupRoutes()

	get := func(path string) (int, string) {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)
		body, _ := io.ReadAll(w.Body)
		return w.Code, string(body)
	}

	t.Run("client route falls back to index.html", func(t *testing.T) {
		code, body := get("/scenarios")
		if code != http.StatusOK {
			t.Fatalf("GET /scenarios = %d, want 200", code)
		}
		if body != indexBody {
			t.Fatalf("deep link should return the app shell, got %q", body)
		}
	})

	t.Run("nested client route also falls back", func(t *testing.T) {
		if code, body := get("/learn/paths/intro"); code != http.StatusOK || body != indexBody {
			t.Fatalf("nested route: got (%d, %q), want (200, index shell)", code, body)
		}
	})

	t.Run("real asset is served as itself", func(t *testing.T) {
		code, body := get("/assets/app.js")
		if code != http.StatusOK {
			t.Fatalf("GET /assets/app.js = %d, want 200", code)
		}
		if body != assetBody {
			t.Fatalf("asset should be served verbatim, got %q", body)
		}
	})

	t.Run("root serves index.html", func(t *testing.T) {
		if code, body := get("/"); code != http.StatusOK || body != indexBody {
			t.Fatalf("GET / : got (%d, %q), want (200, index shell)", code, body)
		}
	})
}
