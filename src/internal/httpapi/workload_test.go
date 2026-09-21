// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"testing"
	"time"

	"github.com/sagar2395/snowopslabs/internal/config"
	"github.com/sagar2395/snowopslabs/internal/incident"
	"github.com/sagar2395/snowopslabs/internal/scenario"
	"github.com/sagar2395/snowopslabs/internal/workload"
)

func testBindServer(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()
	return &Server{
		cfg:       &config.Config{ProjectRoot: root},
		scenes:    &scenario.Engine{ProjectRoot: root, Workload: workload.Default("go-api")},
		incidents: &incident.Engine{ProjectRoot: root, Workload: workload.Default("go-api")},
	}
}

// An activation for another app must leave the server's shared engines bound
// as they were.
func TestWithWorkloadLeavesTheSharedEnginesAlone(t *testing.T) {
	s := testBindServer(t)
	scenes, incidents, release, err := s.withWorkload("")
	if err != nil {
		t.Fatalf("withWorkload: %v", err)
	}
	scenes.Workload = workload.Default("java-api")
	incidents.Workload = workload.Default("java-api")
	release()

	if got := s.scenes.Workload.Name; got != "go-api" {
		t.Errorf("shared scenario engine was rebound to %q", got)
	}
	if got := s.incidents.Workload.Name; got != "go-api" {
		t.Errorf("shared incident engine was rebound to %q", got)
	}
}

// A read handler must not wait on the binding lock, which an injection can hold
// for minutes.
func TestReadsDoNotWaitOnAnInFlightBinding(t *testing.T) {
	s := testBindServer(t)
	_, _, release, err := s.withWorkload("")
	if err != nil {
		t.Fatalf("withWorkload: %v", err)
	}
	defer release()

	done := make(chan workload.Workload, 1)
	go func() { done <- s.boundWorkload("") }()
	select {
	case got := <-done:
		if got.Name != "go-api" {
			t.Errorf("boundWorkload = %q, want go-api", got.Name)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("boundWorkload blocked while an operation held the binding")
	}
}

// An explicitly named app with no app.env is an error, rather than falling
// back to the default app and breaking something the user did not name.
func TestWithWorkloadRejectsAnUnknownApp(t *testing.T) {
	s := testBindServer(t)
	_, _, release, err := s.withWorkload("no-such-app")
	defer release()
	if err == nil {
		t.Fatal("expected an error for an app with no app.env")
	}
	if s.scenes.Workload.Name != "go-api" {
		t.Errorf("a failed bind changed the shared engine to %q", s.scenes.Workload.Name)
	}
}
