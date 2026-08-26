// SPDX-License-Identifier: Apache-2.0
package executor

import (
	"fmt"
	"testing"
	"time"
)

func intPtr(i int) *int { return &i }

func TestBroadcaster_JobLifecycle(t *testing.T) {
	b := NewBroadcaster()

	b.Send(ActionEvent{ID: "action-1", Type: "action_start", Action: "Deploy go-api", Timestamp: time.Now()})

	jobs := b.Jobs()
	if len(jobs) != 1 {
		t.Fatalf("jobs: got %d, want 1", len(jobs))
	}
	if jobs[0].Status != "running" {
		t.Errorf("status: got %q, want running", jobs[0].Status)
	}

	b.Send(ActionEvent{ID: "action-1", Type: "action_end", Action: "Deploy go-api", ExitCode: intPtr(0), Timestamp: time.Now()})
	jobs = b.Jobs()
	if jobs[0].Status != "succeeded" {
		t.Errorf("status after end: got %q, want succeeded", jobs[0].Status)
	}
	if jobs[0].EndedAt == nil {
		t.Error("expected EndedAt to be set")
	}
}

func TestBroadcaster_JobFailure(t *testing.T) {
	b := NewBroadcaster()
	b.Send(ActionEvent{ID: "action-1", Type: "action_start", Action: "Build x", Timestamp: time.Now()})
	b.Send(ActionEvent{ID: "action-1", Type: "action_end", ExitCode: intPtr(2), Error: "exit status 2", Timestamp: time.Now()})

	jobs := b.Jobs()
	if jobs[0].Status != "failed" {
		t.Errorf("status: got %q, want failed", jobs[0].Status)
	}
	if jobs[0].Error != "exit status 2" {
		t.Errorf("error: got %q", jobs[0].Error)
	}
}

func TestBroadcaster_JobsNewestFirst(t *testing.T) {
	b := NewBroadcaster()
	b.Send(ActionEvent{ID: "action-1", Type: "action_start", Timestamp: time.Now()})
	b.Send(ActionEvent{ID: "action-2", Type: "action_start", Timestamp: time.Now()})

	jobs := b.Jobs()
	if len(jobs) != 2 || jobs[0].ID != "action-2" || jobs[1].ID != "action-1" {
		t.Errorf("expected newest first, got %+v", jobs)
	}
}

func TestBroadcaster_JobHistoryBounded(t *testing.T) {
	b := NewBroadcaster()
	for i := 0; i < maxJobHistory+25; i++ {
		b.Send(ActionEvent{ID: fmt.Sprintf("action-%d", i), Type: "action_start", Timestamp: time.Now()})
	}
	if got := len(b.Jobs()); got != maxJobHistory {
		t.Errorf("history size: got %d, want %d", got, maxJobHistory)
	}
}

func TestBroadcaster_OutputEventsDoNotCreateJobs(t *testing.T) {
	b := NewBroadcaster()
	b.Send(ActionEvent{ID: "action-1", Type: "action_output", Output: "line", Timestamp: time.Now()})
	if got := len(b.Jobs()); got != 0 {
		t.Errorf("jobs: got %d, want 0", got)
	}
}

func TestBroadcaster_UnsubscribeTwiceSafe(t *testing.T) {
	b := NewBroadcaster()
	ch := b.Subscribe()
	b.Unsubscribe(ch)
	// Must not panic on double unsubscribe.
	b.Unsubscribe(ch)
}
