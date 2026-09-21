// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"fmt"
	"sync"

	"github.com/sagar2395/snowopslabs/internal/config"
	"github.com/sagar2395/snowopslabs/internal/incident"
	"github.com/sagar2395/snowopslabs/internal/scenario"
	"github.com/sagar2395/snowopslabs/internal/workload"
)

// WorkloadResp is the workload a scenario or fault runs against: which app,
// deployed where, and how to reach it. Every scenario and fault response
// includes it, so the UI can show and change the app.
type WorkloadResp struct {
	App       string `json:"app"`
	Namespace string `json:"namespace"`
	Service   string `json:"service"`
	Port      string `json:"port"`
	Metric    string `json:"metric"`
}

func workloadResp(w workload.Workload) WorkloadResp {
	w = w.WithDefaults()
	return WorkloadResp{
		App:       w.Name,
		Namespace: w.Namespace,
		Service:   w.Service(),
		Port:      w.Port,
		Metric:    w.Metric,
	}
}

// bindMu serialises operations that need a workload binding: activation,
// injection, teardown and verification. They set WORKLOAD_* on the shared
// executor, so two of them for different apps must not overlap.
//
// Read handlers must not take it: an injection can hold it for minutes. They
// use boundWorkload instead.
var bindMu sync.Mutex

// workloadEnvKeys are the script-environment variables a binding owns.
var workloadEnvKeys = []string{"WORKLOAD_NAME", "WORKLOAD_NAMESPACE", "WORKLOAD_PORT", "WORKLOAD_METRIC"}

// withWorkload takes bindMu and returns copies of both engines bound to app,
// plus a release function that restores the executor environment and unlocks.
// The shared engines are never rebound. It also sets WORKLOAD_* on the shared
// executor, because component and fault scripts read the app from there. An
// empty app keeps the current binding.
func (s *Server) withWorkload(app string) (*scenario.Engine, *incident.Engine, func(), error) {
	bindMu.Lock()

	scenes, incidents := s.scenes, s.incidents
	if scenes != nil {
		scenes = scenes.Clone()
	}
	if incidents != nil {
		incidents = incidents.Clone()
	}

	var prevEnv map[string]string
	if s.exec != nil {
		prevEnv = make(map[string]string, len(workloadEnvKeys))
		for _, k := range workloadEnvKeys {
			prevEnv[k] = s.exec.GetEnv(k)
		}
	}
	release := func() {
		for k, v := range prevEnv {
			s.exec.SetEnv(k, v)
		}
		bindMu.Unlock()
	}

	current := ""
	switch {
	case scenes != nil:
		current = scenes.Workload.Name
	case incidents != nil:
		current = incidents.Workload.Name
	}
	if app == "" || app == current {
		return scenes, incidents, release, nil
	}

	cfg, err := config.LoadAppConfig(s.cfg.ProjectRoot, app)
	if err != nil {
		release()
		return nil, nil, func() {}, fmt.Errorf("app %s: %w", app, err)
	}
	bound := cfg.Workload()
	if scenes != nil {
		scenes.Workload, scenes.Contract = bound, cfg.Contract
	}
	if incidents != nil {
		incidents.Workload = bound
	}
	if s.exec != nil {
		s.exec.SetEnv("WORKLOAD_NAME", bound.Name)
		s.exec.SetEnv("WORKLOAD_NAMESPACE", bound.Namespace)
		s.exec.SetEnv("WORKLOAD_PORT", bound.Port)
		s.exec.SetEnv("WORKLOAD_METRIC", bound.Metric)
	}
	return scenes, incidents, release, nil
}

// boundWorkload returns the binding for the named app without changing the
// engines, or the current binding if the name is empty or unknown. Read
// handlers use it instead of withWorkload.
func (s *Server) boundWorkload(app string) workload.Workload {
	current := workload.Workload{}
	if s.scenes != nil {
		current = s.scenes.Workload
	} else if s.incidents != nil {
		current = s.incidents.Workload
	}
	if app == "" || app == current.Name {
		return current
	}
	cfg, err := config.LoadAppConfig(s.cfg.ProjectRoot, app)
	if err != nil {
		return current
	}
	return cfg.Workload()
}

// activeIncidentWorkload returns the binding the active fault was injected
// into, or the current binding when no incident is active.
func (s *Server) activeIncidentWorkload() workload.Workload {
	app := ""
	if s.incidents != nil {
		if a, err := s.incidents.Active(); err == nil && a != nil {
			app = a.App
		}
	}
	return s.boundWorkload(app)
}
