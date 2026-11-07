-- 0004_sessions - API login sessions, so a restart of `labctl ui` keeps
-- everyone logged in.
--
-- A session is keyed by the SHA-256 hash of its token, never the token
-- itself: a copy of this database cannot be replayed as a session cookie.

CREATE TABLE sessions (
    token_hash TEXT    PRIMARY KEY,   -- hex SHA-256 of the cookie value
    user_name  TEXT    NOT NULL,
    role       TEXT    NOT NULL,
    created_at INTEGER NOT NULL,      -- Unix microseconds
    expires_at INTEGER NOT NULL       -- Unix microseconds; invalid from this instant
);

-- Removing expired sessions scans by expiry.
CREATE INDEX sessions_expires_idx ON sessions (expires_at);
