-- 0001_init — the durable core (W1).
--
-- Timestamps are Unix microseconds (INTEGER). Wall-clock is stored because it
-- is what a human reads back; durations that must be accurate are measured
-- from a monotonic clock in-process and stored as explicit values, never
-- derived by subtracting two wall-clock columns across a DST boundary.
--
-- Forward-only: this file is never edited once released. Changes ship as a new
-- numbered migration.

CREATE TABLE runs (
    id          TEXT    PRIMARY KEY,
    kind        TEXT    NOT NULL,               -- platform.install, scenario.activate, …
    target      TEXT    NOT NULL DEFAULT '',    -- what the run acts on
    lock_key    TEXT    NOT NULL DEFAULT '',    -- exclusive key (ADR-0004)
    status      TEXT    NOT NULL,               -- queued|running|succeeded|failed|cancelled|timed_out
    actor       TEXT    NOT NULL DEFAULT '',    -- who asked for it (server mode)
    argv        TEXT    NOT NULL DEFAULT '[]',  -- JSON array, argv-only (never a shell string)
    script      TEXT    NOT NULL DEFAULT '',
    timeout_ms  INTEGER NOT NULL DEFAULT 0,     -- 0 = engine default
    queued_at   INTEGER NOT NULL,
    started_at  INTEGER,
    ended_at    INTEGER,
    duration_us INTEGER,                        -- measured monotonically while running
    exit_code   INTEGER,
    error       TEXT    NOT NULL DEFAULT '',

    CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'cancelled', 'timed_out'))
);

-- Listing runs newest-first is the common read (CLI `runs list`, UI overview).
CREATE INDEX runs_queued_at_idx ON runs (queued_at DESC);
CREATE INDEX runs_status_idx    ON runs (status);

-- Lock conflicts are checked on every submit, so keep the live set cheap to scan.
CREATE INDEX runs_active_lock_idx ON runs (lock_key)
    WHERE status IN ('queued', 'running');

-- Every output line, in order. seq is monotonic per run and is the cursor
-- consumers resume from (ADR-0006) — no line is ever dropped for a slow reader.
CREATE TABLE run_logs (
    run_id TEXT    NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    seq    INTEGER NOT NULL,
    ts     INTEGER NOT NULL,
    stream TEXT    NOT NULL,   -- stdout | stderr | system
    text   TEXT    NOT NULL,

    PRIMARY KEY (run_id, seq),
    CHECK (stream IN ('stdout', 'stderr', 'system'))
) WITHOUT ROWID;

-- Structured progress, parsed from ##snowops:step:<name> markers, so the UI
-- can show "Installing Prometheus… 3 of 7" instead of a wall of text.
CREATE TABLE run_steps (
    run_id     TEXT    NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    idx        INTEGER NOT NULL,
    name       TEXT    NOT NULL,
    status     TEXT    NOT NULL,   -- running | succeeded | failed
    started_at INTEGER NOT NULL,
    ended_at   INTEGER,

    PRIMARY KEY (run_id, idx),
    CHECK (status IN ('running', 'succeeded', 'failed'))
) WITHOUT ROWID;

-- Who did what, when, from where. Survives the run record's retention window.
CREATE TABLE audit (
    id     INTEGER PRIMARY KEY AUTOINCREMENT,
    ts     INTEGER NOT NULL,
    actor  TEXT    NOT NULL DEFAULT '',
    action TEXT    NOT NULL,
    target TEXT    NOT NULL DEFAULT '',
    run_id TEXT,                          -- deliberately not a FK: audit outlives runs
    source TEXT    NOT NULL DEFAULT '',   -- cli | api | system
    detail TEXT    NOT NULL DEFAULT ''
);

CREATE INDEX audit_ts_idx    ON audit (ts DESC);
CREATE INDEX audit_actor_idx ON audit (actor);
