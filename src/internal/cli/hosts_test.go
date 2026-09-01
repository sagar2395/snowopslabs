// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBuildHostList_IncludesOpenCost(t *testing.T) {
	hosts := buildHostList("k3d.local")
	found := false
	for _, h := range hosts {
		if h == "opencost.k3d.local" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("buildHostList missing opencost.k3d.local (needed for the OpenCost UI ingress); got %v", hosts)
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
	block := buildBlock(buildHostList("k3d.local"))
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

	block := buildBlock(buildHostList("k3d.local"))
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
