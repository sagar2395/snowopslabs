// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

const (
	hostsFile  = "/etc/hosts"
	hostsBegin = "# BEGIN snowops-labs"
	hostsEnd   = "# END snowops-labs"
)

// knownSubdomains are the ingress hostnames managed in /etc/hosts.
var knownSubdomains = []string{
	"go-api",
	"go-api-dev",     // env-promotion: dev environment ingress
	"go-api-staging", // env-promotion: staging environment ingress
	"go-api-prod",    // env-promotion: prod environment ingress
	"echo-server",
	"grafana",
	"prometheus",
	"argocd",
	"traefik",
	"kubernetes-dashboard",
	"chaos", // chaos-engineering: Chaos Mesh dashboard ingress
}

var hostsCmd = &cobra.Command{
	Use:   "hosts",
	Short: "Manage /etc/hosts entries for cluster ingress hostnames",
}

var hostsAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add (or update) the managed /etc/hosts block",
	RunE: func(cmd *cobra.Command, args []string) error {
		if os.Getuid() != 0 {
			return reexecWithSudo()
		}
		hosts := buildHostList(cfg.DomainSuffix)
		return writeHostsBlock(buildBlock(hosts))
	},
}

var hostsRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove the managed /etc/hosts block",
	RunE: func(cmd *cobra.Command, args []string) error {
		if os.Getuid() != 0 {
			return reexecWithSudo()
		}
		return writeHostsBlock("")
	},
}

func init() {
	hostsCmd.AddCommand(hostsAddCmd)
	hostsCmd.AddCommand(hostsRemoveCmd)
	rootCmd.AddCommand(hostsCmd)
}

// hostsBlockPresent reports whether the managed block is already in /etc/hosts.
// Reading the file needs no privileges, so init/doctor can advise without sudo.
func hostsBlockPresent() bool {
	data, err := os.ReadFile(hostsFile)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), hostsBegin)
}

func buildHostList(domainSuffix string) []string {
	hosts := make([]string, len(knownSubdomains))
	for i, sub := range knownSubdomains {
		hosts[i] = sub + "." + domainSuffix
	}
	return hosts
}

func buildBlock(hosts []string) string {
	return hostsBegin + "\n" +
		"127.0.0.1 " + strings.Join(hosts, " ") + "\n" +
		hostsEnd + "\n"
}

// reexecWithSudo re-runs the current invocation under sudo, preserving all flags.
func reexecWithSudo() error {
	fmt.Fprintln(os.Stderr, "Root required — re-running with sudo...")
	c := osexec.Command("sudo", append([]string{os.Args[0]}, os.Args[1:]...)...) //nolint:gosec,noctx // re-exec of this same CLI under sudo; a context would not manage the replacement process
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

// writeHostsBlock replaces the managed block in /etc/hosts.
// Pass an empty string to remove the block.
func writeHostsBlock(block string) error {
	if err := writeManagedHostsFile(hostsFile, block); err != nil {
		return err
	}
	if block != "" {
		fmt.Printf("Added managed block to %s\n", hostsFile)
	} else {
		fmt.Printf("Removed managed block from %s\n", hostsFile)
	}
	return nil
}

// writeManagedHostsFile rewrites path with the managed block replaced (or removed
// when block is empty). The write is atomic — a temp file in the same directory
// swapped in by rename — so a crash or a full disk can never leave /etc/hosts
// half-written and unusable. The file's existing permission bits are preserved.
func writeManagedHostsFile(path, block string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	result := computeHostsContent(string(data), block)

	// Preserve the current mode (0644 for a not-yet-existing file). Ownership
	// follows the rename's new inode; this runs as root against a root-owned
	// /etc/hosts, so root:root/​wheel is what we want anyway.
	perm := os.FileMode(0o644)
	if fi, statErr := os.Stat(path); statErr == nil {
		perm = fi.Mode().Perm()
	}

	if err := atomicWriteFile(path, []byte(result), perm); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// computeHostsContent returns the contents of a hosts file with the managed
// block stripped and, when block is non-empty, the new block appended. It is
// pure so the block-rewriting logic can be tested without touching /etc/hosts.
func computeHostsContent(existing, block string) string {
	lines := strings.Split(existing, "\n")
	var out []string
	inBlock := false
	for _, line := range lines {
		if line == hostsBegin {
			inBlock = true
			continue
		}
		if inBlock {
			if line == hostsEnd {
				inBlock = false
			}
			continue
		}
		out = append(out, line)
	}

	// Drop trailing blank lines produced by the split.
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}

	result := strings.Join(out, "\n")
	if block != "" {
		if result != "" {
			result += "\n"
		}
		result += block
	} else {
		result += "\n"
	}
	return result
}

// atomicWriteFile writes data to a temp file in path's directory and renames it
// over path, so readers ever see only the complete old or complete new file.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".snowops-hosts-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Best-effort cleanup if we bail before the rename; a no-op once renamed.
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
