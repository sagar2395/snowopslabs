// SPDX-License-Identifier: Apache-2.0
package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sagar2395/snowopslabs/internal/auth"
)

// authedServer builds an auth-enabled Server with one operator (alice/pw) and
// one participant (bob/pw).
func authedServer(t *testing.T) *Server {
	t.Helper()
	hash, err := auth.HashPassword("pw")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "users.yaml")
	content := "users:\n" +
		"  - name: alice\n    role: operator\n    passwordHash: " + hash + "\n" +
		"  - name: bob\n    role: participant\n    passwordHash: " + hash + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := auth.LoadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	return &Server{
		authEnabled: true,
		users:       store,
		sessions:    auth.NewSessionStore(0),
	}
}

func login(t *testing.T, s *Server, user, pass string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(loginRequest{Username: user, Password: pass})
	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(string(body)))
	w := httptest.NewRecorder()
	s.handleAuthLogin(w, r)
	return w
}

func sessionCookie(w *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.CookieName {
			return c
		}
	}
	return nil
}

func TestHandleAuthMe(t *testing.T) {
	t.Run("auth disabled", func(t *testing.T) {
		s := &Server{authEnabled: false}
		r := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
		w := httptest.NewRecorder()
		s.handleAuthMe(w, r)

		var resp authMeResponse
		json.Unmarshal(w.Body.Bytes(), &resp)
		if resp.AuthEnabled || !resp.Authenticated {
			t.Errorf("auth off: got %+v, want authEnabled=false authenticated=true", resp)
		}
	})

	t.Run("auth on, no session", func(t *testing.T) {
		s := authedServer(t)
		r := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
		w := httptest.NewRecorder()
		s.handleAuthMe(w, r)

		var resp authMeResponse
		json.Unmarshal(w.Body.Bytes(), &resp)
		if !resp.AuthEnabled || resp.Authenticated {
			t.Errorf("no session: got %+v, want authEnabled=true authenticated=false", resp)
		}
	})

	t.Run("auth on, with session", func(t *testing.T) {
		s := authedServer(t)
		sess, _ := s.sessions.Create("alice", auth.RoleOperator)
		r := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
		r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sess.Token})
		w := httptest.NewRecorder()
		s.handleAuthMe(w, r)

		var resp authMeResponse
		json.Unmarshal(w.Body.Bytes(), &resp)
		if resp.User != "alice" || resp.Role != auth.RoleOperator || !resp.Authenticated {
			t.Errorf("with session: got %+v", resp)
		}
	})
}

func TestHandleAuthLogin(t *testing.T) {
	t.Run("valid credentials set a cookie", func(t *testing.T) {
		s := authedServer(t)
		w := login(t, s, "alice", "pw")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		c := sessionCookie(w)
		if c == nil || c.Value == "" {
			t.Fatal("expected a session cookie")
		}
		if !c.HttpOnly {
			t.Error("session cookie must be HttpOnly")
		}
		if _, ok := s.sessions.Get(c.Value); !ok {
			t.Error("session should exist server-side after login")
		}
	})

	tests := []struct {
		name     string
		user     string
		pass     string
		wantCode int
	}{
		{"wrong password", "alice", "nope", http.StatusUnauthorized},
		{"unknown user", "mallory", "pw", http.StatusUnauthorized},
		{"missing password", "alice", "", http.StatusBadRequest},
		{"missing username", "", "pw", http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := authedServer(t)
			w := login(t, s, tt.user, tt.pass)
			if w.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", w.Code, tt.wantCode)
			}
		})
	}

	t.Run("auth disabled returns 400", func(t *testing.T) {
		s := &Server{authEnabled: false}
		w := login(t, s, "alice", "pw")
		if w.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", w.Code)
		}
	})

	t.Run("malformed json returns 400", func(t *testing.T) {
		s := authedServer(t)
		r := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader("{bad"))
		w := httptest.NewRecorder()
		s.handleAuthLogin(w, r)
		if w.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", w.Code)
		}
	})
}

func TestHandleAuthLogout(t *testing.T) {
	s := authedServer(t)
	sess, _ := s.sessions.Create("alice", auth.RoleOperator)

	r := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sess.Token})
	w := httptest.NewRecorder()
	s.handleAuthLogout(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if _, ok := s.sessions.Get(sess.Token); ok {
		t.Error("session should be deleted after logout")
	}
	c := sessionCookie(w)
	if c == nil || c.MaxAge >= 0 {
		t.Error("logout should clear the cookie (MaxAge < 0)")
	}
}

func TestAuthMiddleware(t *testing.T) {
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	t.Run("auth disabled is pass-through", func(t *testing.T) {
		s := &Server{authEnabled: false}
		r := httptest.NewRequest(http.MethodPost, "/api/platform/up", nil)
		w := httptest.NewRecorder()
		s.authMiddleware(okHandler).ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200 (pass-through)", w.Code)
		}
	})

	t.Run("unauthenticated protected route is 401", func(t *testing.T) {
		s := authedServer(t)
		r := httptest.NewRequest(http.MethodGet, "/api/status", nil)
		w := httptest.NewRecorder()
		s.authMiddleware(okHandler).ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", w.Code)
		}
	})

	t.Run("auth endpoints are always open", func(t *testing.T) {
		s := authedServer(t)
		r := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
		w := httptest.NewRecorder()
		called := false
		s.authMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })).ServeHTTP(w, r)
		if !called {
			t.Error("auth endpoint should bypass the gate")
		}
	})

	t.Run("participant denied operator route", func(t *testing.T) {
		s := authedServer(t)
		sess, _ := s.sessions.Create("bob", auth.RoleParticipant)
		r := httptest.NewRequest(http.MethodPost, "/api/platform/up", nil)
		r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sess.Token})
		w := httptest.NewRecorder()
		s.authMiddleware(okHandler).ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", w.Code)
		}
	})

	t.Run("participant allowed challenge route", func(t *testing.T) {
		s := authedServer(t)
		sess, _ := s.sessions.Create("bob", auth.RoleParticipant)
		r := httptest.NewRequest(http.MethodPost, "/api/challenges/complete", nil)
		r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sess.Token})
		w := httptest.NewRecorder()
		s.authMiddleware(okHandler).ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
		}
	})

	t.Run("operator allowed operator route and gets session in context", func(t *testing.T) {
		s := authedServer(t)
		sess, _ := s.sessions.Create("alice", auth.RoleOperator)
		r := httptest.NewRequest(http.MethodPost, "/api/platform/up", nil)
		r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sess.Token})
		w := httptest.NewRecorder()
		var gotUser string
		s.authMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			gotUser = auth.UserFromContext(r.Context())
		})).ServeHTTP(w, r)
		if gotUser != "alice" {
			t.Errorf("context user = %q, want alice", gotUser)
		}
	})
}

func TestActingUser(t *testing.T) {
	t.Run("falls back to OS user without session", func(t *testing.T) {
		t.Setenv("USER", "osuser")
		r := httptest.NewRequest(http.MethodPost, "/api/challenges/complete", nil)
		if got := actingUser(r); got != "osuser" {
			t.Errorf("actingUser = %q, want osuser", got)
		}
	})

	t.Run("uses session user when present", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/api/challenges/complete", nil)
		r = r.WithContext(auth.WithSession(r.Context(), auth.Session{User: "alice", Role: auth.RoleOperator}))
		if got := actingUser(r); got != "alice" {
			t.Errorf("actingUser = %q, want alice", got)
		}
	})
}
