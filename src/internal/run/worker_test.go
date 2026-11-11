// SPDX-License-Identifier: Apache-2.0

package run

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/snowops-oss/snowopslabs/internal/store"
	"github.com/snowops-oss/snowopslabs/internal/toolchain"
)

func TestClassify(t *testing.T) {
	exit := &toolchain.ExitError{ExitCode: 3, Stderr: "boom"}
	live := context.Background()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, cancelExpired := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelExpired()

	tests := []struct {
		name        string
		runCtx      context.Context
		timeoutCtx  context.Context
		res         toolchain.Result
		err         error
		wantStatus  store.Status
		wantExit    *int
		wantMessage string
	}{
		{"success", live, live, toolchain.Result{ExitCode: 0}, nil, store.StatusSucceeded, intPtr(0), ""},
		{"a script that exits non-zero", live, live, toolchain.Result{}, exit, store.StatusFailed, intPtr(3), exit.Error()},
		{"an operation that explains its failed command keeps the explanation", live, live, toolchain.Result{},
			fmt.Errorf("could not uninstall web, so demo is still active: %w", exit), store.StatusFailed, intPtr(3), "could not uninstall web, so demo is still active"},
		{"a command that could not start", live, live, toolchain.Result{}, errors.New("bash: not found"), store.StatusFailed, nil, "bash: not found"},
		{"cancelled by the user", cancelled, cancelled, toolchain.Result{}, exit, store.StatusCancelled, nil, "cancelled by user"},
		{"out of time", live, expired, toolchain.Result{}, exit, store.StatusTimedOut, nil, "timed out after 1m0s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, code, msg := classify(tt.runCtx, tt.timeoutCtx, time.Minute, tt.res, tt.err)
			if status != tt.wantStatus {
				t.Errorf("status = %q, want %q", status, tt.wantStatus)
			}
			if (code == nil) != (tt.wantExit == nil) || (code != nil && *code != *tt.wantExit) {
				t.Errorf("exit code = %v, want %v", code, tt.wantExit)
			}
			if tt.wantMessage != "" && !strings.Contains(msg, tt.wantMessage) {
				t.Errorf("message = %q, want it to contain %q", msg, tt.wantMessage)
			}
		})
	}
}
