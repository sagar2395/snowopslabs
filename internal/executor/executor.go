// SPDX-License-Identifier: Apache-2.0
package executor

import (
	"bufio"
	"fmt"
	"io"
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
type Executor struct {
	ProjectRoot string
	Env         map[string]string
	Stdout      io.Writer
	Stderr      io.Writer
	Broadcast   *Broadcaster
	actionSeq   int64
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

// NextActionID allocates and returns the next action ID without running anything.
// Handlers call this before launching a goroutine to get the ID for the 202 response.
func (e *Executor) NextActionID() string {
	return fmt.Sprintf("action-%d", atomic.AddInt64(&e.actionSeq, 1))
}

// RunScript executes a shell script relative to the project root.
func (e *Executor) RunScript(scriptPath string, args ...string) error {
	absPath := filepath.Join(e.ProjectRoot, scriptPath)
	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		return fmt.Errorf("script not found: %s", absPath)
	}

	slog.Debug("script start", "script", scriptPath, "args", args)
	cmd := exec.Command("bash", append([]string{absPath}, args...)...)
	cmd.Dir = e.ProjectRoot
	cmd.Stdout = e.Stdout
	cmd.Stderr = e.Stderr
	cmd.Env = e.buildEnv()

	err := cmd.Run()
	slog.Debug("script end", "script", scriptPath, "err", err)
	return err
}

// RunScriptStreamed executes a shell script and streams output via the broadcaster.
// It returns the action ID used to tag broadcast events so callers can correlate
// the 202 HTTP response with the WebSocket stream.
func (e *Executor) RunScriptStreamed(actionLabel, scriptPath string, args ...string) (string, error) {
	absPath := filepath.Join(e.ProjectRoot, scriptPath)
	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		errMsg := fmt.Sprintf("script not found: %s", absPath)
		id := e.broadcastError(actionLabel, scriptPath, args, errMsg)
		return id, fmt.Errorf("%s", errMsg)
	}

	cmdArgs := append([]string{absPath}, args...)
	return e.runStreamed(actionLabel, scriptPath+" "+strings.Join(args, " "), "bash", cmdArgs...)
}

// RunScriptStreamedWith executes a shell script using a caller-supplied action ID.
// Use this when the handler pre-allocates an ID (via NextActionID) to include in the
// 202 response body, so the WebSocket action_start event carries the same ID.
func (e *Executor) RunScriptStreamedWith(actionID, actionLabel, scriptPath string, args ...string) error {
	absPath := filepath.Join(e.ProjectRoot, scriptPath)
	if _, err := os.Stat(absPath); os.IsNotExist(err) {
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

// BroadcastStart emits an action_start event for a pre-allocated action ID.
// Call this before launching a goroutine for composite operations (scenario, platform)
// so the WS subscriber sees the start event immediately.
func (e *Executor) BroadcastStart(actionID, actionLabel string) {
	e.Broadcast.Send(ActionEvent{
		ID:        actionID,
		Type:      "action_start",
		Action:    actionLabel,
		Timestamp: time.Now(),
	})
}

// BroadcastEnd emits an action_end event for a pre-allocated action ID.
// Call this at the end of a goroutine for composite operations.
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

	cmd := exec.Command(name, args...)
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
		if exitErr, ok := err.(*exec.ExitError); ok {
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
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
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
	cmd := exec.Command(name, args...)
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

// SetEnv adds an environment variable to pass to executed commands.
func (e *Executor) SetEnv(key, value string) {
	e.Env[key] = value
}

// CaptureOutput runs a command and returns its stdout as a string.
func (e *Executor) CaptureOutput(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = e.ProjectRoot
	cmd.Env = e.buildEnv()

	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("%s: %s", err, string(exitErr.Stderr))
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (e *Executor) buildEnv() []string {
	env := os.Environ()
	for k, v := range e.Env {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	return env
}
