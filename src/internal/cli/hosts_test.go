// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestMergeHosts(t *testing.T) {
	tests := []struct {
		name       string
		subdomains []string
		discovered []string
		want       []string
	}{
		{
			name:       "subdomains and discovered hosts, sorted and de-duplicated",
			subdomains: []string{"grafana", "echo-server"},
			discovered: []string{"echo-server-dev.k3d.local", "grafana.k3d.local"},
			want:       []string{"echo-server-dev.k3d.local", "echo-server.k3d.local", "grafana.k3d.local"},
		},
		{
			name:       "hosts outside the domain suffix are left alone",
			subdomains: []string{"grafana"},
			discovered: []string{"api.example.com", "k3d.local", "x.k3d.local.evil.io"},
			want:       []string{"grafana.k3d.local"},
		},
		{
			name:       "an unreachable cluster still yields the listed hosts",
			subdomains: []string{"vault"},
			want:       []string{"vault.k3d.local"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mergeHosts("k3d.local", tt.subdomains, tt.discovered); !slices.Equal(got, tt.want) {
				t.Errorf("mergeHosts() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestManagedHosts(t *testing.T) {
	content := "127.0.0.1 localhost other.k3d.local\n" + buildBlock([]string{"a.k3d.local", "b.k3d.local"}) + "::1 localhost\n"
	got := managedHosts(content)
	if len(got) != 2 || !got["a.k3d.local"] || !got["b.k3d.local"] {
		t.Errorf("managedHosts() = %v, want only the two managed hosts", got)
	}
	if n := len(managedHosts("127.0.0.1 localhost\n")); n != 0 {
		t.Errorf("a file with no managed block listed %d hosts", n)
	}
}

func TestUnlistedHosts(t *testing.T) {
	listed := map[string]bool{"grafana.k3d.local": true}
	got := unlistedHosts("k3d.local", []string{"grafana.k3d.local", "go-api-prod.k3d.local", "elsewhere.io"}, listed)
	if !slices.Equal(got, []string{"go-api-prod.k3d.local"}) {
		t.Errorf("unlistedHosts() = %v, want [go-api-prod.k3d.local]", got)
	}
}

func TestPrintHostsSummary(t *testing.T) {
	tests := []struct {
		name   string
		before map[string]bool
		want   string
	}{
		{name: "new hosts are listed", before: map[string]bool{"a.k3d.local": true}, want: "2 hostnames, 1 new:\n  + b.k3d.local\n"},
		{name: "nothing new", before: map[string]bool{"a.k3d.local": true, "b.k3d.local": true}, want: "2 hostnames, none new.\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			printHostsSummary(&buf, []string{"a.k3d.local", "b.k3d.local"}, tt.before)
			if !strings.HasSuffix(buf.String(), tt.want) {
				t.Errorf("summary = %q, want suffix %q", buf.String(), tt.want)
			}
		})
	}
}

func TestComputeHostsContent(t *testing.T) {
	block := buildBlock([]string{"grafana.k3d.local", "argocd.k3d.local"})

	tests := []struct {
		name     string
		existing string
		block    string
		want     string
	}{
		{
			name:     "add to plain file",
			existing: "127.0.0.1 localhost\n",
			block:    block,
			want:     "127.0.0.1 localhost\n" + block,
		},
		{
			name:     "add to file without trailing newline",
			existing: "127.0.0.1 localhost",
			block:    block,
			want:     "127.0.0.1 localhost\n" + block,
		},
		{
			name:     "replace existing managed block",
			existing: "127.0.0.1 localhost\n" + buildBlock([]string{"old.host"}),
			block:    block,
			want:     "127.0.0.1 localhost\n" + block,
		},
		{
			name:     "remove managed block",
			existing: "127.0.0.1 localhost\n" + block,
			block:    "",
			want:     "127.0.0.1 localhost\n",
		},
		{
			name:     "remove when no managed block present",
			existing: "127.0.0.1 localhost\n",
			block:    "",
			want:     "127.0.0.1 localhost\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := computeHostsContent(tt.existing, tt.block); got != tt.want {
				t.Errorf("computeHostsContent()\n got: %q\nwant: %q", got, tt.want)
			}
		})
	}
}

// TestComputeHostsContent_AddRemoveRoundTrip ensures adding then removing the
// managed block returns the file to its original single-trailing-newline form,
// and that a second add does not duplicate the block.
func TestComputeHostsContent_AddRemoveRoundTrip(t *testing.T) {
	block := buildBlock(mergeHosts("k3d.local", platformSubdomains, nil))
	base := "127.0.0.1 localhost\n255.255.255.255 broadcasthost\n"

	added := computeHostsContent(base, block)
	if strings.Count(added, hostsBegin) != 1 {
		t.Fatalf("expected exactly one managed block after add, got %d", strings.Count(added, hostsBegin))
	}
	// Adding again must still leave exactly one block (idempotent).
	addedTwice := computeHostsContent(added, block)
	if strings.Count(addedTwice, hostsBegin) != 1 {
		t.Errorf("add is not idempotent: %d managed blocks", strings.Count(addedTwice, hostsBegin))
	}
	removed := computeHostsContent(addedTwice, "")
	if strings.Contains(removed, hostsBegin) {
		t.Error("managed block still present after remove")
	}
	if removed != base {
		t.Errorf("round trip did not restore original:\n got: %q\nwant: %q", removed, base)
	}
}

// TestWriteManagedHostsFile_Atomic verifies the write lands, leaves no temp
// files behind, and preserves the file's existing permission bits.
func TestWriteManagedHostsFile_Atomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts")

	const mode = 0o600 // deliberately not 0644, to prove the mode is preserved
	if err := os.WriteFile(path, []byte("127.0.0.1 localhost\n"), mode); err != nil {
		t.Fatal(err)
	}

	block := buildBlock(mergeHosts("k3d.local", platformSubdomains, nil))
	if err := writeManagedHostsFile(path, block); err != nil {
		t.Fatalf("writeManagedHostsFile: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), hostsBegin) {
		t.Error("managed block not written")
	}

	// Mode preservation is a POSIX concern; skip the bit check on Windows.
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != os.FileMode(mode) {
			t.Errorf("permission not preserved: got %o, want %o", fi.Mode().Perm(), mode)
		}
	}

	// No temp files (".snowops-hosts-*") must be left in the directory.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".snowops-hosts-") {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}
