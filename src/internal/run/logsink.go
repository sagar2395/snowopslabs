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
// The engine records each marker as a step, so the UI can show progress and a
// failure can name the step it happened in.
const StepMarker = "##snowops:step:"

// flushInterval is the longest a line waits in the buffer before it is stored
// and visible to watchers. Batching keeps a chatty script from costing one
// database write per line.
const flushInterval = 100 * time.Millisecond

// flushLines triggers an early flush once this many lines are buffered.
const flushLines = 64

// logSink turns a process's output into ordered log lines in the store.
//
// Every line is written to the store; only the notification to watchers may
// be dropped. A slow reader can fall behind but never loses a line, because it
// reads the lines from the store by cursor.
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

// system adds a line written by the engine itself, such as "cancelled by
// user", to the run's transcript alongside the script's own output.
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

	// Record a step marker immediately, so the step list is right even if the
	// run dies before the next flush.
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

	// Store before notifying, so a watcher woken by the event can read the
	// lines it announces.
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
// Writes from a pipe arrive at arbitrary boundaries, so a line can span
// several Write calls; the writer buffers until it sees a newline.
type streamWriter struct {
	sink   *logSink
	stream store.Stream

	mu  sync.Mutex
	buf bytes.Buffer
}

// maxBufferedLine caps a line with no newline yet, such as a progress bar, so
// the buffer cannot grow without bound.
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
				line := strings.TrimRight(w.buf.String(), "\r")
				w.buf.Reset()
				w.sink.add(store.LogLine{At: w.sink.now(), Stream: w.stream, Text: line})
			}
			break
		}
		line := string(data[:idx])
		w.buf.Next(idx + 1)
		// Some tools emit CRLF line endings.
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
