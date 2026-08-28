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

	"github.com/spf13/cobra"

	"github.com/sagar2395/snowopslabs/internal/run"
	labsvc "github.com/sagar2395/snowopslabs/internal/service/lab"
	"github.com/sagar2395/snowopslabs/internal/store"
	"github.com/sagar2395/snowopslabs/internal/toolchain"
)

// --- golden output ----------------------------------------------------------

func TestWriteLabStatus_Golden(t *testing.T) {
	cases := []struct {
		name string
		st   labsvc.Status
		want string
	}{
		{
			name: "unknown",
			st:   labsvc.Status{State: labsvc.StateUnknown},
			want: "State:     unknown\n\nNo lab operation recorded yet. Bring one up with: labctl lab up\n",
		},
		{
			name: "up",
			st:   labsvc.Status{State: labsvc.StateUp, Runtime: "k3d", RunID: "run_1"},
			want: "State:     up\nRuntime:   k3d\nLast run:  run_1\n",
		},
		{
			name: "up live reachable",
			st: labsvc.Status{
				State: labsvc.StateUp, Runtime: "k3d", RunID: "run_1",
				Live: &labsvc.Liveness{Reachable: true, Context: "k3d-snowops", Detail: "v1.33, 3 node(s)"},
			},
			want: "State:     up\nRuntime:   k3d\nLast run:  run_1\n\nCluster:   reachable (context k3d-snowops, v1.33, 3 node(s))\n",
		},
		{
			name: "down live unreachable",
			st: labsvc.Status{
				State: labsvc.StateDown, Runtime: "k3d", RunID: "run_2",
				Live: &labsvc.Liveness{Reachable: false, Detail: "connection refused"},
			},
			want: "State:     down\nRuntime:   k3d\nLast run:  run_2\n\nCluster:   unreachable (connection refused)\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := writeLabStatus(&buf, tc.st); err != nil {
				t.Fatal(err)
			}
			if got := buf.String(); got != tc.want {
				t.Fatalf("writeLabStatus mismatch:\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

// --- command wiring (hermetic, via factory override) ------------------------

// fakeLabEnv wires the lab commands to a Fake-backed engine and a temp store,
// and points labEngineFactory at it for the duration of a test.
func fakeLabEnv(t *testing.T) (*toolchain.Fake, *store.Store) {
	t.Helper()
	ctx := context.Background()

	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	root := t.TempDir()
	for _, script := range []string{"up.sh", "down.sh"} {
		p := filepath.Join(root, "runtimes", "k3d", script)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("#!/usr/bin/env bash\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	resolver, err := toolchain.NewResolver(root)
	if err != nil {
		t.Fatal(err)
	}
	f := toolchain.NewFake()
	eng, err := run.New(st, f, resolver, run.WithWorkers(2))
	if err != nil {
		t.Fatal(err)
	}

	prev := labEngineFactory
	labEngineFactory = func(ctx context.Context) (*labsvc.Service, *store.Store, *run.Engine, func(), error) {
		svc, err := labsvc.New(eng, st, "snowops-test")
		if err != nil {
			return nil, nil, nil, nil, err
		}
		cleanup := func() {} // engine/store torn down by t.Cleanup below
		return svc, st, eng, cleanup, nil
	}
	t.Cleanup(func() {
		labEngineFactory = prev
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = eng.Shutdown(sctx)
		_ = st.Close()
	})
	return f, st
}

func runLabCmd(t *testing.T, cmd *cobra.Command, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return buf.String(), err
}

func TestLabUp_RecordsAndStreamsRun(t *testing.T) {
	f, st := fakeLabEnv(t)
	f.WhenArgsContain("up.sh", "creating cluster...\ncluster ready\n", 0)

	out, err := runLabCmd(t, labUpCmd(), "k3d")
	if err != nil {
		t.Fatalf("lab up: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "up started (run ") {
		t.Errorf("missing start line: %q", out)
	}
	if !strings.Contains(out, "cluster ready") {
		t.Errorf("run transcript not streamed: %q", out)
	}
	if !strings.Contains(out, "Succeeded") {
		t.Errorf("missing success summary: %q", out)
	}

	runs, err := st.ListRuns(context.Background(), store.RunFilter{Kind: labsvc.KindUp})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Status != store.StatusSucceeded {
		t.Fatalf("expected one succeeded lab.up run, got %+v", runs)
	}
}

func TestLabDown_FailPropagatesNonZeroExit(t *testing.T) {
	f, _ := fakeLabEnv(t)
	f.WhenArgsContainStderr("down.sh", "", "k3d not found\n", 1)

	out, err := runLabCmd(t, labDownCmd(), "k3d")
	if err == nil {
		t.Fatalf("expected a non-nil error for a failed teardown; output: %s", out)
	}
	if !strings.Contains(err.Error(), "down failed") {
		t.Errorf("error should say the teardown failed: %v", err)
	}
}

func TestLabStatus_UnknownFromEmptyStore(t *testing.T) {
	fakeLabEnv(t)
	out, err := runLabCmd(t, labStatusCmd())
	if err != nil {
		t.Fatalf("lab status: %v", err)
	}
	if !strings.Contains(out, "State:     unknown") {
		t.Errorf("expected unknown state: %q", out)
	}
}

func TestLabStatus_UpAfterUp(t *testing.T) {
	f, _ := fakeLabEnv(t)
	f.WhenArgsContain("up.sh", "", 0)
	if _, err := runLabCmd(t, labUpCmd(), "k3d"); err != nil {
		t.Fatal(err)
	}
	out, err := runLabCmd(t, labStatusCmd())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "State:     up") || !strings.Contains(out, "Runtime:   k3d") {
		t.Errorf("expected up/k3d status: %q", out)
	}
}
