// SPDX-License-Identifier: Apache-2.0
package results

import (
	"testing"
	"time"
)

func makeStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(t.TempDir())
}

var now = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func TestAppend_And_All(t *testing.T) {
	s := makeStore(t)
	r1 := Record{Kind: KindIncident, Name: "oom-kill", StartedAt: now, EndedAt: now.Add(5 * time.Minute), Score: 80, Outcome: "resolved"}
	r2 := Record{Kind: KindChallenge, Name: "restore-broken-deploy", StartedAt: now.Add(10 * time.Minute), Score: 90, Outcome: "passed"}
	if err := s.Append(r1); err != nil {
		t.Fatalf("Append r1: %v", err)
	}
	if err := s.Append(r2); err != nil {
		t.Fatalf("Append r2: %v", err)
	}
	all, err := s.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 records, got %d", len(all))
	}
	if all[0].Kind != KindIncident {
		t.Errorf("first record kind = %q", all[0].Kind)
	}
}

func TestByKind(t *testing.T) {
	s := makeStore(t)
	_ = s.Append(Record{Kind: KindIncident, Name: "oom-kill", StartedAt: now})
	_ = s.Append(Record{Kind: KindChallenge, Name: "restore-broken-deploy", StartedAt: now.Add(time.Minute)})
	_ = s.Append(Record{Kind: KindModule, Name: "k8s-foundations/init", StartedAt: now.Add(2 * time.Minute)})

	incidents, err := s.ByKind(KindIncident)
	if err != nil {
		t.Fatalf("ByKind: %v", err)
	}
	if len(incidents) != 1 {
		t.Errorf("expected 1 incident, got %d", len(incidents))
	}

	challenges, err := s.ByKind(KindChallenge)
	if err != nil {
		t.Fatalf("ByKind challenge: %v", err)
	}
	if len(challenges) != 1 {
		t.Errorf("expected 1 challenge, got %d", len(challenges))
	}
}

func TestAll_Empty(t *testing.T) {
	s := makeStore(t)
	all, err := s.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if all != nil {
		t.Error("expected nil for empty store")
	}
}

func TestAll_ChronologicalOrder(t *testing.T) {
	s := makeStore(t)
	_ = s.Append(Record{Kind: KindIncident, Name: "b", StartedAt: now.Add(time.Hour)})
	_ = s.Append(Record{Kind: KindIncident, Name: "a", StartedAt: now})
	all, _ := s.All()
	if all[0].Name != "a" {
		t.Errorf("expected chronological order, first = %q", all[0].Name)
	}
}

func TestProgress_GroupsByPath(t *testing.T) {
	s := makeStore(t)
	_ = s.Append(Record{Kind: KindModule, Name: "k8s/init", StartedAt: now})
	_ = s.Append(Record{Kind: KindModule, Name: "k8s/deploy", StartedAt: now.Add(time.Minute)})
	_ = s.Append(Record{Kind: KindModule, Name: "other/step1", StartedAt: now.Add(2 * time.Minute)})
	_ = s.Append(Record{Kind: KindIncident, Name: "oom-kill", StartedAt: now.Add(3 * time.Minute)}) // should be excluded

	progress, err := s.Progress()
	if err != nil {
		t.Fatalf("Progress: %v", err)
	}
	if len(progress["k8s"]) != 2 {
		t.Errorf("expected 2 k8s module records, got %d", len(progress["k8s"]))
	}
	if len(progress["other"]) != 1 {
		t.Errorf("expected 1 other module record, got %d", len(progress["other"]))
	}
	if _, ok := progress["k8s"]; !ok {
		t.Error("expected k8s path in progress")
	}
}

func TestAppend_Idempotent_FileCreation(t *testing.T) {
	s := makeStore(t)
	// Append twice — should not create duplicate entries if path changes mid-run.
	_ = s.Append(Record{Kind: KindChallenge, Name: "c1", StartedAt: now})
	_ = s.Append(Record{Kind: KindChallenge, Name: "c2", StartedAt: now.Add(time.Minute)})
	all, _ := s.All()
	if len(all) != 2 {
		t.Errorf("expected 2, got %d", len(all))
	}
}
