// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRecordComponentInstalled_AndList(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	if err := s.RecordComponentInstalled(ctx, Component{
		ID: "platform:ingress/traefik", Kind: "platform", Ref: "ingress/traefik",
		Namespace: "traefik", InstallRun: "run_1",
	}); err != nil {
		t.Fatalf("RecordComponentInstalled: %v", err)
	}

	got, err := s.GetComponent(ctx, "platform:ingress/traefik")
	if err != nil {
		t.Fatalf("GetComponent: %v", err)
	}
	if got.Status != ComponentInstalled || got.Ref != "ingress/traefik" || got.InstallRun != "run_1" {
		t.Fatalf("component = %+v", got)
	}
	if got.InstalledAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Error("timestamps should be set")
	}
}

func TestRecordComponentInstalled_UpsertRefreshesAndClearsRemoval(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	id := "platform:mesh/istio"

	if err := s.RecordComponentInstalled(ctx, Component{ID: id, Kind: "platform", Ref: "mesh/istio", InstallRun: "run_1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkComponentRemoved(ctx, id, "run_2", time.Now()); err != nil {
		t.Fatal(err)
	}
	// Re-install: one row, back to installed, removal cleared.
	if err := s.RecordComponentInstalled(ctx, Component{ID: id, Kind: "platform", Ref: "mesh/istio", InstallRun: "run_3"}); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetComponent(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != ComponentInstalled {
		t.Errorf("status = %q, want installed", got.Status)
	}
	if got.InstallRun != "run_3" {
		t.Errorf("install_run = %q, want run_3", got.InstallRun)
	}
	if !got.RemovedAt.IsZero() || got.RemoveRun != "" {
		t.Errorf("removal metadata should be cleared on re-install: %+v", got)
	}

	all, _ := s.ListComponents(ctx, ComponentFilter{})
	if len(all) != 1 {
		t.Fatalf("expected a single row after re-install, got %d", len(all))
	}
}

func TestMarkComponentRemoved_KeepsRowAsHistory(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	id := "platform:ingress/traefik"
	if err := s.RecordComponentInstalled(ctx, Component{ID: id, Kind: "platform", Ref: "ingress/traefik"}); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkComponentRemoved(ctx, id, "run_9", time.Now()); err != nil {
		t.Fatalf("MarkComponentRemoved: %v", err)
	}
	got, err := s.GetComponent(ctx, id)
	if err != nil {
		t.Fatalf("row should survive removal: %v", err)
	}
	if got.Status != ComponentRemoved || got.RemoveRun != "run_9" || got.RemovedAt.IsZero() {
		t.Errorf("component = %+v, want removed with metadata", got)
	}
}

func TestMarkComponentRemoved_UnknownIsReported(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	err := s.MarkComponentRemoved(ctx, "platform:ghost/none", "run_1", time.Now())
	if !errors.Is(err, ErrComponentNotFound) {
		t.Fatalf("err = %v, want ErrComponentNotFound", err)
	}
}

func TestListComponents_Filters(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	seed := []Component{
		{ID: "platform:ingress/traefik", Kind: "platform", Ref: "ingress/traefik"},
		{ID: "platform:mesh/istio", Kind: "platform", Ref: "mesh/istio"},
		{ID: "scenario:autoscaling/go-api-so", Kind: "scenario", Owner: "autoscaling", Ref: "go-api-so"},
	}
	for _, c := range seed {
		if err := s.RecordComponentInstalled(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	// Remove one so the installed filter must exclude it.
	if err := s.MarkComponentRemoved(ctx, "platform:mesh/istio", "", time.Now()); err != nil {
		t.Fatal(err)
	}

	installed, _ := s.ListComponents(ctx, ComponentFilter{Status: ComponentInstalled})
	if len(installed) != 2 {
		t.Fatalf("installed = %d, want 2", len(installed))
	}
	platform, _ := s.ListComponents(ctx, ComponentFilter{Kind: "platform"})
	if len(platform) != 2 {
		t.Fatalf("platform kind = %d, want 2", len(platform))
	}
	owned, _ := s.ListComponents(ctx, ComponentFilter{Owner: "autoscaling"})
	if len(owned) != 1 || owned[0].Ref != "go-api-so" {
		t.Fatalf("owner filter = %+v, want the scenario component", owned)
	}
}

func TestRecordComponent_RequiresIDAndKind(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	if err := s.RecordComponentInstalled(ctx, Component{Ref: "x"}); err == nil {
		t.Fatal("expected an error when id and kind are missing")
	}
}
