// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/snowops-oss/snowopslabs/internal/config"
	"github.com/snowops-oss/snowopslabs/internal/executor"
	"github.com/snowops-oss/snowopslabs/internal/incident"
	"github.com/snowops-oss/snowopslabs/internal/labtest"
	"github.com/snowops-oss/snowopslabs/internal/platform"
	"github.com/snowops-oss/snowopslabs/internal/runtime"
	"github.com/snowops-oss/snowopslabs/internal/scenario"
	"github.com/snowops-oss/snowopslabs/internal/services"
	"github.com/snowops-oss/snowopslabs/internal/store"
	"github.com/snowops-oss/snowopslabs/internal/toolchain"
)

// The contract tests run the real server over the fixture lab in
// internal/labtest: one of everything the API serves, with kubectl, helm and
// docker replaced by scripts on PATH that answer as a healthy cluster.

const (
	labApp       = labtest.App
	labScenario  = labtest.Scenario
	labFault     = labtest.Fault
	labPath      = labtest.Path
	labChallenge = labtest.Challenge
	labService   = labtest.Service
	labRuntime   = labtest.Runtime
)

const script = "#!/usr/bin/env bash\nset -eu\n"

// newContractLab returns the real server over a private copy of the fixture
// lab, with auth off.
func newContractLab(t *testing.T) *Server {
	t.Helper()
	return contractServer(t, labtest.New(t))
}

// contractServer builds the real server over lab. It reads the auth settings
// from the environment as the server does, so a caller turns auth on by
// setting LABCTL_AUTH after building the lab.
func contractServer(t *testing.T, lab *labtest.Lab) *Server {
	t.Helper()
	root := lab.Root

	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "runs.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cfg := &config.Config{
		ProjectRoot: root, StateDir: filepath.Join(root, ".labctl"),
		Profile: labRuntime, ClusterName: "contract",
		DomainSuffix: "lab.test", MonitoringNamespace: "monitoring",
		IngressProvider: "traefik", MetricsProvider: "prometheus",
		LoggingProvider: "loki", TracingProvider: "tempo",
	}
	s := NewServer(cfg, executor.New(root), platform.NewRegistry(root),
		scenario.NewEngine(root, cfg.DomainSuffix, cfg.Profile),
		incident.NewEngine(root, cfg.DomainSuffix),
		services.NewRegistry(root), runtime.NewManager(root, cfg.ClusterName),
		nil, WithRunStore(st))
	s.docker = toolchain.NewFake()
	return s
}

func writeLabFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	mode := os.FileMode(0o644)
	if filepath.Ext(path) == ".sh" {
		mode = 0o755
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}
