// SPDX-License-Identifier: Apache-2.0
package catalog

import (
	"strings"
	"sync"
	"testing"
)

func TestStore_CurrentAndReload(t *testing.T) {
	root := validRoot(t)
	s, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if s.Current() == nil {
		t.Fatal("Current() is nil after NewStore")
	}
	if n := s.Current().Counts()[KindScenario]; n != 1 {
		t.Fatalf("scenario count = %d, want 1", n)
	}

	// Add a second scenario, then reload; the swap must pick it up.
	write(t, root, "scenarios", "s2", "scenario.yaml", strings.Replace(validScenario, "name: s1", "name: s2", 1))
	c, err := s.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if n := c.Counts()[KindScenario]; n != 2 {
		t.Fatalf("after reload scenario count = %d, want 2", n)
	}
	if n := s.Current().Counts()[KindScenario]; n != 2 {
		t.Fatalf("Current after reload = %d, want 2", n)
	}
}

// TestStore_ConcurrentReadDuringReload exercises the atomic swap under the race
// detector: readers must always see a complete snapshot while Reload runs.
func TestStore_ConcurrentReadDuringReload(t *testing.T) {
	s, err := NewStore(validRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				c := s.Current()
				if c == nil {
					t.Error("Current() returned nil during reload")
					return
				}
				_ = c.Counts()
				_ = c.Scenarios()
			}
		}()
	}
	for i := 0; i < 20; i++ {
		if _, err := s.Reload(); err != nil {
			t.Error(err)
		}
	}
	wg.Wait()
}

func TestStore_Roots(t *testing.T) {
	r1, r2 := t.TempDir(), t.TempDir()
	s, err := NewStore(r1, r2)
	if err != nil {
		t.Fatal(err)
	}
	got := s.Roots()
	if len(got) != 2 || got[0] != r1 || got[1] != r2 {
		t.Errorf("Roots() = %v, want [%s %s]", got, r1, r2)
	}
	// The returned slice must be a copy — mutating it must not affect the store.
	got[0] = "tampered"
	if s.Roots()[0] != r1 {
		t.Error("Roots() leaked the internal slice")
	}
}
