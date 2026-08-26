// SPDX-License-Identifier: Apache-2.0
package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/sagar2395/snowopslabs/internal/auth"
	"github.com/sagar2395/snowopslabs/internal/results"
)

// actingUser returns the user to attribute result records to: the authenticated
// API user when auth is on, otherwise the OS username (so behaviour is unchanged
// when auth is disabled).
func actingUser(r *http.Request) string {
	return results.UserOr(auth.UserFromContext(r.Context()))
}

// authMiddleware enforces authentication and role-based access when auth is
// enabled. When disabled it is a transparent pass-through (no behaviour change).
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authEnabled {
			next.ServeHTTP(w, r)
			return
		}

		// CORS preflight and the auth endpoints themselves are always open;
		// otherwise nobody could ever log in.
		if r.Method == http.MethodOptions || isAuthEndpoint(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		sess, ok := s.sessions.Get(auth.TokenFromRequest(r))
		if !ok {
			respondError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		if !auth.Authorize(sess.Role, r.Method, r.URL.Path) {
			respondError(w, http.StatusForbidden, "forbidden_role",
				"this action requires the operator role")
			return
		}
		next.ServeHTTP(w, r.WithContext(auth.WithSession(r.Context(), sess)))
	})
}

func isAuthEndpoint(path string) bool {
	return path == "/api/auth/me" ||
		path == "/api/auth/login" ||
		path == "/api/auth/logout"
}

// authMeResponse describes the current auth state for the UI.
type authMeResponse struct {
	AuthEnabled   bool   `json:"authEnabled"`
	Authenticated bool   `json:"authenticated"`
	User          string `json:"user,omitempty"`
	Role          string `json:"role,omitempty"`
}

// GET /api/auth/me — reports whether auth is on and, if so, who is logged in.
// When auth is off it returns authEnabled:false, authenticated:true so the UI
// renders normally without a login screen.
func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	if !s.authEnabled {
		respondJSON(w, http.StatusOK, authMeResponse{AuthEnabled: false, Authenticated: true})
		return
	}
	sess, ok := s.sessions.Get(auth.TokenFromRequest(r))
	if !ok {
		respondJSON(w, http.StatusOK, authMeResponse{AuthEnabled: true, Authenticated: false})
		return
	}
	respondJSON(w, http.StatusOK, authMeResponse{
		AuthEnabled:   true,
		Authenticated: true,
		User:          sess.User,
		Role:          sess.Role,
	})
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// POST /api/auth/login — verify credentials, create a session, set the cookie.
func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if !s.authEnabled {
		respondError(w, http.StatusBadRequest, "auth_disabled", "authentication is not enabled")
		return
	}
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_request", "invalid JSON body")
		return
	}
	if req.Username == "" || req.Password == "" {
		respondError(w, http.StatusBadRequest, "invalid_request", "username and password are required")
		return
	}
	u, ok := s.users.Authenticate(req.Username, req.Password)
	if !ok {
		respondError(w, http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
		return
	}
	sess, err := s.sessions.Create(u.Name, u.Role)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "session_error", "could not create session")
		return
	}
	// Secure is intentionally omitted: the lab UI is served over plain HTTP on
	// localhost, where a browser would never send a Secure cookie. HttpOnly and
	// SameSite=Strict are the meaningful protections in this transport.
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: see note above
		Name:     auth.CookieName,
		Value:    sess.Token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Expires:  sess.Expires,
	})
	respondJSON(w, http.StatusOK, authMeResponse{
		AuthEnabled:   true,
		Authenticated: true,
		User:          sess.User,
		Role:          sess.Role,
	})
}

// POST /api/auth/logout — delete the session and clear the cookie.
func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if !s.authEnabled {
		respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	s.sessions.Delete(auth.TokenFromRequest(r))
	// Secure omitted for the same localhost-HTTP reason as the login handler.
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: see login handler note
		Name:     auth.CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
