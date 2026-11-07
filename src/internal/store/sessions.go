// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrSessionNotFound is returned by Session for a hash with no stored row.
var ErrSessionNotFound = errors.New("session not found")

// Session is one stored API login session. TokenHash identifies it; the token
// itself is never stored.
type Session struct {
	TokenHash string
	User      string
	Role      string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// InsertSession stores a new session.
func (s *Store) InsertSession(ctx context.Context, sess Session) error {
	if sess.TokenHash == "" {
		return errors.New("storing session: token hash is required")
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (token_hash, user_name, role, created_at, expires_at) VALUES (?, ?, ?, ?, ?)`,
		sess.TokenHash, sess.User, sess.Role, sess.CreatedAt.UnixMicro(), sess.ExpiresAt.UnixMicro())
	if err != nil {
		return fmt.Errorf("storing session for %s: %w", sess.User, err)
	}
	return nil
}

// Session loads the session stored under tokenHash, expired or not.
func (s *Store) Session(ctx context.Context, tokenHash string) (Session, error) {
	sess := Session{TokenHash: tokenHash}
	var created, expires int64
	err := s.db.QueryRowContext(ctx,
		`SELECT user_name, role, created_at, expires_at FROM sessions WHERE token_hash = ?`, tokenHash).
		Scan(&sess.User, &sess.Role, &created, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrSessionNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("reading session: %w", err)
	}
	sess.CreatedAt = time.UnixMicro(created).UTC()
	sess.ExpiresAt = time.UnixMicro(expires).UTC()
	return sess, nil
}

// DeleteSession removes the session stored under tokenHash. An unknown hash
// is not an error.
func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash); err != nil {
		return fmt.Errorf("deleting session: %w", err)
	}
	return nil
}

// DeleteExpiredSessions removes every session whose expiry is at or before
// now and returns how many it removed.
func (s *Store) DeleteExpiredSessions(ctx context.Context, now time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, now.UnixMicro())
	if err != nil {
		return 0, fmt.Errorf("deleting expired sessions: %w", err)
	}
	return res.RowsAffected()
}
