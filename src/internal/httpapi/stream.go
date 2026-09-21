// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/sagar2395/snowopslabs/internal/executor"
)

// parseAfterCursor returns the event sequence a stream should resume after:
// the Last-Event-ID header (sent by EventSource on reconnect) if present,
// otherwise ?after=<seq>. A missing or invalid value gives 0.
func parseAfterCursor(r *http.Request) int64 {
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			return n
		}
	}
	if v := r.URL.Query().Get("after"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			return n
		}
	}
	return 0
}

// sseKeepalivePeriod is how often an idle SSE stream sends a comment line, so
// proxies do not close the connection.
const sseKeepalivePeriod = 25 * time.Second

// handleStreamSSE streams ActionEvents as Server-Sent Events, for clients that
// cannot use WebSockets. It replays events after the client's cursor, then
// streams new ones. Each event carries an id: line so EventSource resumes from
// the right place after reconnecting.
func (s *Server) handleStreamSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		respondError(w, r, http.StatusInternalServerError, "internal_error", "streaming unsupported")
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	// The stream is same-origin; the CORS middleware has already vetted Origin.

	after := parseAfterCursor(r)
	backlog, ch, contiguous := s.exec.Broadcast.SubscribeFrom(after)
	defer s.exec.Broadcast.Unsubscribe(ch)

	// Events after the cursor were dropped; tell the client to resync from
	// /jobs.
	if !contiguous {
		fmt.Fprint(w, "event: resync\ndata: {}\n\n")
	}
	for _, e := range backlog {
		if !writeSSEEvent(w, e) {
			return
		}
	}
	flusher.Flush()

	keepalive := time.NewTicker(sseKeepalivePeriod)
	defer keepalive.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			// Client disconnected; unsubscribe runs via defer.
			return
		case <-keepalive.C:
			// SSE comment line — ignored by clients, resets idle timers.
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		case e, open := <-ch:
			if !open {
				return
			}
			if !writeSSEEvent(w, e) {
				return
			}
			flusher.Flush()
		}
	}
}

// writeSSEEvent writes one ActionEvent as an SSE frame with an id: line (the
// event's Seq, which the browser echoes back as Last-Event-ID on reconnect).
// It returns false if the write failed, so the caller stops the stream.
func writeSSEEvent(w http.ResponseWriter, e executor.ActionEvent) bool {
	data, err := json.Marshal(e)
	if err != nil {
		return false
	}
	_, err = fmt.Fprintf(w, "id: %d\nevent: action\ndata: %s\n\n", e.Seq, data)
	return err == nil
}
