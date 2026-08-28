// SPDX-License-Identifier: Apache-2.0

package run

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/sagar2395/snowopslabs/internal/store"
)

func TestFinishHook_FiresWithTerminalRecord(t *testing.T) {
	var (
		mu   sync.Mutex
		seen []store.Run
	)
	hook := func(_ context.Context, r store.Run) {
		mu.Lock()
		seen = append(seen, r)
		mu.Unlock()
	}

	h := newHarness(t, WithFinishHook(hook))
	script := h.script("runtimes/k3d/up.sh")
	id, err := h.engine.Submit(context.Background(), Spec{
		Kind: "lab.up", Target: "k3d", LockKey: "lab", Script: script,
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	h.waitForStatus(id, store.StatusSucceeded)

	// The hook runs inside the worker before the run is "done"; a brief poll
	// covers the gap between the store transition and the hook returning.
	deadline := waitUntil(func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(seen) == 1
	})
	if !deadline {
		t.Fatal("finish hook did not fire")
	}
	mu.Lock()
	rec := seen[0]
	mu.Unlock()
	if rec.ID != id || rec.Status != store.StatusSucceeded {
		t.Errorf("hook record = %+v, want %s succeeded", rec, id)
	}
	// The hook must receive the FULL record, including fields finish() does not
	// carry directly — target and lock key — so a recorder can act on them.
	if rec.Target != "k3d" || rec.LockKey != "lab" {
		t.Errorf("hook record missing target/lockkey: %+v", rec)
	}
}

func TestFinishHook_FiresOnFailure(t *testing.T) {
	var (
		mu    sync.Mutex
		count int
		last  store.Status
	)
	hook := func(_ context.Context, r store.Run) {
		mu.Lock()
		count++
		last = r.Status
		mu.Unlock()
	}
	h := newHarness(t, WithFinishHook(hook))
	h.fake.WhenArgsContain("up.sh", "", 1) // exit non-zero
	script := h.script("runtimes/k3d/up.sh")
	id, err := h.engine.Submit(context.Background(), Spec{Kind: "lab.up", Script: script})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	h.waitForStatus(id, store.StatusFailed)

	if !waitUntil(func() bool { mu.Lock(); defer mu.Unlock(); return count == 1 }) {
		t.Fatal("finish hook did not fire on failure")
	}
	mu.Lock()
	defer mu.Unlock()
	if last != store.StatusFailed {
		t.Errorf("hook status = %q, want failed", last)
	}
}

func waitUntil(cond func() bool) bool {
	for i := 0; i < 200; i++ {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}
