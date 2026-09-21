// SPDX-License-Identifier: Apache-2.0

// Package executor runs project scripts and commands and broadcasts their
// output as ActionEvents to the web UI's event stream. The web UI's mutating
// endpoints use it; CLI operations go through internal/run instead.
package executor

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Executor runs shell scripts from the project, streaming output to the caller.
//
// One Executor is shared by all HTTP handlers, which run concurrently. Access
// Env only through SetEnv and GetEnv, which hold envMu; reading or writing the
// map directly races with other requests.
type Executor struct {
	ProjectRoot string
	Env         map[string]string
	Stdout      io.Writer
	Stderr      io.Writer
	Broadcast   *Broadcaster
	actionSeq   atomic.Int64
	envMu       sync.RWMutex
}

// New creates an Executor rooted at projectRoot.
func New(projectRoot string) *Executor {
	return &Executor{
		ProjectRoot: projectRoot,
		Env:         make(map[string]string),
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
		Broadcast:   NewBroadcaster(),
	}
}

// NextActionID returns a new action ID without running anything. Handlers use
// it to put the ID in their 202 response before starting the work.
func (e *Executor) NextActionID() string {
	return fmt.Sprintf("action-%d", e.actionSeq.Add(1))
}

// RunScript executes a shell script relative to the project root.
func (e *Executor) RunScript(scriptPath string, args ...string) error {
	absPath := filepath.Join(e.ProjectRoot, scriptPath)
	if _, err := os.Stat(absPath); errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("script not found: %s", absPath)
	}

	slog.Debug("script start", "script", scriptPath, "args", args)
	cmd := exec.Command("bash", append([]string{absPath}, args...)...) //nolint:noctx // provisioning command; process lifecycle handled via streamed Wait, not context
	cmd.Dir = e.ProjectRoot
	cmd.Stdout = e.Stdout
	cmd.Stderr = e.Stderr
	cmd.Env = e.buildEnv()

	err := cmd.Run()
	slog.Debug("script end", "script", scriptPath, "err", err)
	return err
}

// RunScriptStreamed runs a shell script, broadcasts its output, and returns the
// action ID that tags its events.
func (e *Executor) RunScriptStreamed(actionLabel, scriptPath string, args ...string) (string, error) {
	absPath := filepath.Join(e.ProjectRoot, scriptPath)
	if _, err := os.Stat(absPath); errors.Is(err, fs.ErrNotExist) {
		errMsg := fmt.Sprintf("script not found: %s", absPath)
		id := e.broadcastError(actionLabel, scriptPath, args, errMsg)
		return id, fmt.Errorf("%s", errMsg)
	}

	cmdArgs := append([]string{absPath}, args...)
	return e.runStreamed(actionLabel, scriptPath+" "+strings.Join(args, " "), "bash", cmdArgs...)
}

// RunScriptStreamedWith is RunScriptStreamed with an action ID the caller got
// from NextActionID.
func (e *Executor) RunScriptStreamedWith(actionID, actionLabel, scriptPath string, args ...string) error {
	absPath := filepath.Join(e.ProjectRoot, scriptPath)
	if _, err := os.Stat(absPath); errors.Is(err, fs.ErrNotExist) {
		errMsg := fmt.Sprintf("script not found: %s", absPath)
		cmdStr := scriptPath + " " + strings.Join(args, " ")
		e.broadcastErrorWith(actionID, actionLabel, cmdStr, errMsg)
		return fmt.Errorf("%s", errMsg)
	}

	cmdArgs := append([]string{absPath}, args...)
	return e.runStreamedWith(actionID, actionLabel, scriptPath+" "+strings.Join(args, " "), "bash", cmdArgs...)
}

// RunCommandStreamed executes a command and streams output via the broadcaster.
// It returns the action ID used to tag broadcast events.
func (e *Executor) RunCommandStreamed(actionLabel, name string, args ...string) (string, error) {
	cmdStr := name + " " + strings.Join(args, " ")
	return e.runStreamed(actionLabel, cmdStr, name, args...)
}

// BroadcastStart emits an action_start event for an ID from NextActionID. Call
// it before starting a multi-step operation, so clients see it begin at once.
func (e *Executor) BroadcastStart(actionID, actionLabel string) {
	e.Broadcast.Send(ActionEvent{
		ID:        actionID,
		Type:      "action_start",
		Action:    actionLabel,
		Timestamp: time.Now(),
	})
}

// BroadcastEnd emits the action_end event matching BroadcastStart.
func (e *Executor) BroadcastEnd(actionID, actionLabel string, err error) {
	exitCode := 0
	errStr := ""
	if err != nil {
		exitCode = 1
		errStr = err.Error()
	}
	e.Broadcast.Send(ActionEvent{
		ID:        actionID,
		Type:      "action_end",
		Action:    actionLabel,
		ExitCode:  &exitCode,
		Error:     errStr,
		Timestamp: time.Now(),
	})
}

func (e *Executor) runStreamed(actionLabel, cmdStr, name string, args ...string) (string, error) {
	actionID := e.NextActionID()
	return actionID, e.runStreamedWith(actionID, actionLabel, cmdStr, name, args...)
}

func (e *Executor) runStreamedWith(actionID, actionLabel, cmdStr, name string, args ...string) error {
	slog.Debug("action start", "action", actionLabel, "cmd", cmdStr, "id", actionID)
	e.Broadcast.Send(ActionEvent{
		ID:        actionID,
		Type:      "action_start",
		Action:    actionLabel,
		Command:   cmdStr,
		Timestamp: time.Now(),
	})

	cmd := exec.Command(name, args...) //nolint:noctx // provisioning command; process lifecycle handled via streamed Wait, not context
	cmd.Dir = e.ProjectRoot
	cmd.Env = e.buildEnv()

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		e.broadcastEndError(actionID, actionLabel, cmdStr, err)
		return err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		e.broadcastEndError(actionID, actionLabel, cmdStr, err)
		return err
	}

	if err := cmd.Start(); err != nil {
		e.broadcastEndError(actionID, actionLabel, cmdStr, err)
		return err
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go e.streamOutput(&wg, actionID, actionLabel, stdoutPipe, "stdout", e.Stdout)
	go e.streamOutput(&wg, actionID, actionLabel, stderrPipe, "stderr", e.Stderr)
	wg.Wait()

	err = cmd.Wait()
	exitCode := 0
	errStr := ""
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
		errStr = err.Error()
	}

	slog.Debug("action end", "action", actionLabel, "id", actionID, "exitCode", exitCode, "err", errStr)
	e.Broadcast.Send(ActionEvent{
		ID:        actionID,
		Type:      "action_end",
		Action:    actionLabel,
		Command:   cmdStr,
		ExitCode:  &exitCode,
		Error:     errStr,
		Timestamp: time.Now(),
	})

	return err
}

func (e *Executor) streamOutput(wg *sync.WaitGroup, actionID, actionLabel string, r io.Reader, stream string, w io.Writer) {
	defer wg.Done()
	// bufio.Reader, not bufio.Scanner: a Scanner stops at its maximum token size,
	// which would drop a very long line and everything after it. ReadString has
	// no length limit.
	reader := bufio.NewReader(r)
	for {
		chunk, err := reader.ReadString('\n')
		if len(chunk) > 0 {
			line := strings.TrimRight(chunk, "\r\n")
			fmt.Fprintln(w, line)
			e.Broadcast.Send(ActionEvent{
				ID:        actionID,
				Type:      "action_output",
				Action:    actionLabel,
				Output:    line,
				Stream:    stream,
				Timestamp: time.Now(),
			})
		}
		if err != nil {
			// EOF or any read error ends the stream. A final line with no
			// trailing newline arrives together with the error.
			return
		}
	}
}

// broadcastError emits start+end events for a script-not-found failure and returns the action ID.
func (e *Executor) broadcastError(actionLabel, scriptPath string, args []string, errMsg string) string {
	actionID := e.NextActionID()
	e.broadcastErrorWith(actionID, actionLabel, scriptPath+" "+strings.Join(args, " "), errMsg)
	return actionID
}

func (e *Executor) broadcastErrorWith(actionID, actionLabel, cmdStr, errMsg string) {
	e.Broadcast.Send(ActionEvent{
		ID: actionID, Type: "action_start", Action: actionLabel,
		Command: cmdStr, Timestamp: time.Now(),
	})
	exitCode := 1
	e.Broadcast.Send(ActionEvent{
		ID: actionID, Type: "action_end", Action: actionLabel,
		Command: cmdStr, ExitCode: &exitCode, Error: errMsg, Timestamp: time.Now(),
	})
}

func (e *Executor) broadcastEndError(actionID, actionLabel, cmdStr string, err error) {
	exitCode := 1
	e.Broadcast.Send(ActionEvent{
		ID: actionID, Type: "action_end", Action: actionLabel,
		Command: cmdStr, ExitCode: &exitCode, Error: err.Error(), Timestamp: time.Now(),
	})
}

// RunCommand executes an arbitrary command in the project root.
func (e *Executor) RunCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...) //nolint:noctx // provisioning command; process lifecycle handled via streamed Wait, not context
	cmd.Dir = e.ProjectRoot
	cmd.Stdout = e.Stdout
	cmd.Stderr = e.Stderr
	cmd.Env = e.buildEnv()

	return cmd.Run()
}

// RunMake executes a Make target.
func (e *Executor) RunMake(target string, vars ...string) error {
	args := []string{target}
	args = append(args, vars...)
	return e.RunCommand("make", args...)
}

// RunHelm executes a helm command.
func (e *Executor) RunHelm(args ...string) error {
	return e.RunCommand("helm", args...)
}

// RunKubectl executes a kubectl command.
func (e *Executor) RunKubectl(args ...string) error {
	return e.RunCommand("kubectl", args...)
}

// SetEnv sets an environment variable for commands run after this call. It is
// safe for concurrent use.
func (e *Executor) SetEnv(key, value string) {
	e.envMu.Lock()
	defer e.envMu.Unlock()
	e.Env[key] = value
}

// CaptureOutput runs a command and returns its stdout as a string.
func (e *Executor) CaptureOutput(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...) //nolint:noctx // provisioning command; process lifecycle handled via streamed Wait, not context
	cmd.Dir = e.ProjectRoot
	cmd.Env = e.buildEnv()

	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", fmt.Errorf("%w: %s", err, string(exitErr.Stderr))
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// GetEnv returns one environment value set with SetEnv. It is safe for
// concurrent use.
func (e *Executor) GetEnv(key string) string {
	e.envMu.RLock()
	defer e.envMu.RUnlock()
	return e.Env[key]
}

func (e *Executor) buildEnv() []string {
	env := os.Environ()
	e.envMu.RLock()
	defer e.envMu.RUnlock()
	for k, v := range e.Env {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	return env
}
