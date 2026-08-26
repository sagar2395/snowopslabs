// SPDX-License-Identifier: Apache-2.0

package toolchain

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Fake is a Runner that records invocations and returns scripted responses.
//
// It is what makes hermetic testing possible above this package: service, CLI
// and HTTP tests get a full run lifecycle — including output, exit codes and
// cancellation — with no cluster, no network and no binaries. The shell-level
// equivalent for bats is test/shell/helpers/stub.bash.
//
// The zero value is usable: every command succeeds silently.
type Fake struct {
	mu    sync.Mutex
	calls []Command
	rules []rule

	// Delay makes every command take this long, so cancellation and timeout
	// paths can be exercised deterministically.
	Delay time.Duration

	// LookPathErr, when set, makes LookPath fail for any binary not in
	// Available — used to test preflight's missing-binary handling.
	LookPathErr error
	// Available lists binaries LookPath should resolve. Empty means all.
	Available map[string]string
}

type rule struct {
	match     func(Command) bool
	stdout    string
	stderr    string
	exitCode  int
	err       error
	blockFor  time.Duration
	described string
}

// NewFake returns a Fake in which every command succeeds.
func NewFake() *Fake { return &Fake{} }

// WhenArgsContain scripts the response for commands whose rendered form
// contains substr. Rules are matched in the order added and the first match
// wins, so register the most specific first.
func (f *Fake) WhenArgsContain(substr string, stdout string, exitCode int) *Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rules = append(f.rules, rule{
		match:     func(c Command) bool { return strings.Contains(c.String(), substr) },
		stdout:    stdout,
		exitCode:  exitCode,
		described: "args contain " + substr,
	})
	return f
}

// WhenArgsContainStderr is WhenArgsContain with error output as well — used to
// assert that a failure's stderr reaches the log transcript.
func (f *Fake) WhenArgsContainStderr(substr, stdout, stderr string, exitCode int) *Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rules = append(f.rules, rule{
		match:     func(c Command) bool { return strings.Contains(c.String(), substr) },
		stdout:    stdout,
		stderr:    stderr,
		exitCode:  exitCode,
		described: "args contain " + substr,
	})
	return f
}

// WhenArgsContainBlock makes a matching command hang for d, so a test can
// cancel or time it out mid-flight.
func (f *Fake) WhenArgsContainBlock(substr string, d time.Duration) *Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rules = append(f.rules, rule{
		match:     func(c Command) bool { return strings.Contains(c.String(), substr) },
		blockFor:  d,
		described: "args contain " + substr + " (blocking)",
	})
	return f
}

// WhenArgsContainError makes a matching command fail with a transport-level
// error — the binary vanished, permission denied — rather than a non-zero exit.
func (f *Fake) WhenArgsContainError(substr string, err error) *Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rules = append(f.rules, rule{
		match:     func(c Command) bool { return strings.Contains(c.String(), substr) },
		err:       err,
		described: "args contain " + substr + " (error)",
	})
	return f
}

// Run records the command and applies the first matching rule.
func (f *Fake) Run(ctx context.Context, cmd Command) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	f.mu.Lock()
	f.calls = append(f.calls, cmd)
	var matched *rule
	for i := range f.rules {
		if f.rules[i].match(cmd) {
			matched = &f.rules[i]
			break
		}
	}
	delay := f.Delay
	f.mu.Unlock()

	block := delay
	if matched != nil && matched.blockFor > 0 {
		block = matched.blockFor
	}
	if block > 0 {
		select {
		case <-time.After(block):
		case <-ctx.Done():
			// Mirror Exec: a cancelled run reports the context error and is
			// marked as signalled, so callers cannot accidentally treat a
			// cancellation as an ordinary failure.
			return Result{Signalled: true}, ctx.Err()
		}
	}

	if matched == nil {
		writeTo(cmd.Stdout, "")
		return Result{}, nil
	}
	if matched.err != nil {
		return Result{}, matched.err
	}

	writeTo(cmd.Stdout, matched.stdout)
	writeTo(cmd.Stderr, matched.stderr)

	if matched.exitCode != 0 {
		return Result{ExitCode: matched.exitCode},
			&ExitError{Command: cmd.String(), ExitCode: matched.exitCode, Stderr: matched.stderr}
	}
	return Result{}, nil
}

// LookPath resolves a binary according to Available / LookPathErr.
func (f *Fake) LookPath(name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.Available != nil {
		if path, ok := f.Available[name]; ok {
			return path, nil
		}
		if f.LookPathErr != nil {
			return "", f.LookPathErr
		}
		return "", fmt.Errorf("exec: %q: executable file not found in $PATH", name)
	}
	if f.LookPathErr != nil {
		return "", f.LookPathErr
	}
	return "/usr/bin/" + name, nil
}

// Calls returns a copy of every command Run received, in order.
func (f *Fake) Calls() []Command {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Command, len(f.calls))
	copy(out, f.calls)
	return out
}

// CallStrings renders every recorded call, for assertions and failure messages.
func (f *Fake) CallStrings() []string {
	calls := f.Calls()
	out := make([]string, len(calls))
	for i, c := range calls {
		out[i] = c.String()
	}
	return out
}

// Called reports whether any recorded call contains substr.
func (f *Fake) Called(substr string) bool {
	for _, s := range f.CallStrings() {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}

// CallCount returns how many recorded calls contain substr. An empty substr
// counts every call.
func (f *Fake) CallCount(substr string) int {
	n := 0
	for _, s := range f.CallStrings() {
		if substr == "" || strings.Contains(s, substr) {
			n++
		}
	}
	return n
}

// Reset clears the recorded calls, keeping the rules. Used between the two
// halves of an idempotency test.
func (f *Fake) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = nil
}

func writeTo(w interface{ Write([]byte) (int, error) }, s string) {
	if w == nil || s == "" {
		return
	}
	_, _ = w.Write([]byte(s))
}
