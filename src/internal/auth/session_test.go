// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/snowops-oss/snowopslabs/internal/store"
)

// openDB opens a lab database in a temp directory and returns it with its
// path, so a test can reopen the same file as a restarted server would.
func openDB(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "snowops.db")
	return reopenDB(t, path), path
}

func reopenDB(t *testing.T, path string) *store.Store {
	t.Helper()
	db, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestSessionStore_CreateGetDelete(t *testing.T) {
	ctx := t.Context()
	db, _ := openDB(t)
	st := NewSessionStore(db, time.Hour)

	sess, err := st.Create(ctx, "alice", RoleOperator)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sess.Token == "" {
		t.Fatal("expected non-empty token")
	}

	got, err := st.Get(ctx, sess.Token)
	if err != nil || got.User != "alice" || got.Role != RoleOperator || got.Token != sess.Token {
		t.Errorf("Get = %+v, %v", got, err)
	}

	if err := st.Delete(ctx, sess.Token); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := st.Get(ctx, sess.Token); !errors.Is(err, ErrNoSession) {
		t.Errorf("Get after Delete = %v, want ErrNoSession", err)
	}
	if err := st.Delete(ctx, sess.Token); err != nil {
		t.Errorf("Delete of an unknown token = %v, want nil", err)
	}
	if err := st.Delete(ctx, ""); err != nil {
		t.Errorf("Delete of an empty token = %v, want nil", err)
	}
}

func TestSessionStore_Get(t *testing.T) {
	ctx := t.Context()
	db, _ := openDB(t)
	st := NewSessionStore(db, time.Hour)
	sess, _ := st.Create(ctx, "bob", RoleParticipant)

	tests := []struct {
		name    string
		token   string
		wantErr error
	}{
		{"valid token", sess.Token, nil},
		{"empty token", "", ErrNoSession},
		{"unknown token", "deadbeef", ErrNoSession},
		{"the stored hash is not a token", hashToken(sess.Token), ErrNoSession},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := st.Get(ctx, tt.token); !errors.Is(err, tt.wantErr) {
				t.Errorf("Get(%q) = %v, want %v", tt.token, err, tt.wantErr)
			}
		})
	}
}

// A session outlives the server: a store reopened on the same database file
// accepts it, and a logout through the new store ends it for good.
func TestSessionStore_SurvivesRestart(t *testing.T) {
	ctx := t.Context()
	db, path := openDB(t)
	sess, err := NewSessionStore(db, time.Hour).Create(ctx, "alice", RoleOperator)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	restarted := NewSessionStore(reopenDB(t, path), time.Hour)
	got, err := restarted.Get(ctx, sess.Token)
	if err != nil || got.User != "alice" || !got.Expires.Equal(sess.Expires.Truncate(time.Microsecond)) {
		t.Fatalf("after a restart Get = %+v, %v; want alice's session", got, err)
	}
	if err := restarted.Delete(ctx, sess.Token); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := NewSessionStore(reopenDB(t, path), time.Hour).Get(ctx, sess.Token); !errors.Is(err, ErrNoSession) {
		t.Errorf("a logged-out session is back after another restart: %v", err)
	}
}

// The database holds a hash of the token, never the token, so a copy of it
// cannot be replayed as a cookie.
func TestSessionStore_StoresOnlyATokenHash(t *testing.T) {
	ctx := t.Context()
	db, _ := openDB(t)
	sess, err := NewSessionStore(db, time.Hour).Create(ctx, "alice", RoleOperator)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	var stored string
	if err := db.DB().QueryRowContext(ctx, `SELECT token_hash FROM sessions`).Scan(&stored); err != nil {
		t.Fatalf("reading the sessions table: %v", err)
	}
	if stored == sess.Token || strings.Contains(stored, sess.Token) {
		t.Fatalf("the sessions table holds the token itself: %q", stored)
	}
	if stored != hashToken(sess.Token) {
		t.Errorf("stored key = %q, want the SHA-256 of the token", stored)
	}
}

func TestSessionStore_Expiry(t *testing.T) {
	ctx := t.Context()
	db, _ := openDB(t)
	st := NewSessionStore(db, time.Minute)
	now := time.Now()
	st.now = func() time.Time { return now }

	sess, _ := st.Create(ctx, "carol", RoleOperator)
	if _, err := st.Get(ctx, sess.Token); err != nil {
		t.Fatalf("session should be valid immediately after creation: %v", err)
	}

	st.now = func() time.Time { return now.Add(time.Minute) }
	if _, err := st.Get(ctx, sess.Token); !errors.Is(err, ErrNoSession) {
		t.Errorf("Get at the expiry instant = %v, want ErrNoSession", err)
	}
	if _, err := db.Session(ctx, hashToken(sess.Token)); !errors.Is(err, store.ErrSessionNotFound) {
		t.Errorf("an expired session found by Get is still stored: %v", err)
	}
}

// Create removes every expired session, so sessions nobody comes back for do
// not pile up.
func TestSessionStore_CreateRemovesExpiredRows(t *testing.T) {
	ctx := t.Context()
	db, _ := openDB(t)
	st := NewSessionStore(db, time.Minute)
	now := time.Now()
	st.now = func() time.Time { return now }
	old, _ := st.Create(ctx, "dave", RoleParticipant)

	st.now = func() time.Time { return now.Add(2 * time.Minute) }
	fresh, err := st.Create(ctx, "erin", RoleParticipant)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := db.Session(ctx, hashToken(old.Token)); !errors.Is(err, store.ErrSessionNotFound) {
		t.Errorf("an expired session survived a later Create: %v", err)
	}
	if _, err := db.Session(ctx, hashToken(fresh.Token)); err != nil {
		t.Errorf("the new session is not stored: %v", err)
	}
}

func TestSessionStore_DefaultTTL(t *testing.T) {
	st := NewSessionStore(nil, 0)
	if st.ttl != DefaultSessionTTL {
		t.Errorf("ttl = %v, want default %v", st.ttl, DefaultSessionTTL)
	}
}

// With no database every operation that needs one fails, and says where to
// look, instead of letting anyone in.
func TestSessionStore_NoDatabase(t *testing.T) {
	ctx := t.Context()
	st := NewSessionStore(nil, time.Hour)
	if _, err := st.Create(ctx, "alice", RoleOperator); !errors.Is(err, errNoDatabase) {
		t.Errorf("Create = %v, want errNoDatabase", err)
	}
	if _, err := st.Get(ctx, "tok"); !errors.Is(err, errNoDatabase) {
		t.Errorf("Get = %v, want errNoDatabase", err)
	}
	if err := st.Delete(ctx, "tok"); !errors.Is(err, errNoDatabase) {
		t.Errorf("Delete = %v, want errNoDatabase", err)
	}
	if _, err := st.Get(ctx, ""); !errors.Is(err, ErrNoSession) {
		t.Errorf("Get of an empty token = %v, want ErrNoSession", err)
	}
}

// A cancelled or timed-out request context stops each database call, and
// is reported as an error, never as a valid or missing session.
func TestSessionStore_ContextEnds(t *testing.T) {
	db, _ := openDB(t)
	st := NewSessionStore(db, time.Hour)
	sess, err := st.Create(t.Context(), "alice", RoleOperator)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	expired, stop := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer stop()

	for name, ctx := range map[string]context.Context{"cancelled": cancelled, "deadline exceeded": expired} {
		t.Run(name, func(t *testing.T) {
			if _, err := st.Create(ctx, "bob", RoleParticipant); err == nil {
				t.Error("Create succeeded on an ended context")
			}
			if _, err := st.Get(ctx, sess.Token); err == nil || errors.Is(err, ErrNoSession) {
				t.Errorf("Get = %v, want a context error", err)
			}
			if err := st.Delete(ctx, sess.Token); err == nil {
				t.Error("Delete succeeded on an ended context")
			}
		})
	}
}

func TestContextHelpers(t *testing.T) {
	ctx := context.Background()
	if u := UserFromContext(ctx); u != "" {
		t.Errorf("UserFromContext(empty) = %q, want \"\"", u)
	}
	if _, ok := SessionFromContext(ctx); ok {
		t.Error("SessionFromContext(empty) should report not-ok")
	}

	ctx = WithSession(ctx, Session{User: "dave", Role: RoleParticipant})
	if u := UserFromContext(ctx); u != "dave" {
		t.Errorf("UserFromContext = %q, want dave", u)
	}
	sess, ok := SessionFromContext(ctx)
	if !ok || sess.Role != RoleParticipant {
		t.Errorf("SessionFromContext = %+v, %v", sess, ok)
	}
}

func TestTokenFromRequest(t *testing.T) {
	tests := []struct {
		name  string
		setup func(r *http.Request)
		want  string
	}{
		{
			name:  "from cookie",
			setup: func(r *http.Request) { r.AddCookie(&http.Cookie{Name: CookieName, Value: "cookie-tok"}) },
			want:  "cookie-tok",
		},
		{
			name:  "from bearer header",
			setup: func(r *http.Request) { r.Header.Set("Authorization", "Bearer header-tok") },
			want:  "header-tok",
		},
		{
			name: "cookie wins over header",
			setup: func(r *http.Request) {
				r.AddCookie(&http.Cookie{Name: CookieName, Value: "cookie-tok"})
				r.Header.Set("Authorization", "Bearer header-tok")
			},
			want: "cookie-tok",
		},
		{
			name:  "none",
			setup: func(r *http.Request) {},
			want:  "",
		},
		{
			name:  "non-bearer auth header ignored",
			setup: func(r *http.Request) { r.Header.Set("Authorization", "Basic abc") },
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			tt.setup(r)
			if got := TokenFromRequest(r); got != tt.want {
				t.Errorf("TokenFromRequest = %q, want %q", got, tt.want)
			}
		})
	}
}
