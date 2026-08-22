// SPDX-License-Identifier: Apache-2.0
package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"sync"
	"time"
)

// CookieName is the session cookie set by the server and read on each request.
const CookieName = "labctl_session"

// DefaultSessionTTL is how long a session stays valid after login.
const DefaultSessionTTL = 12 * time.Hour

// Session is a logged-in user's server-side session.
type Session struct {
	Token   string
	User    string
	Role    string
	Expires time.Time
}

// SessionStore is a concurrency-safe in-memory session table. Sessions are not
// persisted: restarting the server logs everyone out, which is acceptable for a
// lab tool (and avoids storing tokens on disk).
type SessionStore struct {
	mu    sync.Mutex
	ttl   time.Duration
	byTok map[string]Session
	now   func() time.Time // injectable for tests
}

// NewSessionStore creates a session store with the given TTL (<=0 uses the
// default).
func NewSessionStore(ttl time.Duration) *SessionStore {
	if ttl <= 0 {
		ttl = DefaultSessionTTL
	}
	return &SessionStore{
		ttl:   ttl,
		byTok: map[string]Session{},
		now:   time.Now,
	}
}

// Create issues a new session for the user/role and returns its token.
func (st *SessionStore) Create(user, role string) (Session, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return Session{}, err
	}
	tok := base64.RawURLEncoding.EncodeToString(raw)
	sess := Session{
		Token:   tok,
		User:    user,
		Role:    role,
		Expires: st.now().Add(st.ttl),
	}
	st.mu.Lock()
	st.byTok[tok] = sess
	st.mu.Unlock()
	return sess, nil
}

// Get returns the session for a token if it exists and has not expired. Expired
// sessions are removed lazily.
func (st *SessionStore) Get(token string) (Session, bool) {
	if token == "" {
		return Session{}, false
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	sess, ok := st.byTok[token]
	if !ok {
		return Session{}, false
	}
	if !st.now().Before(sess.Expires) {
		delete(st.byTok, token)
		return Session{}, false
	}
	return sess, true
}

// Delete removes a session (logout). It is a no-op for unknown tokens.
func (st *SessionStore) Delete(token string) {
	st.mu.Lock()
	delete(st.byTok, token)
	st.mu.Unlock()
}

// Count returns the number of live (non-expired) sessions.
func (st *SessionStore) Count() int {
	st.mu.Lock()
	defer st.mu.Unlock()
	n := 0
	now := st.now()
	for _, s := range st.byTok {
		if now.Before(s.Expires) {
			n++
		}
	}
	return n
}

// --- request context helpers -------------------------------------------------

type ctxKey int

const sessionKey ctxKey = 0

// WithSession returns a child context carrying the authenticated session.
func WithSession(ctx context.Context, sess Session) context.Context {
	return context.WithValue(ctx, sessionKey, sess)
}

// SessionFromContext returns the session stored on the context, if any.
func SessionFromContext(ctx context.Context) (Session, bool) {
	sess, ok := ctx.Value(sessionKey).(Session)
	return sess, ok
}

// UserFromContext returns the authenticated username, or "" when auth is off /
// no session is present. Callers fall back to results.CurrentUser() in that
// case, preserving byte-identical behaviour when auth is disabled.
func UserFromContext(ctx context.Context) string {
	if sess, ok := SessionFromContext(ctx); ok {
		return sess.User
	}
	return ""
}

// TokenFromRequest extracts a session token from the cookie or, for CLI/API
// clients, an "Authorization: Bearer <token>" header.
func TokenFromRequest(r *http.Request) string {
	if c, err := r.Cookie(CookieName); err == nil && c.Value != "" {
		return c.Value
	}
	const prefix = "Bearer "
	if h := r.Header.Get("Authorization"); len(h) > len(prefix) && h[:len(prefix)] == prefix {
		return h[len(prefix):]
	}
	return ""
}
