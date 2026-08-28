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
	incsvc "github.com/sagar2395/snowopslabs/internal/service/incident"
	"github.com/sagar2395/snowopslabs/internal/store"
	"github.com/sagar2395/snowopslabs/internal/toolchain"
)

// fakeIncidentEnv points incidentEngineFactory at a Fake-backed engine and a
// temp store with stub fault scripts. Like production, each factory call builds
// a fresh engine over the shared store, so a test can drive several ops.
func fakeIncidentEnv(t *testing.T) (*toolchain.Fake, *store.Store) {
	t.Helper()
	ctx := context.Background()

	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	root := t.TempDir()
	for _, script := range []string{"inject.sh", "resolve.sh"} {
		p := filepath.Join(root, "incidents", "oom-kill", script)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("#!/usr/bin/env bash\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f := toolchain.NewFake()

	prev := incidentEngineFactory
	incidentEngineFactory = func(ctx context.Context) (*incsvc.Service, *store.Store, *run.Engine, func(), error) {
		resolver, rerr := toolchain.NewResolver(root)
		if rerr != nil {
			return nil, nil, nil, nil, rerr
		}
		eng, eerr := run.New(st, f, resolver, run.WithWorkers(2))
		if eerr != nil {
			return nil, nil, nil, nil, eerr
		}
		svc, serr := incsvc.New(eng, st)
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
		incidentEngineFactory = prev
		_ = st.Close()
	})
	return f, st
}

func TestRunIncidentOp_InjectRecordsAndStreams(t *testing.T) {
	f, st := fakeIncidentEnv(t)
	f.WhenArgsContain("inject.sh", "breaking echo-server...\n", 0)

	var buf bytes.Buffer
	cmd := dummyCmd(&buf)
	target := incsvc.Target{Namespace: "echo-server", Workload: "echo-server"}
	err := runIncidentOp(cmd, "inject", "oom-kill", target, func(ctx context.Context, svc *incsvc.Service) (string, error) {
		return svc.Inject(ctx, "oom-kill", target)
	})
	if err != nil {
		t.Fatalf("inject: %v\n%s", err, buf.String())
	}
	if !strings.Contains(buf.String(), "inject oom-kill started") || !strings.Contains(buf.String(), "breaking echo-server") {
		t.Errorf("missing start line / transcript: %q", buf.String())
	}
	runs, _ := st.ListRuns(context.Background(), store.RunFilter{Kind: incsvc.KindInject})
	if len(runs) != 1 || runs[0].Status != store.StatusSucceeded || runs[0].Target != "oom-kill" {
		t.Fatalf("expected one succeeded inject of oom-kill, got %+v", runs)
	}
}

func TestRunIncidentOp_ResolveFailPropagates(t *testing.T) {
	f, _ := fakeIncidentEnv(t)
	f.WhenArgsContainStderr("resolve.sh", "", "stuck\n", 1)

	var buf bytes.Buffer
	target := incsvc.Target{Namespace: "echo-server", Workload: "echo-server"}
	err := runIncidentOp(dummyCmd(&buf), "resolve", "oom-kill", target, func(ctx context.Context, svc *incsvc.Service) (string, error) {
		return svc.Resolve(ctx, "oom-kill", target)
	})
	if err == nil {
		t.Fatalf("expected a non-nil error for a failed resolve; output: %s", buf.String())
	}
	if !strings.Contains(err.Error(), "resolve oom-kill failed") {
		t.Errorf("error should name the failed op: %v", err)
	}
}
