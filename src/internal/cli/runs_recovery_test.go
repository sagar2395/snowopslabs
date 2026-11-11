// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/snowops-oss/snowopslabs/internal/store"
)

func recoveryStore(t *testing.T) func() *store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runs.db")
	open := func() *store.Store {
		t.Helper()
		st, err := store.Open(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	st := open()
	if err := st.CreateRun(t.Context(), store.Run{ID: "interrupted", Kind: "lab.up", LockKey: "lab"}); err != nil {
		t.Fatal(err)
	}
	if err := st.StartRun(t.Context(), "interrupted", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendLogs(t.Context(), "interrupted", []store.LogLine{{Text: "saved output"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RecoverOrphanedRuns(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	original := openStore
	openStore = func(ctx context.Context) (*store.Store, error) { return store.Open(ctx, path) }
	t.Cleanup(func() { openStore = original })
	return open
}

func TestRunsRecoverRequiresForce(t *testing.T) {
	open := recoveryStore(t)
	st := open()
	before, _ := st.GetRun(t.Context(), "interrupted")
	logs, _ := st.ReadLogs(t.Context(), "interrupted", 0, 100)
	_ = st.Close()
	cmd := runsCmd()
	var out, diagnostics bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&diagnostics)
	cmd.SetArgs([]string{"recover", "interrupted"})
	err := cmd.ExecuteContext(t.Context())
	if !errors.Is(err, store.ErrRecoveryRequired) {
		t.Fatalf("recover without --force = %v; output %s", err, out.String())
	}
	for _, want := range []string{"interrupted", "lab", "Recovery required", "labctl runs recover interrupted --force", "operation may still be running"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("preview missing %q: %s", want, out.String())
		}
	}
	st = open()
	defer func() { _ = st.Close() }()
	after, _ := st.GetRun(t.Context(), "interrupted")
	afterLogs, _ := st.ReadLogs(t.Context(), "interrupted", 0, 100)
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(logs, afterLogs) {
		t.Fatal("preview changed the row or transcript")
	}
	if err := st.CreateRun(t.Context(), store.Run{ID: "blocked", Kind: "lab.down", LockKey: "lab"}); !errors.Is(err, store.ErrLockHeld) {
		t.Fatalf("preview released the key: %v", err)
	}
}

func TestRunsRecoveryClients(t *testing.T) {
	for _, follow := range []bool{false, true} {
		t.Run(map[bool]string{false: "snapshot", true: "follow"}[follow], func(t *testing.T) {
			open := recoveryStore(t)
			st := open()
			defer func() { _ = st.Close() }()
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			var out bytes.Buffer
			err := streamRunLogs(ctx, &out, st, "interrupted", follow)
			if follow && !errors.Is(err, store.ErrRecoveryRequired) || !follow && err != nil {
				t.Fatalf("logs follow=%v = %v", follow, err)
			}
			if !strings.Contains(out.String(), "saved output") || !strings.Contains(out.String(), "Recovery required") {
				t.Fatalf("lost output or recovery guidance: %s", out.String())
			}
			rec, _ := st.GetRun(t.Context(), "interrupted")
			if got := duration(rec); got != "-" {
				t.Errorf("interrupted duration advances: %q", got)
			}
			out.Reset()
			if err := writeRunTable(&out, []store.Run{rec}); err != nil || !strings.Contains(out.String(), "Recovery required") {
				t.Errorf("list = %s, %v", out.String(), err)
			}
		})
	}
}

func TestRunsCancelRefusesRecovery(t *testing.T) {
	open := recoveryStore(t)
	cmd := runsCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"cancel", "interrupted"})
	if err := cmd.ExecuteContext(t.Context()); !errors.Is(err, store.ErrRecoveryRequired) {
		t.Fatalf("ordinary cancel = %v, want recovery guidance", err)
	}
	st := open()
	defer func() { _ = st.Close() }()
	rec, _ := st.GetRun(t.Context(), "interrupted")
	logs, _ := st.ReadLogs(t.Context(), "interrupted", 0, 100)
	if rec.Status != store.StatusRunning || rec.RecoveryState != store.RecoveryRequired || len(logs) != 2 {
		t.Fatalf("cancel changed interrupted work: %+v, logs=%d", rec, len(logs))
	}
}

func TestRunsRecoveryJourney(t *testing.T) {
	l := newTerminal(t)
	open := recoveryStore(t)
	preview := l.run("runs", "recover", "interrupted")
	if preview.exitCode() != 1 || !strings.Contains(preview.stdout, "Key: lab") {
		t.Fatalf("preview = %+v", preview)
	}
	release := l.run("runs", "recover", "interrupted", "--force")
	if release.err != nil || !strings.Contains(release.stderr, "operation may still be running") {
		t.Fatalf("forced release = %+v", release)
	}
	st := open()
	rec, err := st.GetRun(t.Context(), "interrupted")
	if err != nil || rec.RecoveryState != store.RecoveryReleased || rec.ReleasedAt.IsZero() || !strings.HasPrefix(rec.ReleasedBy, "uid="+strconv.Itoa(os.Getuid())) {
		t.Fatalf("release audit = %+v, %v", rec, err)
	}
	if err := st.CreateRun(t.Context(), store.Run{ID: "replacement", Kind: "lab.down", LockKey: "lab"}); err != nil {
		t.Fatalf("forced release retained key: %v", err)
	}
	_ = st.Close()
	repeat := l.run("runs", "recover", "interrupted", "--force")
	logs := l.run("runs", "logs", "interrupted", "--follow")
	if repeat.err != nil || logs.err != nil || !strings.Contains(logs.stdout, "saved output") || !strings.Contains(logs.stdout, "Key released by operator") {
		t.Fatalf("repeat=%+v, logs=%+v", repeat, logs)
	}
	st = open()
	defer func() { _ = st.Close() }()
	after, _ := st.GetRun(t.Context(), "interrupted")
	audit, err := st.ListAudit(t.Context(), 10)
	if err != nil || len(audit) != 1 || audit[0].Action != "run.recover" || audit[0].Actor != rec.ReleasedBy || audit[0].Target != "lab" || !reflect.DeepEqual(rec, after) {
		t.Fatalf("repeat replaced audit: %+v, %v, %+v", audit, err, after)
	}
	for _, result := range []*cliResult{&release, &repeat, &logs} {
		result.stdout = strings.ReplaceAll(result.stdout, rec.ReleasedBy, "<operator>")
	}
	l.golden("runs_recovery_journey", preview, release, repeat, logs)
}

func TestRunsRecoveryFailures(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		prepare func(*store.Store)
		want    string
	}{
		{name: "unknown run", args: []string{"recover", "missing", "--force"}, want: "run not found"},
		{name: "missing run ID", args: []string{"recover", "--force"}, want: "accepts 1 arg"},
		{name: "not interrupted", args: []string{"recover", "live", "--force"}, prepare: func(st *store.Store) {
			if err := st.CreateRun(t.Context(), store.Run{ID: "live", Kind: "lab.up"}); err != nil {
				t.Fatal(err)
			}
		}, want: "does not require recovery; inspect it with labctl runs list"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			open := recoveryStore(t)
			st := open()
			if tc.prepare != nil {
				tc.prepare(st)
			}
			_ = st.Close()
			cmd := runsCmd()
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetArgs(tc.args)
			if err := cmd.ExecuteContext(t.Context()); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("failure = %v, want %q", err, tc.want)
			}
		})
	}
}

type recoveryWriteFailure struct{}

func (recoveryWriteFailure) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestRunsRecoveryRefusesAnUnprintedWarning(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(strconv.FormatBool(force), func(t *testing.T) {
			open := recoveryStore(t)
			cmd := runsCmd()
			args := []string{"recover", "interrupted"}
			if force {
				args = append(args, "--force")
			}
			cmd.SetArgs(args)
			cmd.SetOut(recoveryWriteFailure{})
			cmd.SetErr(recoveryWriteFailure{})
			if err := cmd.ExecuteContext(t.Context()); !errors.Is(err, io.ErrClosedPipe) {
				t.Fatalf("unprinted warning = %v", err)
			}
			st := open()
			defer st.Close()
			rec, _ := st.GetRun(t.Context(), "interrupted")
			if rec.RecoveryState != store.RecoveryRequired {
				t.Fatalf("key released without showing warning: %+v", rec)
			}
		})
	}
}

func TestRunsRecoveryStoreFailure(t *testing.T) {
	original := openStore
	openStore = func(context.Context) (*store.Store, error) { return nil, io.ErrUnexpectedEOF }
	t.Cleanup(func() { openStore = original })
	cmd := runsCmd()
	cmd.SetArgs([]string{"recover", "missing", "--force"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.ExecuteContext(t.Context()); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("store failure = %v", err)
	}
}

func TestRunsRecoverMissingOwner(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(strconv.FormatBool(force), func(t *testing.T) {
			open := recoveryStore(t)
			st := open()
			owner := "owner_" + strings.Repeat("a", 32)
			if _, err := st.DB().ExecContext(t.Context(), `UPDATE runs SET owner_id = ? WHERE id = 'interrupted'`, owner); err != nil {
				t.Fatal(err)
			}
			if _, err := st.RecoverOrphanedRuns(t.Context(), time.Now()); err != nil {
				t.Fatal(err)
			}
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			cmd := runsCmd()
			var out, diagnostics bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&diagnostics)
			args := []string{"recover", "interrupted"}
			if force {
				args = append(args, "--force")
			}
			cmd.SetArgs(args)
			err := cmd.ExecuteContext(t.Context())
			if force && err != nil || !force && !errors.Is(err, store.ErrRecoveryRequired) {
				t.Fatalf("missing-owner recovery = %v", err)
			}
			for _, want := range []string{"can't check whether this work is still running", "Check the lab before releasing it"} {
				if !strings.Contains(out.String()+diagnostics.String(), want) {
					t.Errorf("missing warning %q: %s%s", want, out.String(), diagnostics.String())
				}
			}
			st = open()
			defer st.Close()
			rec, err := st.GetRun(t.Context(), "interrupted")
			if err != nil {
				t.Fatal(err)
			}
			audit, err := st.ListAudit(t.Context(), 10)
			if err != nil {
				t.Fatal(err)
			}
			if force {
				if rec.RecoveryState != store.RecoveryReleased || rec.RecoveryReason != "owner_missing" || len(audit) != 1 || !strings.Contains(audit[0].Detail, "reason=owner_missing") || !strings.Contains(audit[0].Detail, owner) {
					t.Fatalf("release = %+v, audit=%+v", rec, audit)
				}
			} else if rec.RecoveryState != store.RecoveryRequired || len(audit) != 0 {
				t.Fatalf("preview changed missing-owner work: %+v, audit=%+v", rec, audit)
			}
		})
	}
}
