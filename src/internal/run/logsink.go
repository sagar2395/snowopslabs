// SPDX-License-Identifier: Apache-2.0

package run

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"time"

	"github.com/sagar2395/snowopslabs/internal/store"
)

// StepMarker is the line prefix a script emits to announce progress:
//
//	echo "##snowops:step:install-prometheus"
//
// The engine turns these into a structured timeline, so the UI can show
// "Installing Prometheus… 3 of 7" rather than a wall of text, and a failure can
// say precisely which step it died in.
const StepMarker = "##snowops:step:"

// flushInterval bounds how long a line can sit buffered before it is durable
// and visible to a watcher. Short enough to feel live, long enough that a
// chatty script does not cause one transaction per line.
const flushInterval = 100 * time.Millisecond

// flushLines forces a flush once this many lines are buffered, so a burst does
// not wait out the interval.
const flushLines = 64

// logSink turns a process's byte streams into ordered, persisted log lines.
//
// The invariant: the write to the store is never skipped. v1 dropped events for
// slow clients with a non-blocking channel send, so output silently vanished.
// Here delivery is only a notification — consumers read the actual lines from
// the store by cursor — so a slow reader can fall behind but can never lose
// anything.
type logSink struct {
	ctx   context.Context
	store *store.Store
	subs  *subscribers
	runID string
	now   func() time.Time

	mu      sync.Mutex
	pending []store.LogLine
	timer   *time.Timer
	closed  bool
}

func newLogSink(ctx context.Context, st *store.Store, subs *subscribers, runID string, now func() time.Time) *logSink {
	return &logSink{ctx: ctx, store: st, subs: subs, runID: runID, now: now}
}

// writer returns an io.Writer that splits input into lines on the given stream.
func (s *logSink) writer(stream store.Stream) *streamWriter {
	return &streamWriter{sink: s, stream: stream}
}

// system records an engine-authored line — "cancelled by user", "timed out
// after 20m" — in the same ordered transcript as the script's own output, so
// the reason a run ended is visible where the user is already looking.
func (s *logSink) system(text string) {
	s.add(store.LogLine{At: s.now(), Stream: store.StreamSystem, Text: text})
}

func (s *logSink) add(line store.LogLine) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.pending = append(s.pending, line)

	// A step marker is structural, not just text: record it immediately so the
	// timeline stays accurate even if the run dies in the next instant.
	if line.Stream == store.StreamStdout {
		if name, ok := parseStepMarker(line.Text); ok {
			s.mu.Unlock()
			s.flush()
			if _, err := s.store.StartStep(s.ctx, s.runID, name, line.At); err == nil {
				s.subs.publish(Event{Type: EventStep, RunID: s.runID, Step: name})
			}
			return
		}
	}

	shouldFlush := len(s.pending) >= flushLines
	if !shouldFlush && s.timer == nil {
		s.timer = time.AfterFunc(flushInterval, func() { s.flush() })
	}
	s.mu.Unlock()

	if shouldFlush {
		s.flush()
	}
}

// flush persists everything buffered and notifies watchers.
func (s *logSink) flush() {
	s.mu.Lock()
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	if len(s.pending) == 0 {
		s.mu.Unlock()
		return
	}
	batch := s.pending
	s.pending = nil
	s.mu.Unlock()

	// Persist first, notify second. A watcher woken by the event must find the
	// data already readable, never a cursor pointing at a line that is not
	// there yet.
	lastSeq, err := s.store.AppendLogs(s.ctx, s.runID, batch)
	if err != nil {
		return
	}
	s.subs.publish(Event{Type: EventLog, RunID: s.runID, Seq: lastSeq})
}

// close flushes and stops accepting further lines.
func (s *logSink) close() {
	s.flush()
	s.mu.Lock()
	s.closed = true
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	s.mu.Unlock()
}

// parseStepMarker extracts a step name from a marker line.
func parseStepMarker(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, StepMarker) {
		return "", false
	}
	name := strings.TrimSpace(strings.TrimPrefix(trimmed, StepMarker))
	if name == "" {
		return "", false
	}
	return name, true
}

// streamWriter splits a byte stream into lines for the sink.
//
// It handles the awkward parts of reading from a pipe: writes arrive at
// arbitrary boundaries, a line can span several of them, and a script may end
// without a trailing newline.
type streamWriter struct {
	sink   *logSink
	stream store.Stream

	mu  sync.Mutex
	buf bytes.Buffer
}

// maxBufferedLine caps an unterminated line. A script printing a progress bar
// with no newlines must not grow the buffer without bound.
const maxBufferedLine = 64 * 1024

func (w *streamWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	n := len(p)
	w.buf.Write(p)

	for {
		data := w.buf.Bytes()
		idx := bytes.IndexByte(data, '\n')
		if idx < 0 {
			if w.buf.Len() > maxBufferedLine {
				// Emit what we have so an unterminated stream still surfaces.
				line := strings.TrimRight(w.buf.String(), "\r")
				w.buf.Reset()
				w.sink.add(store.LogLine{At: w.sink.now(), Stream: w.stream, Text: line})
			}
			break
		}
		line := string(data[:idx])
		w.buf.Next(idx + 1)
		// Handle CRLF as well as LF; some tools emit Windows line endings.
		line = strings.TrimRight(line, "\r")
		w.sink.add(store.LogLine{At: w.sink.now(), Stream: w.stream, Text: line})
	}
	return n, nil
}

// Flush emits any trailing partial line. Called when the process has exited.
func (w *streamWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.buf.Len() == 0 {
		return
	}
	line := strings.TrimRight(w.buf.String(), "\r\n")
	w.buf.Reset()
	if line != "" {
		w.sink.add(store.LogLine{At: w.sink.now(), Stream: w.stream, Text: line})
	}
}
