// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sagar2395/snowopslabs/internal/auth"
	"github.com/sagar2395/snowopslabs/internal/store"
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
	users, err := auth.LoadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	return &Server{
		authEnabled: true,
		users:       users,
		sessions:    auth.NewSessionStore(testDB(t), 0),
	}
}

// testDB opens a lab database in a temp directory for the session store.
func testDB(t *testing.T) *store.Store {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "snowops.db"))
	if err != nil {
		t.Fatalf("opening the test database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
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
		sess, _ := s.sessions.Create(t.Context(), "alice", auth.RoleOperator)
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
		if _, err := s.sessions.Get(t.Context(), c.Value); err != nil {
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
	sess, _ := s.sessions.Create(t.Context(), "alice", auth.RoleOperator)

	r := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: sess.Token})
	w := httptest.NewRecorder()
	s.handleAuthLogout(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if _, err := s.sessions.Get(t.Context(), sess.Token); !errors.Is(err, auth.ErrNoSession) {
		t.Errorf("session after logout: %v, want it deleted", err)
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
		sess, _ := s.sessions.Create(t.Context(), "bob", auth.RoleParticipant)
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
		sess, _ := s.sessions.Create(t.Context(), "bob", auth.RoleParticipant)
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
		sess, _ := s.sessions.Create(t.Context(), "alice", auth.RoleOperator)
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

// The session gate over persisted sessions: a session from before a restart
// still works, one whose user was removed or demoted while the server was
// down is judged by the users file, and a database that fails is a 500,
// never a way in.
func TestAuthMiddleware_PersistedSessions(t *testing.T) {
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	call := func(s *Server, method, path, token string) int {
		r := httptest.NewRequest(method, path, nil)
		if token != "" {
			r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
		}
		w := httptest.NewRecorder()
		s.authMiddleware(okHandler).ServeHTTP(w, r)
		return w.Code
	}

	t.Run("a session from before a restart is still logged in", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "snowops.db")
		open := func() *store.Store {
			db, err := store.Open(t.Context(), path)
			if err != nil {
				t.Fatalf("opening %s: %v", path, err)
			}
			t.Cleanup(func() { _ = db.Close() })
			return db
		}
		db := open()
		before := authedServer(t)
		before.sessions = auth.NewSessionStore(db, 0)
		c := sessionCookie(login(t, before, "alice", "pw"))
		if c == nil {
			t.Fatal("login set no cookie")
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}

		after := authedServer(t)
		after.sessions = auth.NewSessionStore(open(), 0)
		if got := call(after, http.MethodPost, "/api/v2/platform/up", c.Value); got != http.StatusOK {
			t.Errorf("status after restart = %d, want 200", got)
		}
	})

	t.Run("a removed user's session is refused and deleted", func(t *testing.T) {
		s := authedServer(t)
		sess, _ := s.sessions.Create(t.Context(), "carol", auth.RoleOperator)
		if got := call(s, http.MethodGet, "/api/v2/status", sess.Token); got != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", got)
		}
		if _, err := s.sessions.Get(t.Context(), sess.Token); !errors.Is(err, auth.ErrNoSession) {
			t.Errorf("the removed user's session is still stored: %v", err)
		}
	})

	t.Run("the role comes from the users file, not the session", func(t *testing.T) {
		s := authedServer(t)
		sess, _ := s.sessions.Create(t.Context(), "bob", auth.RoleOperator)
		if got := call(s, http.MethodPost, "/api/v2/platform/up", sess.Token); got != http.StatusForbidden {
			t.Errorf("a participant with an operator session got %d, want 403", got)
		}
	})

	t.Run("a database that fails is a 500", func(t *testing.T) {
		s := authedServer(t)
		sess, _ := s.sessions.Create(t.Context(), "alice", auth.RoleOperator)
		db := testDB(t)
		s.sessions = auth.NewSessionStore(db, 0)
		_ = db.Close()
		if got := call(s, http.MethodGet, "/api/v2/status", sess.Token); got != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", got)
		}
	})

	t.Run("no database refuses login with a 500", func(t *testing.T) {
		s := authedServer(t)
		s.sessions = auth.NewSessionStore(nil, 0)
		if w := login(t, s, "alice", "pw"); w.Code != http.StatusInternalServerError || sessionCookie(w) != nil {
			t.Errorf("login with no database = %d, cookie %v; want 500 and no cookie", w.Code, sessionCookie(w))
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
