// SPDX-License-Identifier: Apache-2.0
package lab

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func sampleSnapshot(name string) *Snapshot {
	return &Snapshot{
		Name:         name,
		TakenAt:      time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC),
		Profile:      "k3d",
		DomainSuffix: "k3d.local",
		Platform:     []string{"monitoring/metrics", "ingress/traefik", "monitoring/grafana"},
		Apps:         []string{"go-api"},
		Scenarios:    []string{"observability-sre"},
	}
}

func TestStore_SaveLoadRoundTrip(t *testing.T) {
	st := NewStore(t.TempDir())
	want := sampleSnapshot("before-gameday")

	if err := st.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := st.Load("before-gameday")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", got, want)
	}
}

func TestStore_SaveOverwrites(t *testing.T) {
	st := NewStore(t.TempDir())
	first := sampleSnapshot("snap")
	if err := st.Save(first); err != nil {
		t.Fatal(err)
	}
	second := sampleSnapshot("snap")
	second.Apps = []string{"go-api", "echo-server"}
	if err := st.Save(second); err != nil {
		t.Fatal(err)
	}
	got, _ := st.Load("snap")
	if len(got.Apps) != 2 {
		t.Fatalf("expected overwrite, got apps %v", got.Apps)
	}
}

func TestStore_InvalidNames(t *testing.T) {
	st := NewStore(t.TempDir())
	for _, bad := range []string{"", "../escape", "name with spaces", strings.Repeat("a", 65)} {
		if err := st.Save(&Snapshot{Name: bad}); err == nil {
			t.Errorf("Save(%q): expected error", bad)
		}
		if _, err := st.Load(bad); err == nil {
			t.Errorf("Load(%q): expected error", bad)
		}
	}
}

func TestStore_LoadMissing(t *testing.T) {
	st := NewStore(t.TempDir())
	if _, err := st.Load("ghost"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

func TestStore_ListNewestFirstAndSkipsCorrupt(t *testing.T) {
	root := t.TempDir()
	st := NewStore(root)

	old := sampleSnapshot("old")
	old.TakenAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	recent := sampleSnapshot("recent")
	recent.TakenAt = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if err := st.Save(old); err != nil {
		t.Fatal(err)
	}
	if err := st.Save(recent); err != nil {
		t.Fatal(err)
	}
	// A corrupt file must be skipped, not break listing.
	os.WriteFile(filepath.Join(st.Dir, "broken.yaml"), []byte("{{{"), 0644)

	got, err := st.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 || got[0].Name != "recent" || got[1].Name != "old" {
		names := []string{}
		for _, s := range got {
			names = append(names, s.Name)
		}
		t.Fatalf("expected [recent old], got %v", names)
	}
}

func TestStore_ListEmptyDirIsNotError(t *testing.T) {
	st := NewStore(t.TempDir()) // .labctl/snapshots never created
	got, err := st.List()
	if err != nil || got != nil {
		t.Fatalf("expected (nil, nil) for missing dir, got (%v, %v)", got, err)
	}
}

func TestStore_Delete(t *testing.T) {
	st := NewStore(t.TempDir())
	if err := st.Save(sampleSnapshot("snap")); err != nil {
		t.Fatal(err)
	}
	if err := st.Delete("snap"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := st.Load("snap"); err == nil {
		t.Fatal("snapshot should be gone")
	}
	if err := st.Delete("snap"); err == nil {
		t.Fatal("deleting a missing snapshot should error")
	}
}

func TestRestorePlan_Ordering(t *testing.T) {
	plan := RestorePlan(sampleSnapshot("snap"))

	var steps []string
	for _, a := range plan {
		steps = append(steps, a.String())
	}
	want := []string{
		"platform-install ingress/traefik", // ingress before monitoring
		"platform-install monitoring/grafana",
		"platform-install monitoring/metrics",
		"app-deploy go-api",             // apps before scenarios
		"scenario-up observability-sre", // scenarios last
	}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("restore plan:\n got %v\nwant %v", steps, want)
	}
}

func TestResetPlan_TearsDownInReverseAndKeepsIngress(t *testing.T) {
	plan := ResetPlan(
		[]string{"ingress/traefik", "monitoring/metrics", "gitops/argocd"},
		[]string{"go-api"},
		[]string{"observability-sre"},
	)

	var steps []string
	for _, a := range plan {
		steps = append(steps, a.String())
	}
	want := []string{
		"traffic-stop",
		"scenario-down observability-sre",
		"app-destroy go-api",
		"platform-uninstall gitops/argocd",      // non-priority categories first
		"platform-uninstall monitoring/metrics", // then monitoring
		// ingress/traefik deliberately absent
	}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("reset plan:\n got %v\nwant %v", steps, want)
	}
	for _, a := range plan {
		if a.Target == "ingress/traefik" {
			t.Fatal("reset must never uninstall ingress")
		}
	}
}

func TestResetPlan_EmptyLabStillStopsTraffic(t *testing.T) {
	plan := ResetPlan(nil, nil, nil)
	if len(plan) != 1 || plan[0].Kind != "traffic-stop" {
		t.Fatalf("expected only traffic-stop, got %v", plan)
	}
}
