// SPDX-License-Identifier: Apache-2.0
package executor

import (
	"sync"
	"time"
)

// ActionEvent represents a single event in a command execution lifecycle.
type ActionEvent struct {
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

// Broadcaster fans out ActionEvents to all registered WebSocket listeners and
// records a bounded history of jobs derived from start/end events.
type Broadcaster struct {
	mu      sync.RWMutex
	clients map[chan ActionEvent]struct{}

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

// Subscribe returns a channel that receives all future ActionEvents.
func (b *Broadcaster) Subscribe() chan ActionEvent {
	ch := make(chan ActionEvent, 64)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()
	return ch
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

// Send broadcasts an event to all subscribers and updates the job history.
func (b *Broadcaster) Send(event ActionEvent) {
	b.recordJob(event)

	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.clients {
		select {
		case ch <- event:
		default:
			// Drop event if client is slow
		}
	}
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
