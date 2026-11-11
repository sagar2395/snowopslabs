// SPDX-License-Identifier: Apache-2.0

package platform

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	catalog "github.com/snowops-oss/snowopslabs/internal/platform"
)

// stubKubectl puts a fake kubectl first on PATH. script is the body of a POSIX
// shell script that sees kubectl's arguments as "$*".
func stubKubectl(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte("#!/bin/sh\n"+script), 0o755); err != nil { //nolint:gosec // test stub must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// fixtureRegistry is a registry over providers that each have an install.sh:
// two with their own namespace and two that share the monitoring namespace.
func fixtureRegistry(t *testing.T) *catalog.Registry {
	t.Helper()
	root := t.TempDir()
	for _, comp := range []string{"autoscaling/keda", "secrets/vault", "monitoring/grafana", "logging/loki"} {
		writeScript(t, filepath.Join(root, "platform", filepath.FromSlash(comp), "install.sh"))
	}
	return catalog.NewRegistry(root)
}

func TestInstalled(t *testing.T) {
	tests := []struct {
		name    string
		kubectl string
		want    []string
		wantErr bool
	}{
		{
			name: "a namespace or a shared-namespace release marks a provider installed",
			kubectl: `case "$*" in
  *"get namespaces"*) printf 'namespace/keda\nnamespace/monitoring\n' ;;
  *"owner=helm"*) echo 'monitoring grafana' ;;
esac`,
			want: []string{"autoscaling/keda", "monitoring/grafana"},
		},
		{
			name: "the shared namespace alone does not mark its providers installed",
			kubectl: `case "$*" in
  *"get namespaces"*) echo namespace/monitoring ;;
esac`,
			want: []string{},
		},
		{
			name: "a release left by a failed install does not mark its provider installed",
			kubectl: `case "$*" in
  *"get namespaces"*) echo namespace/monitoring ;;
  *"owner=helm,status=deployed"*) exit 0 ;;
  *"owner=helm"*) echo 'monitoring loki' ;;
esac`,
			want: []string{},
		},
		{
			name:    "a cluster that cannot be read is an error, not an empty lab",
			kubectl: "echo 'Unable to connect to the server' >&2; exit 1\n",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubKubectl(t, tt.kubectl)
			got, err := Installed(t.Context(), fixtureRegistry(t))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Installed() = %v, want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Installed() error = %v", err)
			}
			names := slices.Sorted(maps.Keys(got))
			if !slices.Equal(names, tt.want) {
				t.Errorf("Installed() = %v, want %v", names, tt.want)
			}
		})
	}
}
