// SPDX-License-Identifier: Apache-2.0
package executor

import (
	"testing"
	"time"
)

// drain reads up to n events from ch, giving up after a short timeout so a bug
// that starves the channel fails fast instead of hanging.
func drain(t *testing.T, ch chan ActionEvent, n int) []ActionEvent {
	t.Helper()
	var out []ActionEvent
	for len(out) < n {
		select {
		case e := <-ch:
			out = append(out, e)
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for event %d of %d", len(out)+1, n)
		}
	}
	return out
}

func TestBroadcaster_SeqIsMonotonic(t *testing.T) {
	b := NewBroadcaster()
	ch := b.Subscribe()
	defer b.Unsubscribe(ch)

	for i := 0; i < 5; i++ {
		b.Send(ActionEvent{ID: "x", Type: "action_output"})
	}
	events := drain(t, ch, 5)
	for i, e := range events {
		if e.Seq != int64(i+1) {
			t.Fatalf("event %d has Seq %d, want %d (monotonic from 1)", i, e.Seq, i+1)
		}
	}
}

// The core reconnect guarantee: a client that drops after Seq N and resumes with
// SubscribeFrom(N) sees every later event exactly once — the backlog fills the
// gap and the live channel carries on, with no overlap at the seam.
func TestBroadcaster_ResumeIsGapFreeAndNoDuplicates(t *testing.T) {
	b := NewBroadcaster()

	chA := b.Subscribe()
	b.Send(ActionEvent{ID: "1", Type: "action_output"})
	b.Send(ActionEvent{ID: "2", Type: "action_output"})
	seen := drain(t, chA, 2)
	lastSeq := seen[len(seen)-1].Seq
	b.Unsubscribe(chA)

	// Events the client misses while disconnected.
	b.Send(ActionEvent{ID: "3", Type: "action_output"})
	b.Send(ActionEvent{ID: "4", Type: "action_output"})

	backlog, chA2, contiguous := b.SubscribeFrom(lastSeq)
	defer b.Unsubscribe(chA2)
	if !contiguous {
		t.Fatal("cursor is still in the ring; want contiguous")
	}

	// One more event arrives live after resubscription.
	b.Send(ActionEvent{ID: "5", Type: "action_output"})
	live := drain(t, chA2, 1)

	// Assemble the full sequence the client observed across the reconnect.
	all := append([]ActionEvent{}, seen...)
	all = append(all, backlog...)
	all = append(all, live...)

	if len(all) != 5 {
		t.Fatalf("observed %d events across reconnect, want 5", len(all))
	}
	for i, e := range all {
		if e.Seq != int64(i+1) {
			t.Fatalf("seam not gap-free/dup-free: position %d has Seq %d, want %d (full=%v)",
				i, e.Seq, i+1, seqs(all))
		}
	}
}

func TestBroadcaster_NonContiguousWhenCursorFellOffRing(t *testing.T) {
	b := NewBroadcaster()
	total := eventRingCap + 10
	for i := 0; i < total; i++ {
		b.Send(ActionEvent{ID: "x", Type: "action_output"})
	}

	backlog, ch, contiguous := b.SubscribeFrom(0)
	defer b.Unsubscribe(ch)

	if contiguous {
		t.Error("cursor 0 predates the ring after eviction; want non-contiguous")
	}
	wantOldest := int64(total - eventRingCap + 1)
	if len(backlog) == 0 || backlog[0].Seq != wantOldest {
		t.Fatalf("backlog should start at the oldest retained event Seq %d, got %v", wantOldest, seqs(backlog))
	}
}

func seqs(events []ActionEvent) []int64 {
	out := make([]int64, len(events))
	for i, e := range events {
		out[i] = e.Seq
	}
	return out
}
