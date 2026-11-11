// SPDX-License-Identifier: Apache-2.0

// Package traffic submits traffic scripts as recorded, cancellable runs so
// callers share validation, timeouts and locking.
package traffic

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path"
	"time"

	"github.com/snowops-oss/snowopslabs/internal/run"
	trafficopts "github.com/snowops-oss/snowopslabs/internal/traffic"
)

// LockKey prevents traffic start and stop from changing the same workload at once.
const LockKey = "traffic"

// Run kinds identify traffic operations in durable history.
const (
	KindStart  = "traffic.start"
	KindStop   = "traffic.stop"
	KindStatus = "traffic.status"
)

// Service validates traffic options and submits the scripts to the run engine.
type Service struct {
	engine      *run.Engine
	projectRoot string
	env         map[string]string
}

// New builds a service with its own copy of the configured script environment.
func New(engine *run.Engine, projectRoot string, env map[string]string) (*Service, error) {
	if engine == nil {
		return nil, errors.New("traffic: a run engine is required")
	}
	if projectRoot == "" {
		return nil, errors.New("traffic: a project root is required; pass the lab checkout directory")
	}
	base := make(map[string]string, len(env))
	maps.Copy(base, env)
	return &Service{engine: engine, projectRoot: projectRoot, env: base}, nil
}

// Start validates the requested traffic and returns its run ID without waiting.
func (s *Service) Start(ctx context.Context, opts trafficopts.Options) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("traffic: starting traffic: %w", err)
	}
	if err := opts.Validate(s.projectRoot); err != nil {
		return "", err
	}
	env := s.environment()
	maps.Copy(env, opts.Env())
	return s.submit(
		ctx,
		KindStart,
		"start.sh",
		env,
	)
}

// Stop submits traffic cleanup and returns its run ID without waiting.
func (s *Service) Stop(ctx context.Context) (string, error) {
	return s.submit(
		ctx,
		KindStop,
		"stop.sh",
		s.environment(),
	)
}

// Status submits a live status check without taking the mutation lock.
func (s *Service) Status(ctx context.Context) (string, error) {
	return s.submit(
		ctx,
		KindStatus,
		"status.sh",
		s.environment(),
	)
}

func (s *Service) environment() map[string]string {
	env := make(map[string]string, len(s.env))
	maps.Copy(env, s.env)
	return env
}

func (s *Service) submit(ctx context.Context, kind, script string, env map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("traffic: submitting %s: %w", kind, err)
	}
	lock, timeout := LockKey, 2*time.Minute
	if kind == KindStatus {
		lock, timeout = "", 30*time.Second
	}
	return s.engine.Submit(ctx, run.Spec{
		Kind:    kind,
		Target:  "traffic",
		LockKey: lock,
		Script:  path.Join(trafficopts.ScriptDir, script),
		Env:     env,
		Dir:     s.projectRoot,
		Timeout: timeout,
	})
}
