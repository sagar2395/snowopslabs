// SPDX-License-Identifier: Apache-2.0
package executor

import (
	"sync"
	"time"
)

// ActionEvent represents a single event in a command execution lifecycle.
//
// Seq is a monotonic, per-broadcaster sequence number assigned at Send time. It
// is the cursor clients use to resume a dropped stream: a reconnecting client
// asks for everything after the last Seq it saw, so no event is missed and none
// is replayed twice. Callers of Send leave it zero; the broadcaster stamps it.
type ActionEvent struct {
	Seq       int64     `json:"seq"`
	ID        string    `json:"id"`
	Type      string    `json:"type"`    // action_start, action_output, action_error, action_end
	Action    string    `json:"action"`  // Human-readable label, e.g., "Deploy go-api"
	Command   string    `json:"command"` // The actual command being run
	Output    string    `json:"output,omitempty"`
	Stream    string    `json:"stream,omitempty"` // stdout or stderr
	ExitCode  *int      `json:"exitCode,omitempty"`
	Error     string    `json:"error,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// JobInfo is the recorded lifecycle of one action, kept so HTTP clients can
// recover job state after a page reload or a dropped WebSocket connection.
type JobInfo struct {
	ID        string     `json:"id"`
	Action    string     `json:"action"`
	Status    string     `json:"status"` // running, succeeded, failed
	Error     string     `json:"error,omitempty"`
	StartedAt time.Time  `json:"startedAt"`
	EndedAt   *time.Time `json:"endedAt,omitempty"`
}

// maxJobHistory bounds the in-memory job history.
const maxJobHistory = 100

// eventRingCap bounds the replay buffer of recent events. A reconnecting client
// can resume without gaps as long as its last-seen Seq is still in the ring; a
// client that was gone long enough to fall off the ring is told to resync from
// the job history instead (see the stream handler).
const eventRingCap = 1024

// Broadcaster fans out ActionEvents to all registered listeners, stamps each
// with a monotonic Seq, keeps a bounded replay ring so dropped clients can
// resume, and records a bounded history of jobs derived from start/end events.
type Broadcaster struct {
	mu      sync.Mutex
	clients map[chan ActionEvent]struct{}
	seq     int64         // last assigned sequence number
	ring    []ActionEvent // recent events, oldest first, capped at eventRingCap

	jobsMu   sync.Mutex
	jobs     map[string]*JobInfo
	jobOrder []string // insertion order, oldest first
}

// NewBroadcaster creates a new Broadcaster.
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{
		clients: make(map[chan ActionEvent]struct{}),
		jobs:    make(map[string]*JobInfo),
	}
}

// Subscribe returns a channel that receives all future ActionEvents. It is the
// backward-compatible, future-only subscription; use SubscribeFrom to also
// replay recent history.
func (b *Broadcaster) Subscribe() chan ActionEvent {
	ch := make(chan ActionEvent, 256)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

// SubscribeFrom atomically returns the backlog of buffered events with Seq
// greater than after, plus a channel of all subsequent events. Because Send and
// SubscribeFrom take the same lock, no event can slip between the backlog
// snapshot and the channel registration: the two together are gap-free and
// duplicate-free. A caller resuming a stream passes its last-seen Seq as after;
// a fresh client passes 0 to replay whatever the ring still holds.
//
// The bool reports whether the requested cursor is still fully covered by the
// ring. It is false when after points before the oldest buffered event (the
// client was gone too long), so the caller can tell the client to resync rather
// than silently starting mid-history.
func (b *Broadcaster) SubscribeFrom(after int64) (backlog []ActionEvent, ch chan ActionEvent, contiguous bool) {
	ch = make(chan ActionEvent, 256)
	b.mu.Lock()
	defer b.mu.Unlock()

	contiguous = true
	if len(b.ring) > 0 {
		oldest := b.ring[0].Seq
		// after+1 is the first event we owe the client; if that predates the
		// ring, there is an unrecoverable gap.
		if after+1 < oldest {
			contiguous = false
		}
		for _, e := range b.ring {
			if e.Seq > after {
				backlog = append(backlog, e)
			}
		}
	}
	// When the ring is empty there is nothing to replay; the client simply
	// receives future events on ch, so contiguous stays true.

	b.clients[ch] = struct{}{}
	return backlog, ch, contiguous
}

// Unsubscribe removes a listener channel. Safe to call more than once.
func (b *Broadcaster) Unsubscribe(ch chan ActionEvent) {
	b.mu.Lock()
	if _, ok := b.clients[ch]; ok {
		delete(b.clients, ch)
		close(ch)
	}
	b.mu.Unlock()
}

// Send stamps the event with the next Seq, buffers it for replay, fans it out to
// all subscribers, and updates the job history.
func (b *Broadcaster) Send(event ActionEvent) {
	b.mu.Lock()
	b.seq++
	event.Seq = b.seq
	b.ring = append(b.ring, event)
	if len(b.ring) > eventRingCap {
		b.ring = b.ring[len(b.ring)-eventRingCap:]
	}
	for ch := range b.clients {
		select {
		case ch <- event:
		default:
			// Drop for a slow live consumer; it can recover the gap on
			// reconnect via SubscribeFrom while the event is still in the ring.
		}
	}
	b.mu.Unlock()

	// recordJob takes a different lock; keep it outside b.mu to avoid ordering
	// concerns. Job history is independent of the live stream.
	b.recordJob(event)
}

// Jobs returns the recorded jobs, newest first.
func (b *Broadcaster) Jobs() []JobInfo {
	b.jobsMu.Lock()
	defer b.jobsMu.Unlock()
	out := make([]JobInfo, 0, len(b.jobOrder))
	for i := len(b.jobOrder) - 1; i >= 0; i-- {
		if j, ok := b.jobs[b.jobOrder[i]]; ok {
			out = append(out, *j)
		}
	}
	return out
}

func (b *Broadcaster) recordJob(event ActionEvent) {
	if event.ID == "" {
		return
	}
	b.jobsMu.Lock()
	defer b.jobsMu.Unlock()

	switch event.Type {
	case "action_start":
		if _, exists := b.jobs[event.ID]; exists {
			return
		}
		b.jobs[event.ID] = &JobInfo{
			ID:        event.ID,
			Action:    event.Action,
			Status:    "running",
			StartedAt: event.Timestamp,
		}
		b.jobOrder = append(b.jobOrder, event.ID)
		// Evict oldest entries beyond the cap.
		for len(b.jobOrder) > maxJobHistory {
			delete(b.jobs, b.jobOrder[0])
			b.jobOrder = b.jobOrder[1:]
		}
	case "action_end", "action_error":
		j, ok := b.jobs[event.ID]
		if !ok {
			return
		}
		ended := event.Timestamp
		j.EndedAt = &ended
		failed := event.Error != "" || (event.ExitCode != nil && *event.ExitCode != 0)
		if failed {
			j.Status = "failed"
			j.Error = event.Error
		} else {
			j.Status = "succeeded"
		}
	}
}
