// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"github.com/snowops-oss/snowopslabs/internal/store"
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

// ErrNoSession is returned by Get for an empty, unknown, expired or deleted
// token: the caller is not logged in.
var ErrNoSession = errors.New("no valid session")

// errNoDatabase is returned when the server has no lab database to keep
// sessions in, so nobody can log in.
var errNoDatabase = errors.New("login sessions are kept in the lab database, which did not open: the server log at startup says why")

// SessionStore issues login sessions and keeps them in the lab database, so a
// restart of the server keeps everyone logged in. Only a SHA-256 hash of each
// token is stored.
type SessionStore struct {
	db  *store.Store
	ttl time.Duration
	now func() time.Time // injectable for tests
}

// NewSessionStore returns a session store backed by db, with the given TTL
// (<=0 uses the default). A nil db gives a store that refuses every login.
func NewSessionStore(db *store.Store, ttl time.Duration) *SessionStore {
	if ttl <= 0 {
		ttl = DefaultSessionTTL
	}
	return &SessionStore{db: db, ttl: ttl, now: time.Now}
}

// hashToken is the key a token is stored under.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Create issues a new session for the user and role and returns it with its
// token. Expired sessions are removed first, so the table does not grow.
func (st *SessionStore) Create(ctx context.Context, user, role string) (Session, error) {
	if st.db == nil {
		return Session{}, errNoDatabase
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return Session{}, err
	}
	now := st.now()
	if _, err := st.db.DeleteExpiredSessions(ctx, now); err != nil {
		return Session{}, err
	}
	sess := Session{
		Token:   base64.RawURLEncoding.EncodeToString(raw),
		User:    user,
		Role:    role,
		Expires: now.Add(st.ttl),
	}
	if err := st.db.InsertSession(ctx, store.Session{
		TokenHash: hashToken(sess.Token),
		User:      user,
		Role:      role,
		CreatedAt: now,
		ExpiresAt: sess.Expires,
	}); err != nil {
		return Session{}, err
	}
	return sess, nil
}

// Get returns the session for a token if it exists and has not expired, or
// ErrNoSession. An expired session is deleted when it is found.
func (st *SessionStore) Get(ctx context.Context, token string) (Session, error) {
	if token == "" {
		return Session{}, ErrNoSession
	}
	if st.db == nil {
		return Session{}, errNoDatabase
	}
	hash := hashToken(token)
	row, err := st.db.Session(ctx, hash)
	if errors.Is(err, store.ErrSessionNotFound) {
		return Session{}, ErrNoSession
	}
	if err != nil {
		return Session{}, err
	}
	if !st.now().Before(row.ExpiresAt) {
		if err := st.db.DeleteSession(ctx, hash); err != nil {
			return Session{}, err
		}
		return Session{}, ErrNoSession
	}
	return Session{Token: token, User: row.User, Role: row.Role, Expires: row.ExpiresAt}, nil
}

// Delete removes a session (logout). An empty or unknown token is not an
// error.
func (st *SessionStore) Delete(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	if st.db == nil {
		return errNoDatabase
	}
	return st.db.DeleteSession(ctx, hashToken(token))
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

// UserFromContext returns the authenticated username, or "" when auth is off
// or there is no session. Callers then use results.CurrentUser().
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
