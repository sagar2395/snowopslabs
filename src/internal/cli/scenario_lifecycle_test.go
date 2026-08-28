// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sagar2395/snowopslabs/internal/run"
	scn "github.com/sagar2395/snowopslabs/internal/scenario"
	scnsvc "github.com/sagar2395/snowopslabs/internal/service/scenario"
	"github.com/sagar2395/snowopslabs/internal/store"
	"github.com/sagar2395/snowopslabs/internal/toolchain"
)

const cliDemoScenario = `apiVersion: scenario.snowops.net/v2
name: demo
displayName: Demo
description: hermetic
components:
  - name: setup
    type: script
    script: setup.sh
`

// fakeScenarioEnv points scenarioEngineFactory at a Fake-backed engine, a temp
// store, and a real scenario engine over a temp content root.
func fakeScenarioEnv(t *testing.T) (*toolchain.Fake, *store.Store) {
	t.Helper()
	ctx := context.Background()

	root := t.TempDir()
	scDir := filepath.Join(root, "scenarios", "demo")
	if err := os.MkdirAll(scDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeF(t, filepath.Join(scDir, "scenario.yaml"), cliDemoScenario)
	writeF(t, filepath.Join(scDir, "setup.sh"), "#!/usr/bin/env bash\necho up\n")

	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	f := toolchain.NewFake()
	engine := scn.NewEngine(root, "k3d.local", "k3d")
	// The reset/up/down commands consult the process-wide scenario engine for
	// the active-state pre-check; point it at our temp engine for the test.
	prevScenes := scenes
	scenes = engine
	t.Cleanup(func() { scenes = prevScenes })

	prev := scenarioEngineFactory
	scenarioEngineFactory = func(ctx context.Context) (*scnsvc.Service, *store.Store, *run.Engine, func(), error) {
		resolver, rerr := toolchain.NewResolver(root)
		if rerr != nil {
			return nil, nil, nil, nil, rerr
		}
		eng, eerr := run.New(st, f, resolver, run.WithWorkers(2))
		if eerr != nil {
			return nil, nil, nil, nil, eerr
		}
		svc, serr := scnsvc.New(eng, st, engine, f, root)
		if serr != nil {
			return nil, nil, nil, nil, serr
		}
		cleanup := func() {
			sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = eng.Shutdown(sctx)
		}
		return svc, st, eng, cleanup, nil
	}
	t.Cleanup(func() {
		scenarioEngineFactory = prev
		_ = st.Close()
	})
	return f, st
}

func writeF(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestRunScenarioOp_ActivateRecordsRunAndComponents(t *testing.T) {
	f, st := fakeScenarioEnv(t)
	f.WhenArgsContain("setup.sh", "installing\n", 0)

	var buf bytes.Buffer
	err := runScenarioOp(dummyCmd(&buf), "activate", "demo", func(ctx context.Context, svc *scnsvc.Service) (string, error) {
		return svc.Activate(ctx, "demo", false)
	})
	if err != nil {
		t.Fatalf("activate: %v\n%s", err, buf.String())
	}
	if !strings.Contains(buf.String(), "activate demo started") {
		t.Errorf("missing start line: %q", buf.String())
	}
	runs, _ := st.ListRuns(context.Background(), store.RunFilter{Kind: scnsvc.KindActivate})
	if len(runs) != 1 || runs[0].Status != store.StatusSucceeded {
		t.Fatalf("expected one succeeded activate run, got %+v", runs)
	}
	if _, err := st.GetComponent(context.Background(), "scenario:demo/setup"); err != nil {
		t.Errorf("component not recorded in inventory: %v", err)
	}
}

func TestRunScenarioReset_ReactivatesFromInactive(t *testing.T) {
	f, st := fakeScenarioEnv(t)
	f.WhenArgsContain("setup.sh", "up\n", 0)

	// From inactive: reset just (re)activates.
	var buf bytes.Buffer
	if err := runScenarioReset(dummyCmd(&buf), "demo"); err != nil {
		t.Fatalf("reset: %v\n%s", err, buf.String())
	}
	if !strings.Contains(buf.String(), "Re-activating demo") {
		t.Errorf("missing re-activate line: %q", buf.String())
	}
	acts, _ := st.ListRuns(context.Background(), store.RunFilter{Kind: scnsvc.KindActivate})
	if len(acts) != 1 || acts[0].Status != store.StatusSucceeded {
		t.Fatalf("expected one succeeded activate, got %+v", acts)
	}
}

func TestRunScenarioReset_TearsDownThenReactivatesWhenActive(t *testing.T) {
	f, st := fakeScenarioEnv(t)
	f.WhenArgsContain("setup.sh", "up\n", 0)

	// Activate first so the scenario is active (markActive writes state).
	if err := runScenarioOp(dummyCmd(&bytes.Buffer{}), "activate", "demo", func(ctx context.Context, svc *scnsvc.Service) (string, error) {
		return svc.Activate(ctx, "demo", false)
	}); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := runScenarioReset(dummyCmd(&buf), "demo"); err != nil {
		t.Fatalf("reset: %v\n%s", err, buf.String())
	}
	if !strings.Contains(buf.String(), "tearing down first") {
		t.Errorf("active reset should tear down first: %q", buf.String())
	}
	// One deactivate run, and two activate runs total (initial + reset).
	if n := runCount(t, st, scnsvc.KindDeactivate); n != 1 {
		t.Errorf("deactivate runs = %d, want 1", n)
	}
	if n := runCount(t, st, scnsvc.KindActivate); n != 2 {
		t.Errorf("activate runs = %d, want 2", n)
	}
}

func runCount(t *testing.T, st *store.Store, kind string) int {
	t.Helper()
	runs, err := st.ListRuns(context.Background(), store.RunFilter{Kind: kind, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	return len(runs)
}

func TestRunScenarioOp_FailedActivationPropagates(t *testing.T) {
	f, _ := fakeScenarioEnv(t)
	f.WhenArgsContainStderr("setup.sh", "", "boom\n", 1)

	var buf bytes.Buffer
	err := runScenarioOp(dummyCmd(&buf), "activate", "demo", func(ctx context.Context, svc *scnsvc.Service) (string, error) {
		return svc.Activate(ctx, "demo", false)
	})
	if err == nil {
		t.Fatalf("expected a non-nil error for a failed activation; output: %s", buf.String())
	}
	if !strings.Contains(err.Error(), "activate demo failed") {
		t.Errorf("error should name the failed op: %v", err)
	}
}
