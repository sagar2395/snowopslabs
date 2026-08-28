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

// parseAfterCursor extracts a resume cursor from a stream request. WebSocket
// clients pass it as ?after=<seq>; SSE clients additionally get the standard
// Last-Event-ID header for free on reconnect, which takes precedence. A missing
// or unparseable value means "from the start of the ring" (0).
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

// sseKeepalivePeriod bounds how long the SSE stream sits idle before emitting a
// comment line, keeping intermediaries from timing the connection out.
const sseKeepalivePeriod = 25 * time.Second

// handleStreamSSE streams ActionEvents as Server-Sent Events — the fallback for
// environments where WebSockets are blocked. It shares the broadcaster's
// cursor semantics with the WebSocket path: ?after=<seq> (or a Last-Event-ID
// header on reconnect) replays missed events from the ring, then live events
// follow, each written with an SSE id: line so the browser's EventSource
// resumes from the right place automatically.
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

	// A cursor that fell off the ring: tell the client to resync from /jobs.
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
