// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Status is a run's lifecycle state. The set is closed and enforced by a CHECK
// constraint, so an unknown value cannot reach the database.
type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
	StatusTimedOut  Status = "timed_out"
)

// Terminal reports whether the status is final. A terminal run releases its
// lock key and never changes again.
func (s Status) Terminal() bool {
	switch s {
	case StatusSucceeded, StatusFailed, StatusCancelled, StatusTimedOut:
		return true
	default:
		return false
	}
}

// Valid reports whether s is a known status.
func (s Status) Valid() bool {
	switch s {
	case StatusQueued, StatusRunning, StatusSucceeded, StatusFailed, StatusCancelled, StatusTimedOut:
		return true
	default:
		return false
	}
}

// ErrRunNotFound is returned when no run matches an ID.
var ErrRunNotFound = errors.New("run not found")

// Run is the durable record of one shelled-out operation.
type Run struct {
	ID       string
	Kind     string // platform.install, scenario.activate, …
	Target   string // what it acts on
	LockKey  string // exclusive key (ADR-0004); empty means no lock
	Status   Status
	Actor    string
	Script   string
	Argv     []string // argv-only — never a shell string (ADR-0003)
	Timeout  time.Duration
	QueuedAt time.Time

	StartedAt time.Time
	EndedAt   time.Time
	Duration  time.Duration // measured monotonically, not EndedAt-StartedAt
	ExitCode  *int
	Error     string
}

// CreateRun inserts a queued run. The caller supplies the ID so it can be
// returned to an HTTP client before the run starts (ADR-0006: mutations return
// 202 with a run ID).
func (s *Store) CreateRun(ctx context.Context, r Run) error {
	if r.ID == "" {
		return errors.New("store: run ID is required")
	}
	if r.Kind == "" {
		return errors.New("store: run kind is required")
	}
	if r.Status == "" {
		r.Status = StatusQueued
	}
	if !r.Status.Valid() {
		return fmt.Errorf("store: invalid run status %q", r.Status)
	}
	if r.QueuedAt.IsZero() {
		r.QueuedAt = s.now()
	}

	argv, err := json.Marshal(nonNil(r.Argv))
	if err != nil {
		return fmt.Errorf("encoding argv: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO runs (id, kind, target, lock_key, status, actor, argv, script,
		                  timeout_ms, queued_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.Kind, r.Target, r.LockKey, string(r.Status), r.Actor, string(argv),
		r.Script, r.Timeout.Milliseconds(), r.QueuedAt.UnixMicro())
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("store: run %q already exists", r.ID)
		}
		return fmt.Errorf("creating run %s: %w", r.ID, err)
	}
	return nil
}

// StartRun marks a queued run as running. It is a no-op error if the run has
// already left the queued state, which is what makes a double-start from two
// workers impossible rather than merely unlikely.
func (s *Store) StartRun(ctx context.Context, id string, at time.Time) error {
	if at.IsZero() {
		at = s.now()
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE runs SET status = ?, started_at = ? WHERE id = ? AND status = ?`,
		StatusRunning, at.UnixMicro(), id, StatusQueued)
	if err != nil {
		return fmt.Errorf("starting run %s: %w", id, err)
	}
	return expectOneRow(res, id, "run is not queued")
}

// FinishRun records a terminal state. exitCode may be nil when the run never
// produced one (cancelled before exec, or killed).
func (s *Store) FinishRun(ctx context.Context, id string, status Status, exitCode *int, runErr string, at time.Time, dur time.Duration) error {
	if !status.Terminal() {
		return fmt.Errorf("store: %q is not a terminal status", status)
	}
	if at.IsZero() {
		at = s.now()
	}

	var code sql.NullInt64
	if exitCode != nil {
		code = sql.NullInt64{Int64: int64(*exitCode), Valid: true}
	}
	// Microseconds, not milliseconds: a fast script that finishes in under a
	// millisecond must not be recorded as having taken no time at all.
	var durUs sql.NullInt64
	if dur > 0 {
		durUs = sql.NullInt64{Int64: dur.Microseconds(), Valid: true}
	}

	// Only a non-terminal run may be finished: a cancellation racing with a
	// natural exit must not overwrite the outcome that already landed.
	res, err := s.db.ExecContext(ctx, `
		UPDATE runs
		   SET status = ?, exit_code = ?, error = ?, ended_at = ?, duration_us = ?
		 WHERE id = ? AND status IN (?, ?)`,
		string(status), code, runErr, at.UnixMicro(), durUs,
		id, StatusQueued, StatusRunning)
	if err != nil {
		return fmt.Errorf("finishing run %s: %w", id, err)
	}
	return expectOneRow(res, id, "run already finished")
}

// GetRun loads one run.
func (s *Store) GetRun(ctx context.Context, id string) (Run, error) {
	row := s.db.QueryRowContext(ctx, selectRunColumns+` WHERE id = ?`, id)
	r, err := scanRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, fmt.Errorf("%w: %s", ErrRunNotFound, id)
	}
	return r, err
}

// RunFilter narrows ListRuns. The zero value lists everything, newest first.
type RunFilter struct {
	Status []Status
	Kind   string
	// Before pages backwards: only runs queued strictly before this instant.
	// Cursor pagination rather than OFFSET, so a run inserted mid-scroll cannot
	// shift the page boundary and hide a row.
	Before time.Time
	Limit  int
}

// ListRuns returns runs newest-first.
func (s *Store) ListRuns(ctx context.Context, f RunFilter) ([]Run, error) {
	var (
		where []string
		args  []any
	)
	if len(f.Status) > 0 {
		placeholders := make([]string, len(f.Status))
		for i, st := range f.Status {
			placeholders[i] = "?"
			args = append(args, string(st))
		}
		where = append(where, "status IN ("+strings.Join(placeholders, ",")+")")
	}
	if f.Kind != "" {
		where = append(where, "kind = ?")
		args = append(args, f.Kind)
	}
	if !f.Before.IsZero() {
		where = append(where, "queued_at < ?")
		args = append(args, f.Before.UnixMicro())
	}

	query := selectRunColumns
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY queued_at DESC, id DESC"

	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	query += " LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing runs: %w", err)
	}
	defer rows.Close()

	var out []Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ActiveRunForLock returns the queued-or-running run holding lockKey, if any.
// This is the check behind the 409 in ADR-0004: a conflicting submission is
// refused immediately, naming the run that holds the lock.
func (s *Store) ActiveRunForLock(ctx context.Context, lockKey string) (Run, bool, error) {
	if lockKey == "" {
		return Run{}, false, nil
	}
	row := s.db.QueryRowContext(ctx,
		selectRunColumns+` WHERE lock_key = ? AND status IN (?, ?) ORDER BY queued_at ASC LIMIT 1`,
		lockKey, StatusQueued, StatusRunning)
	r, err := scanRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, false, nil
	}
	if err != nil {
		return Run{}, false, err
	}
	return r, true, nil
}

// RecoverOrphanedRuns marks every non-terminal run as cancelled. Their
// processes died with the previous server, so leaving them "running" would
// hold locks forever and lie to the user about what is happening. Called once
// at startup; returns the IDs it reaped.
func (s *Store) RecoverOrphanedRuns(ctx context.Context, at time.Time) ([]string, error) {
	if at.IsZero() {
		at = s.now()
	}

	var ids []string
	err := s.tx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx,
			`SELECT id FROM runs WHERE status IN (?, ?)`, StatusQueued, StatusRunning)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}

		_, err = tx.ExecContext(ctx, `
			UPDATE runs
			   SET status = ?, ended_at = ?,
			       error = 'interrupted: labctl exited while this run was in flight'
			 WHERE status IN (?, ?)`,
			StatusCancelled, at.UnixMicro(), StatusQueued, StatusRunning)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("recovering orphaned runs: %w", err)
	}
	return ids, nil
}

// PruneRuns deletes terminal runs that ended before cutoff, returning how many
// went. Logs and steps cascade. Without this, run_logs grows without bound on a
// long-lived server (noted as a risk in ADR-0002).
func (s *Store) PruneRuns(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM runs
		 WHERE ended_at IS NOT NULL AND ended_at < ?
		   AND status NOT IN (?, ?)`,
		before.UnixMicro(), StatusQueued, StatusRunning)
	if err != nil {
		return 0, fmt.Errorf("pruning runs: %w", err)
	}
	return res.RowsAffected()
}

const selectRunColumns = `
	SELECT id, kind, target, lock_key, status, actor, argv, script, timeout_ms,
	       queued_at, started_at, ended_at, duration_us, exit_code, error
	  FROM runs`

// scanner covers both *sql.Row and *sql.Rows.
type scanner interface{ Scan(dest ...any) error }

func scanRun(sc scanner) (Run, error) {
	var (
		r         Run
		status    string
		argv      string
		timeoutMs int64
		queuedAt  int64
		started   sql.NullInt64
		ended     sql.NullInt64
		durUs     sql.NullInt64
		exitCode  sql.NullInt64
	)
	if err := sc.Scan(&r.ID, &r.Kind, &r.Target, &r.LockKey, &status, &r.Actor,
		&argv, &r.Script, &timeoutMs, &queuedAt, &started, &ended, &durUs,
		&exitCode, &r.Error); err != nil {
		return Run{}, err
	}

	r.Status = Status(status)
	r.Timeout = time.Duration(timeoutMs) * time.Millisecond
	r.QueuedAt = time.UnixMicro(queuedAt).UTC()
	r.StartedAt = fromMicro(started)
	r.EndedAt = fromMicro(ended)
	if durUs.Valid {
		r.Duration = time.Duration(durUs.Int64) * time.Microsecond
	}
	if exitCode.Valid {
		code := int(exitCode.Int64)
		r.ExitCode = &code
	}
	if err := json.Unmarshal([]byte(argv), &r.Argv); err != nil {
		return Run{}, fmt.Errorf("run %s: decoding argv: %w", r.ID, err)
	}
	return r, nil
}

func expectOneRow(res sql.Result, id, reason string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: %s (%s)", ErrRunNotFound, id, reason)
	}
	return nil
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
