// SPDX-License-Identifier: Apache-2.0

package executor

import (
	"slices"
	"sync"
	"time"
)

// ActionEvent represents a single event in a command execution lifecycle.
//
// Seq is an increasing number the Broadcaster assigns in Send; callers leave
// it zero. A reconnecting client asks for everything after the last Seq it saw,
// so it misses nothing and sees nothing twice.
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

// JobInfo records the lifecycle of one action, so HTTP clients can recover job
// state after a page reload or a dropped WebSocket connection.
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

// eventRingCap is the number of recent events kept for replay. A client whose
// last-seen Seq has dropped out of this buffer is told to resync from the job
// history instead.
const eventRingCap = 1024

// Broadcaster sends each ActionEvent to every listener. It numbers events with
// Seq, keeps recent events so reconnecting clients can catch up, and keeps a
// short history of jobs built from their start and end events.
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

// Subscribe returns a channel that receives all future ActionEvents. Use
// SubscribeFrom to also receive recent past events.
func (b *Broadcaster) Subscribe() chan ActionEvent {
	ch := make(chan ActionEvent, 256)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

// SubscribeFrom returns the buffered events with Seq greater than after, plus
// a channel for every later event. It holds the same lock as Send, so together
// they contain every event exactly once. Pass the last Seq a client saw to
// resume, or 0 to get everything still buffered.
//
// The bool is false when events after `after` have already been dropped from
// the buffer; the caller should then tell the client to resync.
func (b *Broadcaster) SubscribeFrom(after int64) (backlog []ActionEvent, ch chan ActionEvent, contiguous bool) {
	ch = make(chan ActionEvent, 256)
	b.mu.Lock()
	defer b.mu.Unlock()

	contiguous = true
	if len(b.ring) > 0 {
		oldest := b.ring[0].Seq
		// The client needs after+1 onwards; if that is older than the buffer,
		// some events are lost.
		if after+1 < oldest {
			contiguous = false
		}
		for _, e := range b.ring {
			if e.Seq > after {
				backlog = append(backlog, e)
			}
		}
	}

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
			// Skip a listener that is not keeping up; it can recover the event
			// with SubscribeFrom while it is still buffered.
		}
	}
	b.mu.Unlock()

	// recordJob takes its own lock; calling it outside b.mu avoids holding
	// two locks at once.
	b.recordJob(event)
}

// Jobs returns the recorded jobs, newest first.
func (b *Broadcaster) Jobs() []JobInfo {
	b.jobsMu.Lock()
	defer b.jobsMu.Unlock()
	out := make([]JobInfo, 0, len(b.jobOrder))
	for _, id := range slices.Backward(b.jobOrder) {
		if j, ok := b.jobs[id]; ok {
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
