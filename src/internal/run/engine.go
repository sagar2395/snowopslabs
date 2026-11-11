// SPDX-License-Identifier: Apache-2.0

// Package run is the durable run engine. It turns "run this script" into an
// operation that can be cancelled, bounded by a timeout, serialised against
// conflicting work, and read back later.
//
// Everything that changes cluster state should go through here. The engine
// guarantees that:
//
//   - Cancellation reaches the whole process tree (ADR-0003).
//   - Conflicting operations are refused, not raced (ADR-0004).
//   - Every output line is persisted with a monotonic sequence, so a consumer
//     that reconnects misses nothing (ADR-0006).
//   - A run's record survives a restart, and an interrupted run is reconciled
//     rather than left "running" forever.
package run

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/snowops-oss/snowopslabs/internal/runowner"
	"github.com/snowops-oss/snowopslabs/internal/store"
	"github.com/snowops-oss/snowopslabs/internal/toolchain"
)

// RunFunc is an in-process operation the engine runs as a recorded run. It must
// honour ctx (returning promptly when it is cancelled) and write human-readable
// progress to out, which is persisted to the run's transcript. A nil return is
// success; any error is a failure recorded with the error's message.
type RunFunc func(ctx context.Context, out io.Writer) error

// Spec describes work to submit. It is the only way into the engine.
type Spec struct {
	// Kind names the operation, e.g. "platform.install". It selects the
	// default timeout and appears in listings.
	Kind string
	// Target is what the operation acts on, e.g. "ingress/traefik".
	Target string
	// LockKey serialises conflicting work. Runs sharing a key cannot overlap;
	// an empty key means the run takes no lock. See ADR-0004.
	LockKey string
	// Actor is who asked for it (server mode). Empty for local CLI use.
	Actor string
	// Script is the path to execute, relative to a content root. It is
	// resolved and containment-checked before anything runs. Exactly one of
	// Script or Func is set.
	Script string
	// Func is an in-process operation to run as a durable, cancellable, recorded
	// run - for work the engine orchestrates in Go rather than shelling out to a
	// single script (a multi-component scenario activation, for instance). It
	// receives the run's context (cancelled on Cancel/Shutdown/timeout) and a
	// writer for its transcript. It is held in-process only, never persisted.
	// Exactly one of Script or Func is set.
	Func RunFunc
	// Args are passed to the script as argv. Never a shell string.
	Args []string
	// Env is layered over the process environment. Scripts read their
	// settings as ${VAR:-default}, so this is how configuration reaches them.
	Env map[string]string
	// Dir is the working directory; empty means the engine's default.
	Dir string
	// Timeout overrides the default for this Kind. Zero uses the default.
	Timeout time.Duration
}

// Errors callers branch on.
var (
	// ErrQueueFull means the engine cannot accept more work right now.
	ErrQueueFull = errors.New("run queue is full")
	// ErrNotRunning means the engine has not been started, or is shut down.
	ErrNotRunning = errors.New("run engine is not running")
	// ErrNotCancellable means the run already reached a terminal state.
	ErrNotCancellable = errors.New("run is already finished")
)

// LockConflictError reports that another run holds the requested lock key.
// It carries the holder so the message can name it - "platform up is already
// running (run_abc, started 2m ago)" - instead of a bare refusal.
type LockConflictError struct {
	LockKey string
	Holder  store.Run
	Since   time.Duration
}

func (e *LockConflictError) Error() string {
	if e.Holder.RecoveryState == store.RecoveryRequired {
		return fmt.Sprintf("run %s requires recovery and holds key %s; check the lab, then use labctl runs recover %s --force", e.Holder.ID, e.LockKey, e.Holder.ID)
	}
	what := e.Holder.Kind
	if e.Holder.Target != "" {
		what += " " + e.Holder.Target
	}
	return fmt.Sprintf("%s is already in progress (run %s, started %s ago) - "+
		"wait for it, or cancel it with: labctl runs cancel %s",
		what, e.Holder.ID, e.Since.Round(time.Second), e.Holder.ID)
}

// DefaultTimeouts per run kind. A cluster build is slow; a status check that
// takes a minute is broken. An unlisted kind gets DefaultTimeout.
var DefaultTimeouts = map[string]time.Duration{
	"lab.up":              20 * time.Minute,
	"lab.down":            10 * time.Minute,
	"platform.install":    15 * time.Minute,
	"platform.uninstall":  10 * time.Minute,
	"platform.status":     2 * time.Minute,
	"scenario.activate":   20 * time.Minute,
	"scenario.deactivate": 10 * time.Minute,
	"scenario.verify":     5 * time.Minute,
	"incident.inject":     5 * time.Minute,
	"incident.resolve":    5 * time.Minute,
	"app.build":           15 * time.Minute,
	"app.deploy":          10 * time.Minute,
}

// DefaultTimeout applies to any kind not in DefaultTimeouts.
const DefaultTimeout = 10 * time.Minute

// defaultQueueSize bounds pending work. Saturation is reported, not hidden
// behind an unbounded buffer that turns into a memory leak.
const defaultQueueSize = 256

// defaultWorkers is how many non-conflicting runs execute at once.
const defaultWorkers = 4

// defaultCancelPoll is how often a running run re-reads its record, so a
// cancel another process writes to the store stops it.
const defaultCancelPoll = time.Second

// Engine queues, executes and records runs. Build it with New, call Start,
// then Submit work; call Shutdown before exiting.
type Engine struct {
	store    *store.Store
	runner   toolchain.Runner
	resolver *toolchain.Resolver

	workers   int
	queueSize int
	dir       string
	timeouts  map[string]time.Duration
	now       func() time.Time
	// cancelPoll is how often a running run re-reads its record.
	cancelPoll time.Duration

	queue chan string

	mu       sync.Mutex
	started  bool
	starting bool
	stopped  bool
	owner    *runowner.Lock
	done     chan struct{}
	closeErr error
	// cancels maps a live run to the func that stops it. Present only while
	// the run is queued or executing.
	cancels map[string]context.CancelFunc
	// pending records runs accepted but not yet picked up, so cancelling one
	// still in the queue works.
	pending map[string]struct{}

	// specs holds each submitted Spec until a worker picks it up. The store
	// keeps the durable fields (argv, script, timeout); env, working directory
	// and Func live only here, because env can hold cluster credentials.
	specs sync.Map // map[string]Spec

	subs *subscribers

	// metrics, when non-nil, receives run-lifecycle signals.
	metrics Metrics

	// finishHooks receive a run's terminal record once it has finished, so a
	// caller can react to outcomes without the engine knowing their domain - the
	// component inventory is recorded this way.
	finishHooks []FinishHook

	wg       sync.WaitGroup
	shutdown chan struct{}
}

// FinishHook is called with a run's terminal record once it has reached a final
// state. It runs synchronously in the worker before the run is considered done -
// so Shutdown waits for it - which means it must be quick and must not block.
// The engine stays domain-agnostic: a hook is how a caller learns that, say, a
// platform.install succeeded and records the component it left behind.
type FinishHook func(ctx context.Context, r store.Run)

// Metrics receives run-lifecycle signals for observability. It is deliberately
// tiny and framework-free so the engine does not depend on any metrics library;
// internal/metrics.App satisfies it. A nil Metrics (the default) disables it.
type Metrics interface {
	// RunStarted is called once a run transitions to running.
	RunStarted()
	// RunFinished is called once a run reaches a terminal state, with its kind,
	// terminal status ("succeeded", "failed", …) and wall-clock duration.
	RunFinished(kind, status string, dur time.Duration)
}

// Option configures an Engine.
type Option func(*Engine)

// WithMetrics attaches a metrics sink that receives run-lifecycle signals.
func WithMetrics(m Metrics) Option {
	return func(e *Engine) { e.metrics = m }
}

// WithFinishHook registers a callback invoked with each run's terminal record.
// Multiple hooks run in registration order. See FinishHook.
func WithFinishHook(h FinishHook) Option {
	return func(e *Engine) {
		if h != nil {
			e.finishHooks = append(e.finishHooks, h)
		}
	}
}

// WithWorkers sets how many non-conflicting runs execute concurrently.
func WithWorkers(n int) Option {
	return func(e *Engine) {
		if n > 0 {
			e.workers = n
		}
	}
}

// WithQueueSize bounds pending work.
func WithQueueSize(n int) Option {
	return func(e *Engine) {
		if n > 0 {
			e.queueSize = n
		}
	}
}

// WithWorkingDir sets the default working directory for scripts.
func WithWorkingDir(dir string) Option {
	return func(e *Engine) { e.dir = dir }
}

// WithTimeouts replaces the per-kind timeout table.
func WithTimeouts(t map[string]time.Duration) Option {
	return func(e *Engine) { e.timeouts = t }
}

// WithCancelPoll sets how often a running run checks the store for a cancel
// written by another process.
func WithCancelPoll(d time.Duration) Option {
	return func(e *Engine) {
		if d > 0 {
			e.cancelPoll = d
		}
	}
}

// WithClock replaces the time source; tests use it for determinism.
func WithClock(now func() time.Time) Option {
	return func(e *Engine) { e.now = now }
}

// New builds an Engine. Call Start before submitting work.
func New(st *store.Store, runner toolchain.Runner, resolver *toolchain.Resolver, opts ...Option) (*Engine, error) {
	if st == nil {
		return nil, errors.New("run: a store is required")
	}
	if runner == nil {
		return nil, errors.New("run: a runner is required")
	}
	if resolver == nil {
		return nil, errors.New("run: a script resolver is required")
	}

	e := &Engine{
		store:      st,
		runner:     runner,
		resolver:   resolver,
		workers:    defaultWorkers,
		queueSize:  defaultQueueSize,
		timeouts:   DefaultTimeouts,
		now:        time.Now,
		cancelPoll: defaultCancelPoll,
		cancels:    make(map[string]context.CancelFunc),
		pending:    make(map[string]struct{}),
		subs:       newSubscribers(),
		shutdown:   make(chan struct{}),
	}
	for _, opt := range opts {
		opt(e)
	}
	return e, nil
}

// Start holds a fresh owner lock, reconciles unavailable owners and starts workers.
func (e *Engine) Start(ctx context.Context) (resultErr error) {
	e.mu.Lock()
	if e.started || e.starting || e.stopped {
		e.mu.Unlock()
		return errors.New("run: engine already started")
	}
	e.starting = true
	e.done = make(chan struct{})
	done := e.done
	e.mu.Unlock()
	var owner *runowner.Lock
	var release func() error
	completed := false
	defer func() {
		if !completed {
			resultErr = errors.Join(resultErr, owner.Close())
			if release != nil {
				resultErr = errors.Join(resultErr, release())
			}
			e.mu.Lock()
			e.starting = false
			e.mu.Unlock()
			close(done)
		}
	}()
	var err error
	release, err = e.store.Retain()
	if err != nil {
		return err
	}
	owner, err = runowner.New(ctx, e.store.Path())
	if err != nil {
		return fmt.Errorf("run: registering owner: %w", err)
	}
	referenced, err := e.store.ActiveOwners(ctx)
	if err != nil {
		return fmt.Errorf("run: reading owner references: %w", err)
	}
	if err := runowner.Cleanup(ctx, e.store.Path(), referenced); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("run: cleaning unused owner files: %w", err)
		}
		slog.Warn("could not clean unused owner files; inspect the owner directory", "err", err)
	}
	_, err = e.store.RecoverOrphanedRuns(ctx, e.now())
	if err != nil {
		return fmt.Errorf("run: reconciling interrupted runs: %w", err)
	}
	e.mu.Lock()
	if e.stopped {
		e.mu.Unlock()
		return ErrNotRunning
	}
	e.owner = owner
	e.started, e.starting = true, false
	e.queue = make(chan string, e.queueSize)
	e.wg.Add(e.workers)
	e.mu.Unlock()
	for range e.workers {
		go e.worker()
	}
	go func() {
		e.wg.Wait()
		err := errors.Join(owner.Close(), release())
		e.subs.closeAll()
		e.mu.Lock()
		e.closeErr = err
		e.mu.Unlock()
		close(done)
	}()
	completed = true
	return nil
}

// Shutdown stops accepting work, cancels everything in flight, and waits for
// the workers to finish or ctx to expire. Skipping it leaves helm and kubectl
// processes running with no parent (ADR-0003).
func (e *Engine) Shutdown(ctx context.Context) error {
	e.mu.Lock()
	if !e.started && !e.starting && e.done == nil {
		e.stopped = true
		e.mu.Unlock()
		return nil
	}
	e.started = false
	if !e.stopped {
		e.stopped = true
		close(e.shutdown)
	}
	done := e.done

	cancels := make([]context.CancelFunc, 0, len(e.cancels))
	for _, cancel := range e.cancels {
		cancels = append(cancels, cancel)
	}
	e.mu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}

	select {
	case <-done:
		e.mu.Lock()
		err := e.closeErr
		e.mu.Unlock()
		return err
	case <-ctx.Done():
		return fmt.Errorf("run: shutdown timed out with runs still finishing: %w", ctx.Err())
	}
}

// Submit queues work and returns the new run's ID without waiting for it to
// execute; callers follow progress with Subscribe and the store (ADR-0006).
//
// If another active run holds spec.LockKey, Submit returns a
// *LockConflictError immediately instead of queueing behind it.
func (e *Engine) Submit(ctx context.Context, spec Spec) (string, error) {
	if spec.Kind == "" {
		return "", errors.New("run: spec.Kind is required")
	}
	if (spec.Script == "") == (spec.Func == nil) {
		return "", errors.New("run: exactly one of spec.Script or spec.Func is required")
	}

	e.mu.Lock()
	started := e.started
	if !started {
		e.mu.Unlock()
		return "", ErrNotRunning
	}
	e.wg.Add(1)
	owner := e.owner.ID
	e.mu.Unlock()
	defer e.wg.Done()

	// Resolve the script before recording anything, so a bad path fails here
	// instead of leaving a failed run in the history.
	if spec.Script != "" {
		if _, err := e.resolver.Resolve(spec.Script); err != nil {
			return "", err
		}
	}

	if spec.LockKey != "" {
		holder, held, err := e.store.ActiveRunForLock(ctx, spec.LockKey)
		if err != nil {
			return "", err
		}
		if held {
			since := e.now().Sub(holder.QueuedAt)
			if !holder.StartedAt.IsZero() {
				since = e.now().Sub(holder.StartedAt)
			}
			return "", &LockConflictError{LockKey: spec.LockKey, Holder: holder, Since: since}
		}
	}

	id, err := newRunID()
	if err != nil {
		return "", err
	}

	timeout := spec.Timeout
	if timeout <= 0 {
		timeout = e.timeoutFor(spec.Kind)
	}

	rec := store.Run{
		OwnerID:  owner,
		ID:       id,
		Kind:     spec.Kind,
		Target:   spec.Target,
		LockKey:  spec.LockKey,
		Actor:    spec.Actor,
		Script:   spec.Script,
		Argv:     spec.Args,
		Timeout:  timeout,
		Status:   store.StatusQueued,
		QueuedAt: e.now(),
	}
	if err := e.store.CreateRun(ctx, rec); err != nil {
		// Another writer took the lock between the check above and this
		// insert; report it as the same conflict.
		if errors.Is(err, store.ErrLockHeld) {
			if holder, held, herr := e.store.ActiveRunForLock(ctx, spec.LockKey); herr == nil && held {
				return "", &LockConflictError{LockKey: spec.LockKey, Holder: holder, Since: e.now().Sub(holder.QueuedAt)}
			}
		}
		return "", err
	}
	_ = e.store.Audit(ctx, store.AuditEntry{
		At: e.now(), Actor: spec.Actor, Action: spec.Kind,
		Target: spec.Target, RunID: id, Source: "engine",
	})

	e.mu.Lock()
	if !e.started {
		e.mu.Unlock()
		_ = e.store.FinishOwnedRun(ctx, id, owner, store.StatusCancelled, nil,
			"cancelled during engine shutdown", e.now(), 0)
		return "", ErrNotRunning
	}
	e.pending[id] = struct{}{}
	e.specs.Store(id, spec)

	select {
	case e.queue <- id:
		e.mu.Unlock()
		e.subs.publish(Event{Type: EventStatus, RunID: id, Status: store.StatusQueued})
		return id, nil
	default:
		// Saturated. Mark the run failed so it does not sit queued forever.
		delete(e.pending, id)
		e.specs.Delete(id)
		e.mu.Unlock()
		_ = e.store.FinishOwnedRun(ctx, id, owner, store.StatusFailed, nil,
			"rejected: the run queue is full", e.now(), 0)
		return "", ErrQueueFull
	}
}

func (e *Engine) timeoutFor(kind string) time.Duration {
	if e.timeouts != nil {
		if d, ok := e.timeouts[kind]; ok && d > 0 {
			return d
		}
	}
	return DefaultTimeout
}

// Cancel stops a run. A run still in the queue is finished directly; a running
// one has its context cancelled, which signals its process group.
func (e *Engine) Cancel(ctx context.Context, id string) error {
	rec, err := e.store.GetRun(ctx, id)
	if err != nil {
		return err
	}
	if rec.Status.Terminal() {
		return fmt.Errorf("%w: %s is %s", ErrNotCancellable, id, rec.Status)
	}
	if rec.RecoveryState == store.RecoveryRequired {
		return fmt.Errorf("%w: inspect the lab, then use labctl runs recover %s --force", store.ErrRecoveryRequired, id)
	}

	e.mu.Lock()
	cancel, running := e.cancels[id]
	_, queued := e.pending[id]
	if queued {
		delete(e.pending, id)
	}
	e.mu.Unlock()

	if running {
		// The worker records the terminal state once the process is gone, so
		// the outcome is written exactly once.
		cancel()
		return nil
	}

	if queued {
		_, _ = e.store.AppendLogs(ctx, id, []store.LogLine{{
			At: e.now(), Stream: store.StreamSystem, Text: "cancelled before it started",
		}})
		if err := e.store.FinishOwnedRun(ctx, id, e.owner.ID, store.StatusCancelled, nil,
			"cancelled by user", e.now(), 0); err != nil {
			return err
		}
		e.subs.publish(Event{Type: EventStatus, RunID: id, Status: store.StatusCancelled})
		return nil
	}

	return fmt.Errorf("%w: %s is not tracked by this engine", ErrNotCancellable, id)
}

// newRunID returns an ID of the form run_<unix-millis>_<random hex>. The time
// prefix makes IDs sort chronologically; the random suffix keeps two runs
// submitted in the same millisecond apart.
func newRunID() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("run: generating an ID: %w", err)
	}
	return fmt.Sprintf("run_%d_%s", time.Now().UnixMilli(), hex.EncodeToString(b[:])), nil
}
