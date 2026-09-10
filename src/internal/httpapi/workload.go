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

// WorkloadResp is the binding a piece of content will run against: which app,
// deployed where, reachable how.
//
// It travels with every scenario and fault the API serves. Content names the
// binding rather than an app (ADR-0014), so without this the UI could only show
// the resolved strings and had no way to say what produced them — or to offer
// another app.
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

// bindMu serialises the operations that CHANGE the engines' workload binding —
// activation, injection, teardown, verification. The engines are shared, so two
// of those against different apps must not interleave.
//
// Read paths must never take it. An injection holds it for minutes, and a list
// endpoint that waited on it made the whole UI hang for the length of the
// injection it was displaying. Reads resolve against an explicit binding from
// boundWorkload instead, which mutates nothing.
var bindMu sync.Mutex

// workloadEnvKeys are the script-environment variables a binding owns.
var workloadEnvKeys = []string{"WORKLOAD_NAME", "WORKLOAD_NAMESPACE", "WORKLOAD_PORT", "WORKLOAD_METRIC"}

// withWorkload binds both engines to app for the duration of one operation and
// returns the restore function. An empty app keeps the current binding, so a
// client that does not care about the workload behaves exactly as before.
//
// The executor environment is bound too: component and fault scripts read
// WORKLOAD_* from it, so a binding that stopped at the engines would grade one
// app while the scripts acted on another.
// withWorkload prepares the engines for one mutating operation bound to app, and
// returns them alongside the function that ends the operation.
//
// The engines it hands back are clones: the shared ones are never rebound, so a
// list request served while an injection runs is unaffected by it. What is
// genuinely shared — the executor environment the scripts read WORKLOAD_* from —
// is what the lock protects, and it is restored on release.
//
// An empty app keeps the current binding, so a client that does not care about
// the workload behaves exactly as before.
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

// boundWorkload resolves an app name to a binding without touching the engines,
// falling back to whatever is currently bound. Read handlers use it to describe
// content against the right app while another request is mid-injection.
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

// activeIncidentWorkload is the binding the active fault was injected into, or
// the current one when nothing is active.
func (s *Server) activeIncidentWorkload() workload.Workload {
	app := ""
	if s.incidents != nil {
		if a, err := s.incidents.Active(); err == nil && a != nil {
			app = a.App
		}
	}
	return s.boundWorkload(app)
}
