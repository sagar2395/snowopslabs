// SPDX-License-Identifier: Apache-2.0

package run

import (
	"sync"

	"github.com/sagar2395/snowopslabs/internal/store"
)

// EventType distinguishes what changed.
type EventType string

const (
	// EventLog reports that new log lines are readable up to Seq.
	EventLog EventType = "log"
	// EventStep reports that a new step began.
	EventStep EventType = "step"
	// EventStatus reports a lifecycle transition.
	EventStatus EventType = "status"
)

// Event is a notification, deliberately not a data carrier.
//
// This is the key design choice behind ADR-0006. Events say "there is new data
// up to sequence N"; the actual lines are read from the store by cursor. So a
// consumer that is slow, or briefly disconnected, cannot lose output — it just
// reads from where it left off. v1 put the data in the channel and dropped it
// when the channel was full, which is how log lines silently vanished.
type Event struct {
	Type   EventType
	RunID  string
	Seq    int64        // highest readable log sequence (EventLog)
	Step   string       // step name (EventStep)
	Status store.Status // new status (EventStatus)
}

// subscribers fans events out to watchers.
type subscribers struct {
	mu     sync.RWMutex
	next   int
	chans  map[int]chan Event
	filter map[int]string // run ID a subscriber cares about; empty means all
	closed bool
}

func newSubscribers() *subscribers {
	return &subscribers{chans: make(map[int]chan Event), filter: make(map[int]string)}
}

// subscribeBuffer is generous because events are tiny and losing one costs a
// consumer latency (it waits for the next) rather than data.
const subscribeBuffer = 256

// Subscribe returns a channel of events and a function to stop listening.
// runID filters to one run; empty subscribes to everything.
//
// The unsubscribe function is safe to call more than once and must be called —
// typically by defer — or the subscriber leaks.
func (e *Engine) Subscribe(runID string) (<-chan Event, func()) {
	return e.subs.subscribe(runID)
}

func (s *subscribers) subscribe(runID string) (<-chan Event, func()) {
	ch := make(chan Event, subscribeBuffer)

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		close(ch)
		return ch, func() {}
	}
	id := s.next
	s.next++
	s.chans[id] = ch
	s.filter[id] = runID
	s.mu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			if c, ok := s.chans[id]; ok {
				delete(s.chans, id)
				delete(s.filter, id)
				close(c)
			}
		})
	}
}

// publish delivers an event to interested subscribers.
//
// Delivery is non-blocking: a subscriber that is not draining its channel is
// skipped rather than stalling the run engine. That is safe precisely because
// events carry no data — a skipped notification costs latency, and the next
// event (or a direct read from the store) brings the consumer back up to date.
func (s *subscribers) publish(ev Event) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return
	}
	for id, ch := range s.chans {
		if want := s.filter[id]; want != "" && want != ev.RunID {
			continue
		}
		select {
		case ch <- ev:
		default:
		}
	}
}

// closeAll shuts every subscriber down. Called on engine shutdown so watchers
// see the stream end rather than hanging.
func (s *subscribers) closeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	for id, ch := range s.chans {
		delete(s.chans, id)
		delete(s.filter, id)
		close(ch)
	}
}
