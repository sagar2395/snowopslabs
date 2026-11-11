// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snowops-oss/snowopslabs/internal/config"
)

func TestDomainMove(t *testing.T) {
	tests := []struct {
		name  string
		cfg   config.Config
		want  string
		moves bool
	}{
		{name: "a k3d lab on the old default moves", cfg: config.Config{Profile: "k3d", ClusterName: "snowops", DomainSuffix: "k3d.local"}, want: "snowops.localhost", moves: true},
		{name: "a kind lab on the old default moves", cfg: config.Config{Profile: "kind", ClusterName: "lab2", DomainSuffix: "kind.local"}, want: "lab2.localhost", moves: true},
		{name: "a lab already on localhost stays", cfg: config.Config{Profile: "k3d", ClusterName: "snowops", DomainSuffix: "snowops.localhost"}},
		{name: "a suffix the user chose stays", cfg: config.Config{Profile: "k3d", ClusterName: "snowops", DomainSuffix: "k3d.local", DomainSuffixPinned: true}},
		{name: "a custom suffix stays", cfg: config.Config{Profile: "k3d", ClusterName: "snowops", DomainSuffix: "lab.internal"}},
		{name: "an incluster lab stays", cfg: config.Config{Profile: "incluster", ClusterName: "snowops", DomainSuffix: "k3d.local"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, moves := domainMove(&tt.cfg)
			if got != tt.want || moves != tt.moves {
				t.Errorf("domainMove = (%q, %v), want (%q, %v)", got, moves, tt.want, tt.moves)
			}
		})
	}
}

func TestMoveLegacyDomain(t *testing.T) {
	tests := []struct {
		name       string
		suffix     string
		script     string
		wantErr    string
		wantSuffix string
		wantOutput string
	}{
		{name: "the move is recorded and every URL follows it", suffix: "k3d.local",
			script:     `printf 'HTTP_PORT=8080\nHTTPS_PORT=8443\nDOMAIN_SUFFIX=%s\n' "$3" >"$SNOWOPS_HOME/clusters/$1.env"` + "\n",
			wantSuffix: "lab.localhost", wantOutput: "from *.k3d.local to *.lab.localhost"},
		{name: "a failed move says how to retry or keep the old names", suffix: "k3d.local", script: "exit 1\n",
			wantErr: "DOMAIN_SUFFIX=k3d.local", wantSuffix: "k3d.local"},
		{name: "a lab on localhost runs nothing", suffix: "lab.localhost", script: "exit 1\n", wantSuffix: "lab.localhost"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withLab(t, "k3d", "8080")
			// reloadIngress re-reads the config, which names the cluster from the
			// environment in a lab without .env.
			t.Setenv("CLUSTER_NAME", cfg.ClusterName)
			cfg.DomainSuffix = tt.suffix
			if err := os.MkdirAll(filepath.Join(os.Getenv("SNOWOPS_HOME"), "clusters"), 0o750); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(cfg.ProjectRoot, "runtimes", "_lib")
			if err := os.MkdirAll(filepath.Join(cfg.ProjectRoot, "runtimes", "k3d"), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(dir, 0o750); err != nil {
				t.Fatal(err)
			}
			//nolint:gosec // the test script must be executable
			if err := os.WriteFile(filepath.Join(dir, "move-domain.sh"), []byte("#!/bin/sh\n"+tt.script), 0o755); err != nil {
				t.Fatal(err)
			}

			var out bytes.Buffer
			err := moveLegacyDomain(&out)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			}
			if cfg.DomainSuffix != tt.wantSuffix {
				t.Errorf("DomainSuffix = %q, want %q", cfg.DomainSuffix, tt.wantSuffix)
			}
			if !strings.Contains(out.String(), tt.wantOutput) {
				t.Errorf("output should mention %q:\n%s", tt.wantOutput, out.String())
			}
		})
	}
}
