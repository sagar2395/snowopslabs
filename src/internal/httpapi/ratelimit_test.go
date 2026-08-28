// SPDX-License-Identifier: Apache-2.0
package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sagar2395/snowopslabs/internal/auth"
	"github.com/sagar2395/snowopslabs/internal/config"
)

// The always-open auth endpoints must be recognised under both the /api and
// /api/v2 prefixes — otherwise auth-enabled servers gate the login endpoint on
// v2 and nobody can obtain a session there.
func TestIsAuthEndpoint_BothPrefixes(t *testing.T) {
	open := []string{
		"/api/auth/login", "/api/auth/logout", "/api/auth/me",
		"/api/v2/auth/login", "/api/v2/auth/logout", "/api/v2/auth/me",
	}
	for _, p := range open {
		if !isAuthEndpoint(p) {
			t.Errorf("isAuthEndpoint(%q) = false, want true (must stay open)", p)
		}
	}
	gated := []string{"/api/scenarios", "/api/v2/platform/up", "/api/v2/auth", "/auth/login"}
	for _, p := range gated {
		if isAuthEndpoint(p) {
			t.Errorf("isAuthEndpoint(%q) = true, want false (must be gated)", p)
		}
	}
}

func TestLoginLimiter_BlocksAfterMaxThenRecovers(t *testing.T) {
	now := time.Unix(0, 0)
	l := newLoginLimiter(3, time.Minute)
	l.now = func() time.Time { return now }

	const key = "10.0.0.1"
	for i := 1; i <= 3; i++ {
		if ok, _ := l.allow(key); !ok {
			t.Fatalf("attempt %d should be allowed (under the cap)", i)
		}
	}
	ok, retryAfter := l.allow(key)
	if ok {
		t.Fatal("attempt over the cap should be blocked")
	}
	if retryAfter <= 0 || retryAfter > time.Minute {
		t.Errorf("retryAfter = %v, want (0, 1m]", retryAfter)
	}

	// After the window rolls over, the caller is allowed again.
	now = now.Add(time.Minute + time.Second)
	if ok, _ := l.allow(key); !ok {
		t.Error("attempt after the window expired should be allowed")
	}
}

func TestLoginLimiter_ResetClearsWindow(t *testing.T) {
	l := newLoginLimiter(2, time.Minute)
	const key = "10.0.0.2"
	l.allow(key)
	l.allow(key)
	if ok, _ := l.allow(key); ok {
		t.Fatal("third attempt should be blocked before reset")
	}
	l.reset(key) // a successful login clears the window
	if ok, _ := l.allow(key); !ok {
		t.Error("after reset the caller should be allowed again")
	}
}

func TestLoginLimiter_KeysAreIndependent(t *testing.T) {
	l := newLoginLimiter(1, time.Minute)
	if ok, _ := l.allow("a"); !ok {
		t.Fatal("first key first attempt should pass")
	}
	if ok, _ := l.allow("b"); !ok {
		t.Error("a different key must not be affected by another's window")
	}
}

// End-to-end: a credential-stuffing loop against the login endpoint is cut off
// with 429 once the per-client cap is hit — the W5 exit criterion.
func TestAuthLogin_RateLimitsCredentialStuffing(t *testing.T) {
	s := &Server{
		authEnabled: true,
		users:       auth.NewStore(), // empty: every attempt is a failed login
		sessions:    auth.NewSessionStore(0),
		loginLimit:  newLoginLimiter(5, time.Minute),
		cfg:         &config.Config{ProjectRoot: t.TempDir()},
	}
	s.setupRoutes()

	post := func() *httptest.ResponseRecorder {
		body := strings.NewReader(`{"username":"admin","password":"guess"}`)
		r := httptest.NewRequest(http.MethodPost, "/api/v2/auth/login", body)
		r.RemoteAddr = "203.0.113.9:44321"
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, r)
		return w
	}

	for i := 1; i <= 5; i++ {
		if got := post().Code; got != http.StatusUnauthorized {
			t.Fatalf("attempt %d: got %d, want 401 (still under the cap)", i, got)
		}
	}
	blocked := post()
	if blocked.Code != http.StatusTooManyRequests {
		t.Fatalf("6th attempt: got %d, want 429", blocked.Code)
	}
	if blocked.Header().Get("Retry-After") == "" {
		t.Error("429 response should carry a Retry-After header")
	}
}
