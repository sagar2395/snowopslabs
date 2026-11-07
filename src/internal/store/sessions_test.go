// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSessions_InsertReadDelete(t *testing.T) {
	ctx := t.Context()
	s := newStore(t)
	now := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	in := Session{TokenHash: "h1", User: "alice", Role: "operator", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := s.InsertSession(ctx, in); err != nil {
		t.Fatalf("InsertSession: %v", err)
	}

	got, err := s.Session(ctx, "h1")
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if got != in {
		t.Errorf("Session = %+v, want %+v", got, in)
	}
	if err := s.InsertSession(ctx, in); err == nil {
		t.Error("a second session with the same hash was stored")
	}

	if err := s.DeleteSession(ctx, "h1"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := s.Session(ctx, "h1"); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("Session after delete = %v, want ErrSessionNotFound", err)
	}
	if err := s.DeleteSession(ctx, "h1"); err != nil {
		t.Errorf("deleting an unknown hash = %v, want nil", err)
	}
}

func TestSessions_InsertNeedsAHash(t *testing.T) {
	if err := newStore(t).InsertSession(t.Context(), Session{User: "alice"}); err == nil {
		t.Error("a session with no token hash was stored")
	}
}

func TestDeleteExpiredSessions(t *testing.T) {
	now := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		expiresIn   time.Duration
		wantRemoved int64
	}{
		{"expired an hour ago", -time.Hour, 1},
		{"expires this instant", 0, 1},
		{"expires in a microsecond", time.Microsecond, 0},
		{"expires in an hour", time.Hour, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			s := newStore(t)
			if err := s.InsertSession(ctx, Session{TokenHash: "h", User: "u", Role: "operator", CreatedAt: now, ExpiresAt: now.Add(tt.expiresIn)}); err != nil {
				t.Fatal(err)
			}
			n, err := s.DeleteExpiredSessions(ctx, now)
			if err != nil || n != tt.wantRemoved {
				t.Fatalf("DeleteExpiredSessions = %d, %v; want %d", n, err, tt.wantRemoved)
			}
			_, err = s.Session(ctx, "h")
			if gone := errors.Is(err, ErrSessionNotFound); gone != (tt.wantRemoved == 1) {
				t.Errorf("session gone = %v, want %v", gone, tt.wantRemoved == 1)
			}
		})
	}
}

func TestSessions_ContextEnds(t *testing.T) {
	s := newStore(t)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	expired, stop := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer stop()
	for name, ctx := range map[string]context.Context{"cancelled": cancelled, "deadline exceeded": expired} {
		t.Run(name, func(t *testing.T) {
			if err := s.InsertSession(ctx, Session{TokenHash: "h", User: "u", Role: "operator"}); err == nil {
				t.Error("InsertSession succeeded")
			}
			if _, err := s.Session(ctx, "h"); err == nil || errors.Is(err, ErrSessionNotFound) {
				t.Errorf("Session = %v, want a context error", err)
			}
			if err := s.DeleteSession(ctx, "h"); err == nil {
				t.Error("DeleteSession succeeded")
			}
			if _, err := s.DeleteExpiredSessions(ctx, time.Now()); err == nil {
				t.Error("DeleteExpiredSessions succeeded")
			}
		})
	}
}
