// SPDX-License-Identifier: Apache-2.0

package run

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/sagar2395/snowopslabs/internal/store"
)

// fakeMetrics is a thread-safe run.Metrics that records what it was told.
type fakeMetrics struct {
	mu       sync.Mutex
	started  int
	finished []finishedRun
}

type finishedRun struct {
	kind, status string
	dur          time.Duration
}

func (m *fakeMetrics) RunStarted() {
	m.mu.Lock()
	m.started++
	m.mu.Unlock()
}

func (m *fakeMetrics) RunFinished(kind, status string, dur time.Duration) {
	m.mu.Lock()
	m.finished = append(m.finished, finishedRun{kind, status, dur})
	m.mu.Unlock()
}

func (m *fakeMetrics) snapshot() (int, []finishedRun) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.started, append([]finishedRun(nil), m.finished...)
}

// waitForFinished polls until the sink has recorded n terminal runs. RunFinished
// fires just after the store records the terminal status, so a test that has
// only waited on the store status must give this a moment to land.
func (m *fakeMetrics) waitForFinished(t *testing.T, n int) []finishedRun {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, fin := m.snapshot(); len(fin) >= n {
			return fin
		}
		time.Sleep(5 * time.Millisecond)
	}
	_, fin := m.snapshot()
	t.Fatalf("metrics recorded %d finished runs, want %d", len(fin), n)
	return nil
}

func TestEngineMetrics_RecordsLifecycle(t *testing.T) {
	ctx := context.Background()
	m := &fakeMetrics{}
	h := newHarness(t, WithMetrics(m))

	// A run that succeeds.
	okID, err := h.engine.Submit(ctx, Spec{Kind: "scenario.activate", Script: h.script("ok.sh")})
	if err != nil {
		t.Fatalf("Submit ok: %v", err)
	}
	h.waitForStatus(okID, store.StatusSucceeded)

	// A run that fails (its script exits non-zero).
	failScript := h.script("fail.sh")
	h.fake.WhenArgsContain("fail.sh", "", 1)
	failID, err := h.engine.Submit(ctx, Spec{Kind: "platform.install", Script: failScript})
	if err != nil {
		t.Fatalf("Submit fail: %v", err)
	}
	h.waitForStatus(failID, store.StatusFailed)

	fin := m.waitForFinished(t, 2)

	started, _ := m.snapshot()
	if started != 2 {
		t.Errorf("RunStarted called %d times, want 2", started)
	}
	// In-flight must balance: every start has a matching finish.
	if len(fin) != started {
		t.Errorf("started %d != finished %d (in-flight would not return to zero)", started, len(fin))
	}

	byKind := map[string]finishedRun{}
	for _, f := range fin {
		byKind[f.kind] = f
	}
	if got := byKind["scenario.activate"]; got.status != "succeeded" {
		t.Errorf("scenario.activate status = %q, want succeeded", got.status)
	}
	if got := byKind["platform.install"]; got.status != "failed" {
		t.Errorf("platform.install status = %q, want failed", got.status)
	}
}

func TestEngineMetrics_NilIsNoop(t *testing.T) {
	// The default engine (no WithMetrics) must run fine with a nil sink.
	ctx := context.Background()
	h := newHarness(t)
	id, err := h.engine.Submit(ctx, Spec{Kind: "lab.up", Script: h.script("ok.sh")})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	h.waitForStatus(id, store.StatusSucceeded)
}
