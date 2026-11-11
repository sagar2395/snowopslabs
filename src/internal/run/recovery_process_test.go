// SPDX-License-Identifier: Apache-2.0

package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/snowops-oss/snowopslabs/internal/store"
	"github.com/snowops-oss/snowopslabs/internal/toolchain"
)

type recoveryProcessReceipt struct {
	RunID    string
	OwnerID  string
	ChildPID int
}

// TestRecoveryProcess supplies local parent and child processes for crash recovery.
func TestRecoveryProcess(t *testing.T) {
	mode := os.Getenv("SNOWOPS_RECOVERY_PROCESS")
	if mode == "" {
		return
	}
	dir := os.Getenv("SNOWOPS_RECOVERY_DIRECTORY")
	if mode == "child" {
		for n := 1; ; n++ {
			if err := os.WriteFile(filepath.Join(dir, "heartbeat"), []byte(strconv.Itoa(n)), 0o600); err != nil {
				t.Fatal(err)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(t.Context(), filepath.Join(dir, "runs.db"))
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := toolchain.NewResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := New(st, toolchain.NewFake(), resolver)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	started := make(chan int, 1)
	id, err := engine.Submit(t.Context(), Spec{Kind: "test.child", LockKey: "lab", Func: func(ctx context.Context, out io.Writer) error {
		child := exec.Command(executable, "-test.run=^TestRecoveryProcess$", "-test.timeout=20s")
		child.Env = append(os.Environ(), "SNOWOPS_RECOVERY_PROCESS=child")
		if err := child.Start(); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(out, "saved output before owner crash"); err != nil {
			return err
		}
		started <- child.Process.Pid
		<-ctx.Done()
		return ctx.Err()
	}})
	if err != nil {
		t.Fatal(err)
	}
	pid := <-started
	rec, err := st.GetRun(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(recoveryProcessReceipt{RunID: id, OwnerID: rec.OwnerID, ChildPID: pid})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "receipt"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	<-t.Context().Done()
}

func TestRecoveryKeepsKeyWhileChildSurvives(t *testing.T) {
	dir := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	log, err := os.Create(filepath.Join(dir, "parent.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	parent := exec.Command(executable, "-test.run=^TestRecoveryProcess$", "-test.timeout=20s")
	parent.Env = append(os.Environ(), "SNOWOPS_RECOVERY_PROCESS=parent", "SNOWOPS_RECOVERY_DIRECTORY="+dir)
	parent.Stdout, parent.Stderr = log, log
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = parent.Process.Kill()
			_ = parent.Wait()
		}
	})
	waitFile := func(name string, accepts func([]byte) bool) []byte {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err == nil && accepts(data) {
				return data
			}
			time.Sleep(10 * time.Millisecond)
		}
		output, _ := os.ReadFile(log.Name())
		t.Fatalf("timed out waiting for %s: %s", name, output)
		return nil
	}
	var receipt recoveryProcessReceipt
	waitFile("receipt", func(data []byte) bool { return json.Unmarshal(data, &receipt) == nil })
	child, err := os.FindProcess(receipt.ChildPID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Kill(); _ = child.Release() })
	waitFile("heartbeat", func(data []byte) bool { n, err := strconv.Atoi(string(data)); return err == nil && n > 0 })
	proof, err := store.Open(t.Context(), filepath.Join(dir, "runs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer proof.Close()
	waitFile("receipt", func([]byte) bool {
		logs, err := proof.ReadLogs(t.Context(), receipt.RunID, 0, 100)
		if err != nil {
			return false
		}
		for _, line := range logs {
			if line.Text == "saved output before owner crash" {
				return true
			}
		}
		return false
	})
	if err := proof.Close(); err != nil {
		t.Fatal(err)
	}
	if err := parent.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := parent.Wait(); err == nil {
		t.Fatal("owner process did not die")
	}
	waited = true
	st, err := store.Open(t.Context(), filepath.Join(dir, "runs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	resolver, err := toolchain.NewResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := New(st, toolchain.NewFake(), resolver)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := engine.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	}()
	rec, err := st.GetRun(t.Context(), receipt.RunID)
	if err != nil || rec.RecoveryState != store.RecoveryRequired || rec.OwnerID != receipt.OwnerID {
		t.Fatalf("interrupted child lost ownership: %+v, %v", rec, err)
	}
	logs, err := st.ReadLogs(t.Context(), receipt.RunID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for _, line := range logs {
		text.WriteString(line.Text)
	}
	if !strings.Contains(text.String(), "saved output before owner crash") {
		t.Fatalf("partial output lost: %s", text.String())
	}
	spec := Spec{Kind: "test.replacement", LockKey: "lab", Func: func(context.Context, io.Writer) error { return nil }}
	if _, err := engine.Submit(t.Context(), spec); err == nil || !strings.Contains(err.Error(), "runs recover") {
		t.Fatalf("interrupted key was released: %v", err)
	}
	if _, err := st.ReleaseRecoveredRun(t.Context(), receipt.RunID, "uid=123 (test)"); err != nil {
		t.Fatal(err)
	}
	previous := waitFile("heartbeat", func(data []byte) bool { n, err := strconv.Atoi(string(data)); return err == nil && n > 0 })
	waitFile("heartbeat", func(data []byte) bool {
		n, err := strconv.Atoi(string(data))
		before, _ := strconv.Atoi(string(previous))
		return err == nil && n > before
	})
	replacement, err := engine.Submit(t.Context(), spec)
	if err != nil {
		t.Fatalf("explicit release kept key: %v", err)
	}
	(&harness{t: t, store: st}).waitForStatus(replacement, store.StatusSucceeded)
	if err := st.FinishOwnedRun(t.Context(), receipt.RunID, receipt.OwnerID, store.StatusSucceeded, nil, "", time.Now(), time.Second); !errors.Is(err, store.ErrRunNotFound) {
		t.Fatalf("old owner overwrote release: %v", err)
	}
}
