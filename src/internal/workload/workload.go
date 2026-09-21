// SPDX-License-Identifier: Apache-2.0

// Package workload describes the application a scenario or fault is bound to.
//
// Content names the binding ({{.WorkloadName}}, {{.WorkloadService}}) rather
// than a literal app, so one scenario runs unchanged against a built-in app or
// one the user brings. See ADR-0014.
package workload

import (
	"errors"
	"fmt"
	"strings"
)

// DefaultApp is the app scenarios bind to unless the user picks another. The
// APP_NAME default in internal/config uses this constant.
const DefaultApp = "go-api"

// DefaultPort is the HTTP port the app contract requires a workload to serve on.
const DefaultPort = "8080"

// DefaultMetric is the default request-duration histogram name. It follows the
// OpenTelemetry semantic conventions, which many instrumented apps already use.
const DefaultMetric = "http_server_request_duration_seconds"

// Workload is a resolved binding: which app, deployed where, reachable how.
type Workload struct {
	// Name is the app and its Deployment name, e.g. "go-api".
	Name string
	// Namespace is where it is deployed. Conventionally the same as Name.
	Namespace string
	// Port is the port serving HTTP.
	Port string
	// Metric is the request-duration histogram the app exposes. Each
	// language's instrumentation library picks its own name.
	Metric string
}

// Default returns the conventional binding for an app that follows the repo's
// layout: deployed into a namespace of its own name, serving HTTP on 8080, and
// exposing the semconv request histogram.
func Default(name string) Workload {
	return Workload{Name: name, Namespace: name, Port: DefaultPort, Metric: DefaultMetric}
}

// WithDefaults fills any field the caller left empty, so a partial binding from
// an app manifest never resolves to "deployment/" or a portless URL.
func (w Workload) WithDefaults() Workload {
	if w.Namespace == "" {
		w.Namespace = w.Name
	}
	if w.Port == "" {
		w.Port = DefaultPort
	}
	if w.Metric == "" {
		w.Metric = DefaultMetric
	}
	return w
}

// Service is the workload's in-cluster DNS name.
func (w Workload) Service() string {
	if w.Name == "" || w.Namespace == "" {
		return ""
	}
	return fmt.Sprintf("%s.%s.svc.cluster.local", w.Name, w.Namespace)
}

// URL is the in-cluster base URL callers use to drive traffic at the workload.
func (w Workload) URL() string {
	svc := w.Service()
	if svc == "" {
		return ""
	}
	return fmt.Sprintf("http://%s:%s/", svc, w.WithDefaults().Port)
}

// Validate reports why a binding cannot be used. Call it before installing
// anything.
func (w Workload) Validate() error {
	if strings.TrimSpace(w.Name) == "" {
		return errors.New("workload has no name")
	}
	if strings.TrimSpace(w.Namespace) == "" {
		return fmt.Errorf("workload %q has no namespace", w.Name)
	}
	if strings.TrimSpace(w.Port) == "" {
		return fmt.Errorf("workload %q has no port", w.Name)
	}
	return nil
}
