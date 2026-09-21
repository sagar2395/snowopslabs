// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/sagar2395/snowopslabs/internal/auth"
	"github.com/sagar2395/snowopslabs/internal/results"
)

// actingUser returns the user to record results under: the authenticated API
// user when auth is on, otherwise the OS username.
func actingUser(r *http.Request) string {
	return results.UserOr(auth.UserFromContext(r.Context()))
}

// authMiddleware enforces authentication and role-based access when auth is
// enabled. When auth is off it passes every request through.
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
			respondError(w, r, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		if !auth.Authorize(sess.Role, r.Method, r.URL.Path) {
			respondError(w, r, http.StatusForbidden, "forbidden_role",
				"this action requires the operator role")
			return
		}
		next.ServeHTTP(w, r.WithContext(auth.WithSession(r.Context(), sess)))
	})
}

// isAuthEndpoint reports whether path is one of the auth endpoints, under the
// /api or /api/v2 prefix. They must be reachable without a session so users
// can log in.
func isAuthEndpoint(path string) bool {
	for _, prefix := range []string{"/api/v2", "/api"} {
		if rest, ok := strings.CutPrefix(path, prefix); ok {
			switch rest {
			case "/auth/me", "/auth/login", "/auth/logout":
				return true
			}
		}
	}
	return false
}

// isSecureRequest reports whether the request arrived over TLS, directly
// (r.TLS) or through a TLS-terminating proxy (X-Forwarded-Proto: https). The
// session cookie gets the Secure flag only then, so it still works over plain
// HTTP on localhost. A forged header can only add Secure to the sender's own
// cookie.
func isSecureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// authMeResponse describes the current auth state for the UI.
type authMeResponse struct {
	AuthEnabled   bool   `json:"authEnabled"`
	Authenticated bool   `json:"authenticated"`
	User          string `json:"user,omitempty"`
	Role          string `json:"role,omitempty"`
}

// GET /api/v2/auth/me — reports whether auth is on and, if so, who is logged in.
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

// POST /api/v2/auth/login — verify credentials, create a session, set the cookie.
func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if !s.authEnabled {
		respondError(w, r, http.StatusBadRequest, "auth_disabled", "authentication is not enabled")
		return
	}
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid_request", "invalid JSON body")
		return
	}
	if req.Username == "" || req.Password == "" {
		respondError(w, r, http.StatusBadRequest, "invalid_request", "username and password are required")
		return
	}
	// Rate-limit by client address before the expensive password check.
	limitKey := clientAddr(r)
	if s.loginLimit != nil {
		if ok, retryAfter := s.loginLimit.allow(limitKey); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())+1))
			respondError(w, r, http.StatusTooManyRequests, "too_many_requests",
				"too many login attempts; try again later")
			return
		}
	}
	u, ok := s.users.Authenticate(req.Username, req.Password)
	if !ok {
		respondError(w, r, http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
		return
	}
	// A correct login clears the caller's rate-limit window.
	if s.loginLimit != nil {
		s.loginLimit.reset(limitKey)
	}
	sess, err := s.sessions.Create(u.Name, u.Role)
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "session_error", "could not create session")
		return
	}
	//nolint:gosec // G124: HttpOnly and SameSite=Strict always apply; Secure
	// follows the transport; see isSecureRequest.
	http.SetCookie(w, &http.Cookie{
		Name:     auth.CookieName,
		Value:    sess.Token,
		Path:     "/",
		HttpOnly: true,
		Secure:   isSecureRequest(r),
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

// POST /api/v2/auth/logout — delete the session and clear the cookie.
func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if !s.authEnabled {
		respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	s.sessions.Delete(auth.TokenFromRequest(r))
	//nolint:gosec // G124: see the login handler — Secure is conditional on TLS.
	// Clearing a cookie must repeat the attributes it was set with, or the
	// browser treats it as a different cookie and leaves the original in place.
	http.SetCookie(w, &http.Cookie{
		Name:     auth.CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   isSecureRequest(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
