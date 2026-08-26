// SPDX-License-Identifier: Apache-2.0
package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/sagar2395/snowopslabs/internal/platform"
)

func reqWithOrigin(host, origin string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "http://"+host+"/api/apps/x/deploy", nil)
	r.Host = host
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	return r
}

func TestOriginAllowed(t *testing.T) {
	cases := []struct {
		name   string
		host   string
		origin string
		want   bool
	}{
		{"no origin (curl/CLI)", "localhost:3939", "", true},
		{"same host", "localhost:3939", "http://localhost:3939", true},
		{"localhost different port (vite dev)", "localhost:3939", "http://localhost:5173", true},
		{"loopback IP", "localhost:3939", "http://127.0.0.1:5173", true},
		{"same non-local host", "lab.example.com:3939", "http://lab.example.com", true},
		{"evil cross-site", "localhost:3939", "https://evil.example.com", false},
		{"malformed origin", "localhost:3939", "http://[bad", false},
	}
	for _, c := range cases {
		if got := originAllowed(reqWithOrigin(c.host, c.origin)); got != c.want {
			t.Errorf("%s: originAllowed(host=%q, origin=%q) = %v, want %v", c.name, c.host, c.origin, got, c.want)
		}
	}
}

func TestCorsMiddleware_BlocksCrossSitePOST(t *testing.T) {
	called := false
	h := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))

	r := reqWithOrigin("localhost:3939", "https://evil.example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if called {
		t.Error("handler should not be called for cross-site POST")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("status: got %d, want 403", w.Code)
	}
}

func TestCorsMiddleware_AllowsSameOriginPOST(t *testing.T) {
	called := false
	h := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))

	r := reqWithOrigin("localhost:3939", "http://localhost:3939")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if !called {
		t.Error("handler should be called for same-origin POST")
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3939" {
		t.Errorf("Allow-Origin: got %q, want the request origin", got)
	}
}

func TestCorsMiddleware_AllowsNoOriginPOST(t *testing.T) {
	called := false
	h := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))

	r := reqWithOrigin("localhost:3939", "")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if !called {
		t.Error("handler should be called when no Origin header is present")
	}
}

// makeProvider creates platform/<category...>/<name>/install.sh under root.
func makeProvider(t *testing.T, root string, segments ...string) {
	t.Helper()
	dir := filepath.Join(append([]string{root, "platform"}, segments...)...)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "install.sh"), []byte("#!/usr/bin/env bash\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestResolveCategory(t *testing.T) {
	root := t.TempDir()
	makeProvider(t, root, "ingress", "traefik")
	makeProvider(t, root, "monitoring", "metrics", "prometheus")

	s := &Server{registry: platform.NewRegistry(root)}

	if got := s.resolveCategory("ingress"); got != "ingress" {
		t.Errorf("exact match: got %q, want ingress", got)
	}
	if got := s.resolveCategory("metrics"); got != "monitoring/metrics" {
		t.Errorf("suffix match: got %q, want monitoring/metrics", got)
	}
	if got := s.resolveCategory("unknown"); got != "unknown" {
		t.Errorf("no match: got %q, want unknown (pass-through)", got)
	}
}
