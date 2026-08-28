// SPDX-License-Identifier: Apache-2.0
package httpapi

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sagar2395/snowopslabs/internal/config"
	"github.com/sagar2395/snowopslabs/internal/executor"
)

func TestParseAfterCursor(t *testing.T) {
	mk := func(query, lastEventID string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/v2/stream?"+query, nil)
		if lastEventID != "" {
			r.Header.Set("Last-Event-ID", lastEventID)
		}
		return r
	}
	cases := []struct {
		name  string
		query string
		leid  string
		want  int64
	}{
		{"nothing", "", "", 0},
		{"after param", "after=7", "", 7},
		{"last-event-id wins over after", "after=7", "12", 12},
		{"bad after", "after=nope", "", 0},
		{"negative rejected", "after=-3", "", 0},
	}
	for _, c := range cases {
		if got := parseAfterCursor(mk(c.query, c.leid)); got != c.want {
			t.Errorf("%s: parseAfterCursor = %d, want %d", c.name, got, c.want)
		}
	}
}

// The SSE endpoint must replay events after the cursor as proper SSE frames
// (id: <seq> / event: action / data: <json>), so an EventSource resumes cleanly.
func TestStreamSSE_ReplaysFromCursor(t *testing.T) {
	exec := executor.New(t.TempDir())
	s := &Server{exec: exec, cfg: &config.Config{ProjectRoot: t.TempDir()}}
	s.setupRoutes()
	ts := httptest.NewServer(s.router)
	defer ts.Close()

	// Seed four events into the ring before anyone connects.
	for i := 1; i <= 4; i++ {
		exec.Broadcast.Send(executor.ActionEvent{ID: fmt.Sprintf("%d", i), Type: "action_output", Output: "line"})
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v2/stream?after=2", nil)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("connect SSE: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type: got %q, want text/event-stream", ct)
	}

	// Read the replayed frames (seq 3 and 4) with a hard deadline so a bug can't
	// hang the test, then cancel to release the long-lived stream.
	lines := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()

	var collected []string
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ln := <-lines:
			collected = append(collected, ln)
			// Once we've seen the last replayed id, we're done.
			if ln == "id: 4" {
				cancel()
				joined := strings.Join(collected, "\n")
				if !strings.Contains(joined, "id: 3") {
					t.Fatalf("missing replayed frame id: 3 in:\n%s", joined)
				}
				if !strings.Contains(joined, "event: action") {
					t.Fatalf("frames missing event: action in:\n%s", joined)
				}
				if strings.Contains(joined, "id: 2") {
					t.Fatalf("cursor after=2 should not replay seq 2:\n%s", joined)
				}
				return
			}
		case <-deadline:
			t.Fatalf("timed out; collected:\n%s", strings.Join(collected, "\n"))
		}
	}
}

// When the client's cursor has fallen off the replay ring, the SSE stream must
// lead with a `resync` event so the client reloads from /jobs rather than
// silently starting mid-history.
func TestStreamSSE_ResyncWhenCursorTooOld(t *testing.T) {
	exec := executor.New(t.TempDir())
	s := &Server{exec: exec, cfg: &config.Config{ProjectRoot: t.TempDir()}}
	s.setupRoutes()
	ts := httptest.NewServer(s.router)
	defer ts.Close()

	// Overfill the ring so cursor 0 can no longer be honoured contiguously.
	for i := 0; i < 1050; i++ {
		exec.Broadcast.Send(executor.ActionEvent{ID: "x", Type: "action_output"})
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v2/stream?after=0", nil)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("connect SSE: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	lines := make(chan string, 8)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
	}()

	deadline := time.After(3 * time.Second)
	for {
		select {
		case ln := <-lines:
			if ln == "event: resync" {
				cancel()
				return
			}
		case <-deadline:
			t.Fatal("expected a leading `event: resync` frame, never saw one")
		}
	}
}
