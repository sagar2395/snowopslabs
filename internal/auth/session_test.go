// SPDX-License-Identifier: Apache-2.0
package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSessionStore_CreateGetDelete(t *testing.T) {
	st := NewSessionStore(time.Hour)

	sess, err := st.Create("alice", RoleOperator)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sess.Token == "" {
		t.Fatal("expected non-empty token")
	}

	got, ok := st.Get(sess.Token)
	if !ok || got.User != "alice" || got.Role != RoleOperator {
		t.Errorf("Get = %+v, %v", got, ok)
	}

	st.Delete(sess.Token)
	if _, ok := st.Get(sess.Token); ok {
		t.Error("expected session gone after Delete")
	}
}

func TestSessionStore_Get(t *testing.T) {
	st := NewSessionStore(time.Hour)
	sess, _ := st.Create("bob", RoleParticipant)

	tests := []struct {
		name  string
		token string
		want  bool
	}{
		{"valid token", sess.Token, true},
		{"empty token", "", false},
		{"unknown token", "deadbeef", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, ok := st.Get(tt.token); ok != tt.want {
				t.Errorf("Get(%q) ok = %v, want %v", tt.token, ok, tt.want)
			}
		})
	}
}

func TestSessionStore_Expiry(t *testing.T) {
	st := NewSessionStore(time.Minute)
	now := time.Now()
	st.now = func() time.Time { return now }

	sess, _ := st.Create("carol", RoleOperator)
	if _, ok := st.Get(sess.Token); !ok {
		t.Fatal("session should be valid immediately after creation")
	}

	// Advance past the TTL.
	st.now = func() time.Time { return now.Add(2 * time.Minute) }
	if _, ok := st.Get(sess.Token); ok {
		t.Error("session should be expired")
	}
	if st.Count() != 0 {
		t.Errorf("Count() = %d, want 0 after expiry", st.Count())
	}
}

func TestSessionStore_DefaultTTL(t *testing.T) {
	st := NewSessionStore(0)
	if st.ttl != DefaultSessionTTL {
		t.Errorf("ttl = %v, want default %v", st.ttl, DefaultSessionTTL)
	}
}

func TestSessionStore_Count(t *testing.T) {
	st := NewSessionStore(time.Hour)
	if st.Count() != 0 {
		t.Fatalf("empty store Count() = %d", st.Count())
	}
	st.Create("a", RoleOperator)
	st.Create("b", RoleParticipant)
	if st.Count() != 2 {
		t.Errorf("Count() = %d, want 2", st.Count())
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
