// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Stream identifies where a log line came from.
type Stream string

const (
	StreamStdout Stream = "stdout"
	StreamStderr Stream = "stderr"
	// StreamSystem is the engine speaking for itself — "cancelled by user",
	// "timed out after 20m" — so the reason a run ended is in the same
	// ordered transcript as the output, not only in a status field.
	StreamSystem Stream = "system"
)

// LogLine is one persisted output line. Seq is monotonic within a run and is
// the cursor consumers resume from.
type LogLine struct {
	Seq    int64
	At     time.Time
	Stream Stream
	Text   string
}

// maxLogLine bounds a single stored line. Some tools emit megabyte-long lines
// (a base64 blob, a minified manifest); storing them whole would blow up the
// database and any client rendering them.
const maxLogLine = 16 * 1024

const truncationNotice = "… [truncated by snowops: line exceeded 16KiB]"

// AppendLogs writes lines for a run in one transaction, assigning sequence
// numbers after the run's current maximum. It returns the last sequence
// written.
//
// Persistence is never skipped for a slow consumer — that was v1's bug, where
// a non-blocking channel send dropped output silently. Delivery backpressure is
// the streaming layer's problem; the transcript is always complete.
func (s *Store) AppendLogs(ctx context.Context, runID string, lines []LogLine) (int64, error) {
	if len(lines) == 0 {
		return s.LastLogSeq(ctx, runID)
	}

	var last int64
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var maxSeq sql.NullInt64
		if err := tx.QueryRowContext(ctx,
			`SELECT MAX(seq) FROM run_logs WHERE run_id = ?`, runID).Scan(&maxSeq); err != nil {
			return err
		}
		last = maxSeq.Int64

		stmt, err := tx.PrepareContext(ctx,
			`INSERT INTO run_logs (run_id, seq, ts, stream, text) VALUES (?, ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()

		for _, l := range lines {
			stream := l.Stream
			if stream == "" {
				stream = StreamStdout
			}
			at := l.At
			if at.IsZero() {
				at = s.now()
			}
			text := l.Text
			if len(text) > maxLogLine {
				text = text[:maxLogLine] + truncationNotice
			}

			last++
			if _, err := stmt.ExecContext(ctx, runID, last, at.UnixMicro(), string(stream), text); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("appending logs for run %s: %w", runID, err)
	}
	return last, nil
}

// ReadLogs returns up to limit lines with seq strictly greater than after.
// Passing after=0 reads from the beginning; passing the last seq a client saw
// resumes exactly where it left off, with no gap and no duplicate (ADR-0006).
func (s *Store) ReadLogs(ctx context.Context, runID string, after int64, limit int) ([]LogLine, error) {
	if limit <= 0 {
		limit = 1000
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT seq, ts, stream, text
		  FROM run_logs
		 WHERE run_id = ? AND seq > ?
		 ORDER BY seq ASC
		 LIMIT ?`, runID, after, limit)
	if err != nil {
		return nil, fmt.Errorf("reading logs for run %s: %w", runID, err)
	}
	defer rows.Close()

	var out []LogLine
	for rows.Next() {
		var (
			l      LogLine
			ts     int64
			stream string
		)
		if err := rows.Scan(&l.Seq, &ts, &stream, &l.Text); err != nil {
			return nil, err
		}
		l.At = time.UnixMicro(ts).UTC()
		l.Stream = Stream(stream)
		out = append(out, l)
	}
	return out, rows.Err()
}

// LastLogSeq returns the highest sequence written for a run, or 0 if none.
func (s *Store) LastLogSeq(ctx context.Context, runID string) (int64, error) {
	var maxSeq sql.NullInt64
	if err := s.db.QueryRowContext(ctx,
		`SELECT MAX(seq) FROM run_logs WHERE run_id = ?`, runID).Scan(&maxSeq); err != nil {
		return 0, fmt.Errorf("reading log cursor for run %s: %w", runID, err)
	}
	return maxSeq.Int64, nil
}

// ── Steps ───────────────────────────────────────────────────────────────────

// StepStatus is a step's state.
type StepStatus string

const (
	StepRunning   StepStatus = "running"
	StepSucceeded StepStatus = "succeeded"
	StepFailed    StepStatus = "failed"
)

// Step is one entry in a run's progress timeline, parsed from
// ##snowops:step:<name> markers a script emits.
type Step struct {
	Index     int
	Name      string
	Status    StepStatus
	StartedAt time.Time
	EndedAt   time.Time
}

// ErrStepNotFound is returned when finishing a step that was never started.
var ErrStepNotFound = errors.New("step not found")

// StartStep records the beginning of a step and returns its index. Starting a
// step closes the previous one as succeeded: scripts announce steps, they do
// not announce the end of the one before.
func (s *Store) StartStep(ctx context.Context, runID, name string, at time.Time) (int, error) {
	if at.IsZero() {
		at = s.now()
	}

	var idx int
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var maxIdx sql.NullInt64
		if err := tx.QueryRowContext(ctx,
			`SELECT MAX(idx) FROM run_steps WHERE run_id = ?`, runID).Scan(&maxIdx); err != nil {
			return err
		}

		if maxIdx.Valid {
			if _, err := tx.ExecContext(ctx, `
				UPDATE run_steps SET status = ?, ended_at = ?
				 WHERE run_id = ? AND idx = ? AND status = ?`,
				StepSucceeded, at.UnixMicro(), runID, maxIdx.Int64, StepRunning); err != nil {
				return err
			}
			idx = int(maxIdx.Int64) + 1
		}

		_, err := tx.ExecContext(ctx, `
			INSERT INTO run_steps (run_id, idx, name, status, started_at)
			VALUES (?, ?, ?, ?, ?)`,
			runID, idx, name, StepRunning, at.UnixMicro())
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("starting step %q on run %s: %w", name, runID, err)
	}
	return idx, nil
}

// FinishSteps closes any still-running step for a run. Called when the run
// itself ends, so an interrupted run's timeline shows exactly which step it
// died in rather than leaving it open forever.
func (s *Store) FinishSteps(ctx context.Context, runID string, status StepStatus, at time.Time) error {
	if at.IsZero() {
		at = s.now()
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE run_steps SET status = ?, ended_at = ?
		 WHERE run_id = ? AND status = ?`,
		string(status), at.UnixMicro(), runID, StepRunning)
	if err != nil {
		return fmt.Errorf("finishing steps for run %s: %w", runID, err)
	}
	return nil
}

// ListSteps returns a run's step timeline in order.
func (s *Store) ListSteps(ctx context.Context, runID string) ([]Step, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT idx, name, status, started_at, ended_at
		  FROM run_steps WHERE run_id = ? ORDER BY idx ASC`, runID)
	if err != nil {
		return nil, fmt.Errorf("listing steps for run %s: %w", runID, err)
	}
	defer rows.Close()

	var out []Step
	for rows.Next() {
		var (
			st      Step
			status  string
			started int64
			ended   sql.NullInt64
		)
		if err := rows.Scan(&st.Index, &st.Name, &status, &started, &ended); err != nil {
			return nil, err
		}
		st.Status = StepStatus(status)
		st.StartedAt = time.UnixMicro(started).UTC()
		st.EndedAt = fromMicro(ended)
		out = append(out, st)
	}
	return out, rows.Err()
}

// ── Audit ───────────────────────────────────────────────────────────────────

// AuditEntry records a mutating operation: who, what, when, from where.
type AuditEntry struct {
	At     time.Time
	Actor  string
	Action string
	Target string
	RunID  string
	Source string // cli | api | system
	Detail string
}

// Audit appends an audit entry. Audit deliberately outlives runs — the run_id
// column is not a foreign key, so pruning run history does not erase the record
// that something happened.
func (s *Store) Audit(ctx context.Context, e AuditEntry) error {
	if e.Action == "" {
		return errors.New("store: audit action is required")
	}
	if e.At.IsZero() {
		e.At = s.now()
	}
	var runID sql.NullString
	if e.RunID != "" {
		runID = sql.NullString{String: e.RunID, Valid: true}
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO audit (ts, actor, action, target, run_id, source, detail)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.At.UnixMicro(), e.Actor, e.Action, e.Target, runID, e.Source, e.Detail)
	if err != nil {
		return fmt.Errorf("writing audit entry: %w", err)
	}
	return nil
}

// ListAudit returns audit entries newest-first.
func (s *Store) ListAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT ts, actor, action, target, run_id, source, detail
		  FROM audit ORDER BY ts DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("listing audit entries: %w", err)
	}
	defer rows.Close()

	var out []AuditEntry
	for rows.Next() {
		var (
			e     AuditEntry
			ts    int64
			runID sql.NullString
		)
		if err := rows.Scan(&ts, &e.Actor, &e.Action, &e.Target, &runID, &e.Source, &e.Detail); err != nil {
			return nil, err
		}
		e.At = time.UnixMicro(ts).UTC()
		e.RunID = runID.String
		out = append(out, e)
	}
	return out, rows.Err()
}
