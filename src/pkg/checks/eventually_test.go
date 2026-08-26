// SPDX-License-Identifier: Apache-2.0
package checks

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// fastEventually returns opts tuned for tests: sub-millisecond intervals so the
// backoff loop spins quickly, with a generous deadline the test can override.
func fastEventually() EventuallyOpts {
	return EventuallyOpts{
		Deadline:        2 * time.Second,
		InitialInterval: time.Millisecond,
		MaxInterval:     2 * time.Millisecond,
		Multiplier:      2.0,
	}
}

// countingExec returns an ExecFunc that fails (empty output = "no matching
// resources" for an existence check) until the nth call, then succeeds. It also
// records how many times it was invoked.
func countingExec(passOn int32) (ExecFunc, *int32) {
	var calls int32
	fn := func(ctx context.Context, name string, args ...string) (string, error) {
		n := atomic.AddInt32(&calls, 1)
		if n >= passOn {
			return "pod/go-api-abc\n", nil
		}
		return "", nil
	}
	return fn, &calls
}

func TestEventually_PassesAfterRetries(t *testing.T) {
	exec, calls := countingExec(3)
	r := testRunner()
	r.Exec = exec

	check := Check{Name: "pod-exists", Type: "kubectl", Resource: "pod/go-api"}
	res := r.Eventually(context.Background(), check, fastEventually())

	if !res.Pass {
		t.Fatalf("expected pass after retries, got fail: %s", res.Explanation)
	}
	if res.Attempts != 3 {
		t.Fatalf("Attempts = %d, want 3", res.Attempts)
	}
	if got := atomic.LoadInt32(calls); got != 3 {
		t.Fatalf("exec called %d times, want 3", got)
	}
}

func TestEventually_FirstAttemptPasses(t *testing.T) {
	exec, _ := countingExec(1)
	r := testRunner()
	r.Exec = exec

	res := r.Eventually(context.Background(), Check{Name: "c", Type: "kubectl", Resource: "pod/x"}, fastEventually())
	if !res.Pass {
		t.Fatalf("expected immediate pass, got: %s", res.Explanation)
	}
	if res.Attempts != 1 {
		t.Fatalf("Attempts = %d, want 1 (should not sleep before first attempt)", res.Attempts)
	}
}

func TestEventually_DeadlineElapses(t *testing.T) {
	// Never passes: the resource stays absent.
	r := testRunner()
	r.Exec = func(ctx context.Context, name string, args ...string) (string, error) {
		return "", nil
	}

	opts := fastEventually()
	opts.Deadline = 40 * time.Millisecond
	start := time.Now()
	res := r.Eventually(context.Background(), Check{Name: "c", Type: "kubectl", Resource: "pod/x"}, opts)
	elapsed := time.Since(start)

	if res.Pass {
		t.Fatal("expected failure when the check never passes")
	}
	if res.Attempts < 1 {
		t.Fatalf("Attempts = %d, want >= 1", res.Attempts)
	}
	if res.Explanation == "" {
		t.Fatal("expected an explanation on the final result")
	}
	// It must respect the deadline and not run unbounded.
	if elapsed > 500*time.Millisecond {
		t.Fatalf("Eventually ran for %s, expected it to stop near the 40ms deadline", elapsed)
	}
}

func TestEventually_Cancellation(t *testing.T) {
	r := testRunner()
	r.Exec = func(ctx context.Context, name string, args ...string) (string, error) {
		return "", nil // always fails
	}

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel shortly after the poll begins.
	go func() {
		time.Sleep(15 * time.Millisecond)
		cancel()
	}()

	opts := fastEventually()
	opts.Deadline = 10 * time.Second // long, so cancellation is what stops us
	start := time.Now()
	res := r.Eventually(ctx, Check{Name: "c", Type: "kubectl", Resource: "pod/x"}, opts)
	elapsed := time.Since(start)

	if res.Pass {
		t.Fatal("expected failure on cancellation")
	}
	if elapsed > time.Second {
		t.Fatalf("Eventually took %s to notice cancellation", elapsed)
	}
}

func TestEventuallyOpts_Defaults(t *testing.T) {
	got := EventuallyOpts{}.withDefaults()
	if got.Deadline != defaultEventuallyDeadline {
		t.Errorf("Deadline = %s, want %s", got.Deadline, defaultEventuallyDeadline)
	}
	if got.InitialInterval != defaultInitialInterval {
		t.Errorf("InitialInterval = %s, want %s", got.InitialInterval, defaultInitialInterval)
	}
	if got.MaxInterval != defaultMaxInterval {
		t.Errorf("MaxInterval = %s, want %s", got.MaxInterval, defaultMaxInterval)
	}
	if got.Multiplier != defaultMultiplier {
		t.Errorf("Multiplier = %v, want %v", got.Multiplier, defaultMultiplier)
	}
}

func TestResultExplain(t *testing.T) {
	tests := []struct {
		name string
		res  Result
		want string
	}{
		{"error", Result{Name: "c", Error: "boom"}, "c errored: boom"},
		{"pass with got", Result{Name: "c", Pass: true, Got: "status 200"}, "c passed: observed status 200"},
		{"pass no got", Result{Name: "c", Pass: true}, "c passed"},
		{"fail want+got", Result{Name: "c", Want: ">= 3", Got: "1"}, "c failed: expected >= 3 but observed 1"},
		{"fail want only", Result{Name: "c", Want: ">= 3"}, "c failed: expected >= 3"},
		{"fail got only", Result{Name: "c", Got: "no samples"}, "c failed: observed no samples"},
		{"fail bare", Result{Name: "c"}, "c failed"},
		{"unnamed", Result{Pass: true}, "check passed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.res.explain(); got != tt.want {
				t.Errorf("explain() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRun_SetsExplanationAndAttempts(t *testing.T) {
	r := testRunner()
	r.Exec = func(ctx context.Context, name string, args ...string) (string, error) {
		return "pod/x\n", nil
	}
	res := r.Run(context.Background(), Check{Name: "c", Type: "kubectl", Resource: "pod/x"})
	if res.Attempts != 1 {
		t.Errorf("Attempts = %d, want 1", res.Attempts)
	}
	if res.Explanation == "" {
		t.Error("expected Run to populate Explanation")
	}
}
